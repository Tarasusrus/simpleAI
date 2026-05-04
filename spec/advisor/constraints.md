# Architectural Constraints: Financial Advisor Skill

## 1. Codebase facts (verified)

- Skill flow: Telegram → `agent.Service` → manifest-based router (LLM choose tool) → `plugin.Registry.Invoke(skillName, action, payload)` → конкретный skill (`internal/skills/`)
- Существующие skill'ы: `budget_skill` (multi-action), `rag_search`
- Tables (migrations 00007–00011):
  - `budget_transaction` — `id, type, amount, currency, category_id, description, transaction_date, created_at` — **БЕЗ `chat_id`** — глобальный бюджет
  - `budget_category` — справочник
  - `budget_goal` — цели
  - `budget_debt` — `id, name, total_amount, paid_amount, monthly_payment, direction (owe/owed), counterparty, status (active/paid), due_date, ...` — **БЕЗ `chat_id`**, поле статуса = `status`, не `is_closed`
  - `budget_recurring` — **ЕСТЬ `chat_id`**, поле даты = `next_date` (не `next_run_at`), поле включения = `enabled`
  - `budget_reminder` — `chat_id`-aware
- Курс: таблица `exchange_rate (currency, rate_to_rub, updated_at)`. Конверсия идёт через RUB как pivot. Метод: `Store.GetExchangeRates(ctx) -> map[string]float64`
- Forecast: `Store.GetForecastData(ctx, months int) -> []CategoryForecast` — переиспользовать
- Manifest: `plugin.Manifest{Name, Description, Schema}` — JSON-schema для action+payload, как в `budget_skill.go:30`
- Логирование: `slog` через стандартный logger, поля как `skill`, `action`, `chat_id`, `duration_ms`
- ChatID извлекается из `ctx` через `agent.ChatIDKey{}`

## 2. DB Schema

### MUST NOT
- **MUST NOT** добавлять новые таблицы или колонки в этой итерации
- **MUST NOT** менять существующие миграции
- **MUST NOT** добавлять `chat_id` в `budget_transaction` или `budget_debt` — это вне scope

### MUST
- **MUST** работать с существующей асимметрией: транзакции и долги — глобальные, recurring/reminder — per-chat
- **MUST** в snapshot SQL фильтровать `budget_recurring` по `chat_id = $1`, `enabled = true`
- **MUST** для `budget_transaction` и `budget_debt` — читать глобально (нет `chat_id`); пометить это явным комментом в SQL чтобы не путало читателей
- **MUST** snapshot собирать одним SQL CTE для transactions + debt + recurring; forecast получать **отдельным последующим вызовом** `Store.GetForecastData(ctx, 0)` (не внутри CTE — проще, переиспользует существующий проверенный код)
- **MUST** в snapshot SQL для `budget_debt` использовать `status = 'active'` и `direction = 'owe'` (мы должны), **не `is_closed`**
- **MUST** учитывать долг в `active_debt_due_mtd` если `due_date` IS NOT NULL и `due_date <= last_day_of_month`
- **MUST** конвертировать суммы через `rate_to_rub` затем в THB (двойной шаг: amount → RUB → THB)

## 3. Component Design

### MUST
- **MUST** реализовать **новый skill** `internal/skills/advisor_skill.go` (отдельный файл, отдельный struct `AdvisorSkill`), а не действие внутри `budget_skill.go`
  - Reason: `budget_skill` уже > 1100 строк, расширение усложнит поддержку. Advisor — другой профиль (single action, LLM-heavy), отдельный skill чище
- **MUST** реализовать `AdvisorSkill` с методами `Manifest() plugin.Manifest`, `Run(ctx, payload) (string, error)`, регистрацией через `plugin.Registry.Register` в `cmd/app/main.go`
- **MUST** инжектировать зависимости через конструктор: `NewAdvisorSkill(store *budget.Store, llm llm.Client, logger *slog.Logger) *AdvisorSkill`
- **MUST** добавить метод `Store.GetAdvisorSnapshot(ctx, chatID int64, today time.Time) (*AdvisorSnapshot, error)` в `internal/budget/store.go` (один SQL CTE)
- **MUST** определить тип `AdvisorSnapshot` в `internal/budget/model.go` рядом с `CategoryForecast`
- **MUST** манифест exposes одно action `advice` с payload `{question: string, amount?: number, currency?: string}`

### SHOULD
- **SHOULD** переиспользовать `Store.GetForecastData` для forecast_remaining_thb внутри `GetAdvisorSnapshot` или вызывать его параллельно из skill
- **SHOULD** хранить prompt template в константе/файле в том же пакете `skills`, формат — единый текстовый prompt с placeholder'ами

### MUST NOT
- **MUST NOT** вызывать другие skill'ы из advisor (никакого agent loop)
- **MUST NOT** хранить state между вызовами (никаких полей-кэшей в struct)
- **MUST NOT** добавлять новые внешние зависимости (libs)

## 4. Manifest description

### MUST
- **MUST** Manifest description формулировать так чтобы LLM-роутер однозначно выбирал advisor для вопросов «можем ли купить», «что приоритетнее», «хватит ли денег», «стоит ли тратить»
- **MUST** включить в description явный негатив: «Do NOT use for recording transactions, listing transactions, getting summaries — use budget skill for those.»
- **MUST** в description явно перечислить триггер-паттерны: «Use when user asks for advice about a potential purchase, prioritization between expenses, affordability check.»

### SHOULD
- **SHOULD** advisor manifest name = `"advisor"`, action name = `"advice"` (single action в этом skill)

## 5. API / Output format

### MUST
- **MUST** Skill возвращает строку для Telegram (Markdown / plain) — НЕ JSON наружу
- **MUST** LLM внутри skill вызывается с структурированным промптом и должна возвращать JSON со схемой `{verdict: "Да"|"Нет"|"Условно", numbers: {free_cash_thb, forecast_remaining_thb, obligations_thb}, explanation: string, recommendation?: string}`
- **MUST** Skill парсит JSON, форматирует в человекочитаемый русский Markdown и возвращает строку

### MUST NOT
- **MUST NOT** возвращать сырой JSON пользователю
- **MUST NOT** включать в ответ суммы в RUB как первичные — только если в вопросе была non-THB сумма, тогда показать обе

## 6. Currency

### MUST
- **MUST** все цифры в snapshot и ответе хранить и отдавать в THB
- **MUST** если в вопросе сумма не в THB — конвертировать через `exchange_rate` (валюта → RUB → THB) и в ответе показывать обе суммы
- **MUST** если для **THB** курса нет — вернуть friendly-ошибку «не могу посчитать в THB — обнови курс», skill завершается без ответа LLM
- **MUST** если для **отдельной валюты** транзакции/recurring нет курса — пропустить запись, залогировать `slog.Warn` с currency, продолжить snapshot
- **MUST** округление сумм в THB на финальном шаге форматирования ответа (до 0 или 2 знаков), промежуточные расчёты в `float64`

### SHOULD
- **SHOULD** распознавание `amount` + `currency` из вопроса делегировать LLM (она сама выделяет в payload)

## 7. Error handling

### MUST
- **MUST** SQL-ошибка → friendly строка пользователю + `slog.Error` с error и chatID
- **MUST** LLM-ошибка / невалидный JSON → friendly строка + `slog.Error`
- **MUST** недостаточно данных (< `MinTxForConfidence` транзакций в текущем месяце; константа = 5 в коде skill'а) → не падать, передать флаг в prompt («low_data: true»), LLM должна вернуть `verdict: "Условно"` с пояснением

## 8. Logging

### MUST
- **MUST** на каждый вызов лог: `skill="advisor"`, `chat_id`, `question_len`, `verdict` (если успех), `free_cash_thb`, `duration_ms`, `error` (если ошибка)
- **MUST** не логировать полный текст вопроса — только длину или первые 200 символов (privacy)

## 9. Testing strategy

### MUST
- **MUST** unit-тест для `GetAdvisorSnapshot` — testcontainers Postgres с фикстурами (паттерн как для существующих store-тестов; если их нет — добавить, но не за рамки этой задачи)
- **MUST** unit-тест на формирование promt (snapshot → string) — table-driven
- **MUST** unit-тест на парсинг LLM-ответа (валидный JSON, невалидный JSON, missing fields)
- **MUST** покрыть edge cases: пустой recurring, пустые debt, отсутствие курса, low_data

### SHOULD
- **SHOULD** smoke-тест на routing — мок LLM, проверить manifest matching на типичных фразах («можем купить велосипед», «купил молоко за 50»)

### MUST NOT
- **MUST NOT** делать тест с реальным LLM (DeepSeek/Gemini) в CI — мок

## 10. Code style

### MUST
- **MUST** следовать существующему паттерну skill'а: метод `Manifest()`, `Run(ctx, payload)`, конструктор `New<Name>Skill`
- **MUST** именование: type `AdvisorSkill`, файл `advisor_skill.go`, prompt файл `advisor_prompt.go` или константа в том же файле
- **MUST** все суммы — `float64` (как в проекте), форматирование на выводе

### MUST NOT
- **MUST NOT** добавлять comments to obvious code (по project policy)
- **MUST NOT** копировать SQL из `budget_skill` — если нужна общая часть, переиспользовать через store

## 11. Performance

### MUST
- **MUST** snapshot — ровно один round-trip к БД (CTE)
- **MUST** общий путь advisor.Run ≤ 4000 ms p95 на типичных данных (≤ 200 трнз/месяц)
- **MUST NOT** делать N+1

## 12. Dependencies on other features

- Зависит от существующих: `budget.Store`, `agent.Service`, `plugin.Registry`, LLM client, `exchange_rate` table
- Не блокирует и не блокируется issue #43 (RAG advisor) — это будущая итерация
- НЕ зависит от python-migration (epic simpleAI-0ma) — реализуется в Go, останется работать после миграции с минимальными изменениями (только Telegram-слой)

## 13. Open issues (resolved here)

| Open issue from requirements | Resolution |
|-------|------------|
| O-1 (отдельный skill vs новый action) | Отдельный `AdvisorSkill` (см. §3) |
| O-2 (формулировка manifest) | Явный negative + trigger patterns в description (§4) |
| O-3 (мало данных) | low_data flag в prompt → verdict «Условно» (§7) |
| O-4 (priority logic) | Решает LLM на основе snapshot — никаких explicit правил приоритезации в коде |
| O-5 (схема `budget_debt`) | Подтверждено: `status='active'`, `direction='owe'`, `due_date` (см. §1) |
