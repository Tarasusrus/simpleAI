# Requirements: Financial Advisor Skill

## 1. Functional Requirements

### FR-1. Auto-routed advisor skill
- Бот должен распознавать финансовые вопросы пользователя ("можем купить X?", "хватит ли денег?", "что приоритетнее?") и автоматически направлять их в advisor skill.
- Триггер — manifest-based routing через LLM-роутер `agent.Service` (как существующие skill'ы). Никакой явной команды (`/advice`).
- Manifest description должен явно отделять advisor от существующих action'ов budget_skill (`summary`, `forecast`, `debt_status`).

### FR-2. Финансовый снэпшот
- Skill при каждом вызове собирает снэпшот **одним SQL-запросом (CTE)** по таблицам:
  - `budget_transaction` — расходы/доходы текущего месяца
  - `budget_category` — категории
  - `budget_recurring` — повторяющиеся платежи с `next_run_at <= end_of_month`
  - `budget_debt` — открытые долги (`is_closed = false`) с дедлайном в текущем месяце
- Все суммы конвертируются в **THB** через текущий `exchange_rate` (как в forecast skill).
- Прогноз остатка месяца — переиспользовать SQL-логику `GetForecastData()` (avg + trend по категориям).

### FR-3. Метрика «свободные деньги»
- Главная цифра ответа: `free_cash = balance_mtd − upcoming_recurring_mtd − active_debt_due_mtd`
  - `balance_mtd` = доходы месяца − расходы месяца (THB)
  - `upcoming_recurring_mtd` = сумма повторяющихся платежей с `next_run_at` ≤ конец месяца
  - `active_debt_due_mtd` = сумма открытых долгов с `due_date` ≤ конец месяца
- Горизонт расчёта — конец календарного месяца (как forecast skill).

### FR-4. Конверсия валют в вопросе
- Если в вопросе указана сумма в валюте отличной от THB (напр. RUB, USD), skill конвертирует её в THB по текущему `exchange_rate`.
- В ответе показываются обе суммы: исходная и THB-эквивалент.
- Распознавание валюты — на стороне LLM (передаётся в input как `amount` + `currency`).

### FR-5. Формат ответа (всегда развёрнутый)
Структура ответа:
1. **Вердикт**: `Да` / `Нет` / `Условно`
2. **Ключевые цифры (3 штуки)**: free_cash, остаток до конца месяца (по прогнозу), сумма обязательств
3. **Краткое объяснение** (2–3 предложения): почему такой вердикт, на что обратить внимание (приоритетные траты, долги, превышение категорий)
4. (опц.) **Рекомендация**: альтернативы или предупреждения если вердикт «Условно» / «Нет»

Без separate "follow-up details" — всегда полный ответ за один вызов.

### FR-6. Multi-user / общий бюджет
- Один общий бюджет на весь инстанс бота (отражает текущую schema: `budget_transaction` и `budget_debt` не имеют `chat_id`).
- `chat_id` используется skill'ом только для таблиц где он есть: `budget_recurring` (фильтрация) и `budget_reminder`.
- Skill использует `agent.ChatIDKey{}` из контекста как и budget_skill.
- Никакой per-user сегрегации внутри одного инстанса — taras + жена работают с общим бюджетом.

### FR-7. Stateless
- Каждый вопрос обрабатывается независимо.
- Никакой conversation history, никаких follow-up'ов с памятью предыдущего ответа.

### FR-8. Логирование
- slog: `skill="advisor"`, `chat_id`, `question`, `verdict`, `free_cash_thb`, `duration_ms`.
- Ошибки SQL/LLM — обычная цепочка `slog.Error` + возврат пользовательской ошибки.

## 2. Non-Functional Requirements

### NFR-1. Latency
- Цель: ≤ 4 сек на ответ (1 SQL + 1 LLM call).

### NFR-2. Стек
- Go 1.25, без новых зависимостей.
- Реализация — отдельный файл `internal/skills/advisor_skill.go` либо новый action в `budget_skill.go` (выбор архитектуры — на этапе constraints).
- LLM — DeepSeek (primary) / Gemini (fallback), как сейчас.

### NFR-3. Тесты
- Unit-тест на SQL снэпшота с фикстурами (in-memory или testcontainers — посмотреть как уже принято в проекте).
- Unit-тест на формирование промпта (snapshot → prompt).
- Smoke-тест на manifest routing (мок LLM → проверка что вопрос «можем купить X» роутится в advisor).

## 3. Data Sources

| Источник | Колонки | Использование |
|----------|---------|----------------|
| `budget_transaction` | `amount, currency, type (income/expense), category_id, occurred_at` | balance_mtd, расходы по категориям |
| `budget_category` | `id, title` | название категории в ответе |
| `budget_recurring` | `amount, currency, next_run_at, type` | upcoming_recurring_mtd |
| `budget_debt` | `amount_total, amount_paid, currency, due_date, is_closed, creditor` | active_debt_due_mtd |
| `exchange_rate` | `from_currency, to_currency, rate` | конверсия в THB |
| `forecast` (CTE из `GetForecastData`) | per-category avg + trend | прогноз остатка месяца |

## 4. Out of Scope

- RAG / индексация книг по финсоветам (отдельный итерация, связан с issue #43)
- Команда `/advice` (явный триггер)
- Multi-step agent loop (вызов других skill'ов как tools)
- Conversation history / follow-up детали
- Раздельный multi-currency вывод
- Прогноз по дате зарплаты (только конец календарного месяца)
- Per-user сегрегация бюджета

## 5. Ambiguities Identified (Resolved)

| # | Question | Resolution |
|---|----------|------------|
| 1 | «Свободные деньги» формула | Баланс MTD − обязательства (recurring + долги до конца месяца) |
| 2 | Горизонт расчёта | Конец календарного месяца |
| 3 | Сумма в RUB в вопросе | Авто-конверсия в THB по текущему курсу, обе суммы в ответе |
| 4 | Follow-up «детали» | Всегда развёрнутый ответ — без follow-up в MVP |

## 6. Open Issues (defer to constraints step)

| # | Issue | Deferred to |
|---|-------|-------------|
| O-1 | Отдельный skill vs новый action в `budget_skill` | constraints.md (architect decision) |
| O-2 | Формулировка manifest description чтобы не конфликтовать с `summary`/`forecast` | constraints.md |
| O-3 | Поведение при недостаточных данных (новый месяц, < 5 транзакций) | constraints.md (fallback strategy) |
| O-4 | Определение «приоритетных трат» — насколько LLM сама решает на основе данных vs мы даём explicit правила | constraints.md |
| O-5 | Точная схема `budget_debt` — какие колонки (`amount_total`, `due_date`, `is_closed`) реально есть | проверить миграцию 00007 при реализации |
