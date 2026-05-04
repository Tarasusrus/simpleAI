# Acceptance Criteria: Financial Advisor Skill

WHEN-THEN-SHALL format. Each criterion testable independently.

---

## A. Routing

### AC-A1 — Auto-routing срабатывает на финансовый вопрос
- **WHEN** пользователь отправляет сообщение типа «можем купить велосипед за 25000?»
- **THEN** `agent.Service` обращается к LLM-роутеру с manifest всех skill'ов
- **SHALL** LLM выбрать advisor skill, а не `budget.summary` / `budget.forecast`

### AC-A2 — Routing не срабатывает на запрос ввода транзакции
- **WHEN** пользователь пишет «купил молоко за 50»
- **THEN** LLM-роутер делает выбор
- **SHALL** выбрать `budget.add_expense`, не advisor

### AC-A3 — Manifest exposes single tool
- **WHEN** registry собирает manifest всех skill'ов
- **THEN** в списке инструментов
- **SHALL** присутствовать advisor с непустым `description`, который явно указывает: советы по покупкам / приоритезация / "могу ли я позволить"

---

## B. Финансовый снэпшот

### AC-B1 — Один SQL-запрос
- **WHEN** advisor.Run вызывается с любым вопросом
- **THEN** skill обращается к БД
- **SHALL** выполнить ровно один SQL-запрос (CTE) для сбора всех данных снэпшота — проверяется через mock/sql-spy

### AC-B2 — Снэпшот содержит обязательные поля
- **WHEN** snapshot построен
- **THEN** структура содержит:
- **SHALL** `balance_mtd_thb`, `spent_by_category_thb` (map), `forecast_remaining_thb`, `upcoming_recurring_mtd_thb`, `active_debt_due_mtd_thb`, `free_cash_thb` (где `free_cash = balance − recurring − debt`)

### AC-B3 — Все суммы в THB
- **WHEN** транзакция/recurring/debt записаны в RUB или USD
- **THEN** при построении снэпшота
- **SHALL** конвертировать в THB через текущий `exchange_rate` и не показывать исходную валюту в полях снэпшота

### AC-B4 — Горизонт = конец месяца
- **WHEN** снэпшот собирается 15 марта
- **THEN** `upcoming_recurring_mtd` фильтрует
- **SHALL** включать только записи с `next_run_at <= '2026-03-31 23:59:59'`

### AC-B5 — Долги фильтруются по due_date в текущем месяце
- **WHEN** в БД есть открытый долг с `due_date = 2026-04-15` (следующий месяц)
- **THEN** при сборе снэпшота 2026-03-15
- **SHALL** этот долг не входить в `active_debt_due_mtd_thb`

### AC-B6 — Закрытые долги не учитываются
- **WHEN** долг с `is_closed = true`
- **THEN** при сборе снэпшота
- **SHALL** не учитываться в `active_debt_due_mtd_thb`

---

## C. Конверсия валют в вопросе

### AC-C1 — RUB сумма конвертируется
- **WHEN** пользователь спрашивает «можем купить за 50000 руб?»
- **THEN** advisor парсит amount + currency и
- **SHALL** ответ содержит и RUB-сумму и THB-эквивалент по текущему курсу

### AC-C2 — THB сумма не конвертируется
- **WHEN** пользователь спрашивает «можем купить за 25000 THB?»
- **THEN** advisor определяет валюту THB
- **SHALL** в ответе показывать только THB

### AC-C3 — Валюта не указана — считаем THB
- **WHEN** пользователь спрашивает «можем купить за 5000?»
- **THEN** advisor по умолчанию интерпретирует
- **SHALL** как THB и не пытаться конвертировать

---

## D. Формат ответа

### AC-D1 — Развёрнутый ответ всегда
- **WHEN** любой запрос приходит в advisor
- **THEN** ответ возвращается за один вызов
- **SHALL** содержать четыре секции: вердикт, ключевые цифры, объяснение, (опц.) рекомендация

### AC-D2 — Вердикт = одно из трёх значений
- **WHEN** LLM формирует ответ
- **THEN** поле verdict
- **SHALL** содержать одно из: `Да` / `Нет` / `Условно`

### AC-D3 — Ключевые цифры — три штуки
- **WHEN** в ответе раздел «ключевые цифры»
- **THEN** этот раздел
- **SHALL** содержать ровно три значения: `free_cash_thb`, `forecast_remaining_thb`, `obligations_thb` — все в THB

### AC-D4 — Краткое объяснение
- **WHEN** ответ возвращается
- **THEN** объяснение
- **SHALL** быть текстом 2–3 предложений, описывающим причину вердикта

### AC-D5 — Рекомендация при «Условно»/«Нет»
- **WHEN** verdict не равен `Да`
- **THEN** ответ
- **SHALL** содержать секцию «рекомендация» с альтернативой или предупреждением

---

## E. Edge cases

### AC-E1 — Недостаточно данных
- **WHEN** в БД < 5 транзакций за текущий месяц
- **THEN** advisor
- **SHALL** вернуть ответ с verdict = `Условно` и явно указать в объяснении «недостаточно данных для уверенного прогноза»

### AC-E2 — Нет повторяющихся / нет долгов
- **WHEN** `budget_recurring` или `budget_debt` пустые для пользователя
- **THEN** snapshot
- **SHALL** установить соответствующие поля в 0, а не падать с ошибкой

### AC-E3 — Курс отсутствует
- **WHEN** в `exchange_rate` нет записи для нужной валюты
- **THEN** advisor
- **SHALL** вернуть пользователю friendly-ошибку «не могу посчитать в THB — обнови курс» и не падать

### AC-E4 — LLM вернула невалидный JSON
- **WHEN** LLM-ответ не парсится в ожидаемую структуру
- **THEN** advisor
- **SHALL** вернуть friendly-ошибку и залогировать `slog.Error` с полным ответом LLM

### AC-E5 — SQL ошибка
- **WHEN** SQL снэпшота падает
- **THEN** advisor
- **SHALL** вернуть friendly-ошибку «временная ошибка, попробуй позже» и залогировать `slog.Error`

---

## F. Multi-user / chatID

### AC-F1 — Snapshot фильтруется по chatID только где применимо
- **WHEN** advisor.Run вызывается из чата с `chatID = 100`
- **THEN** SQL снэпшота
- **SHALL** фильтровать `budget_recurring` по `chat_id = 100`; для таблиц без `chat_id` (`budget_transaction`, `budget_debt`) читать глобально (как `budget_skill`)

### AC-F2 — Общий бюджет на инстанс
- **WHEN** taras и жена пишут из одного чата
- **THEN** оба получат один и тот же снэпшот
- **SHALL** не существовать per-user сегрегации; бюджет общий на весь инстанс бота

---

## G. Logging

### AC-G1 — slog запись
- **WHEN** advisor завершает работу (успех или ошибка)
- **THEN** в логах
- **SHALL** появиться запись с полями: `skill="advisor"`, `chat_id`, `question` (truncated 200), `verdict` (если успех), `free_cash_thb`, `duration_ms`

---

## H. Stateless

### AC-H1 — Никакой conversation history
- **WHEN** advisor вызван дважды подряд с разными вопросами
- **THEN** второй вызов
- **SHALL** не получать данные первого вопроса/ответа в input — только текущее сообщение и снэпшот

---

## I. Performance

### AC-I1 — Latency
- **WHEN** advisor.Run выполняется на типичных данных (≤ 200 транзакций / месяц)
- **THEN** total duration
- **SHALL** не превышать 4000 ms (p95) в продакшен-окружении
