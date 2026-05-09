# Requirements: Debt Payoff Planner

## Source
Derived from `proposal.md` + clarifications collected in interview round 2 (questions on free-cashflow formula, default strategy, deficit behavior, min-payment default).

## Analysis Summary

### Resolved ambiguities
| # | Question | Resolution |
|---|----------|------------|
| A1 | Формула «свободных денег» | `forecast_income − forecast_expense − Σ min_payment по active debts`. Платежи по долгам должны быть исключены из forecast_expense чтобы не двоить учёт |
| A2 | Стратегия портфеля по умолчанию | **avalanche** (max % первым) |
| A3 | Поведение при дефиците (`free ≤ 0` или `free < Σ min`) | Вернуть структурированную ошибку «insufficient_cashflow», план не строить |
| A4 | Min-платёж по умолчанию | `Debt.MonthlyPayment` если задан; иначе `balance * rate + 1 ед валюты` (минимум, при котором долг сходится) |
| A5 | Тип `interest_rate` | Месячная ставка (decimal, например `0.024` = 2.4% в месяц), хранится `REAL` в sqlite |
| A6 | Формат ошибки `payment ≤ accrual` | Структурированный `error: "payment_below_accrual"` с указанием `min_required` |
| A7 | Округление | До целых единиц основной валюты (как существующий budget — `summary` округляет рубли до целых) |

### Implicit assumptions made explicit
- IA1. Все долги пользователя в одной валюте (= валюта основного cashflow)
- IA2. `interest_rate = 0` означает беспроцентный долг (валидный кейс — рассрочки)
- IA3. Активный долг = `status = "active"` И `total_amount − paid_amount > 0`
- IA4. Месяцы дискретны: один платёж в начале месяца, % начисляется в конце месяца на остаток
- IA5. Существующие записи Debt без `interest_rate` после миграции получают `NULL`; в расчёте трактуются как `0` пока пользователь не задаст явно

### Edge cases (must be handled)
- E1. `payment ≤ balance * rate` для одного долга → `payment_below_accrual`
- E2. `balance ≤ 0` (`paid_amount ≥ total_amount`) → долг считается закрытым, в портфельный план не попадает
- E3. Нет ни одного active debt → `debt_plan` без аргументов возвращает «нет активных долгов»
- E4. Указан `debt_id` несуществующего долга → `not_found`
- E5. `interest_rate` отрицательный или > 1 (= 100% в месяц) → валидация `invalid_rate`
- E6. План превышает разумный горизонт (>50 лет) → `payoff_too_long` (защита от deadlock-плана)
- E7. `forecast` пуст (нет данных за текущий месяц) → fallback: использовать `avg(income−expense)` за последние 3 месяца; если и этого нет — `no_cashflow_data`

---

## Functional Requirements

### FR1. Расширение схемы Debt
- FR1.1. Миграция БД: `ALTER TABLE debts ADD COLUMN interest_rate REAL`, `planned_monthly REAL`, `target_payoff_date TEXT` (ISO-8601 date)
- FR1.2. `internal/budget.Debt` обновлён соответствующими полями (Go-нативно: `*float64`, `*float64`, `*time.Time`)
- FR1.3. Существующие операции `add_debt`, `pay_debt`, `debt_status` продолжают работать без передачи новых полей

### FR2. Calc engine — план для одного долга
- FR2.1. Чистая функция `PlanSingleDebt(balance, rate, payment) (months, total_paid, overpayment, error)`
- FR2.2. Возвращает `payment_below_accrual` если `payment ≤ balance * rate` и `rate > 0`
- FR2.3. При `rate = 0` сводится к `months = ceil(balance / payment)`
- FR2.4. Защита от бесконечного цикла: лимит 600 итераций (50 лет) → `payoff_too_long`

### FR3. Calc engine — портфельный план
- FR3.1. `PlanPortfolio(debts, free, strategy)` где `strategy ∈ {"avalanche", "snowball"}`
- FR3.2. Алгоритм: каждый долг получает свой `min_payment`; остаток `free − Σ min` направляется на «приоритетный» долг (по rate desc для avalanche / по balance asc для snowball); по закрытии освобождённые деньги перетекают на следующий
- FR3.3. Возвращает помесячный массив платежей по каждому долгу + итог: `total_months`, `total_overpayment`, `per_debt_payoff_date`
- FR3.4. Если `free < Σ min` → `insufficient_cashflow` с указанием `deficit`

### FR4. Action `debt_plan`
- FR4.1. Аргументы: `debt_name?` (один долг) или без него (портфель), `min_payment?`, `max_payment?`, `strategy?` (default avalanche)
- FR4.2. Single-debt режим (есть `debt_name`): возвращает 2 сценария (min vs max). Если `min_payment` не передан — берётся по правилу A4. Если `max_payment` не передан — берётся `free_cashflow_this_month`
- FR4.3. Portfolio режим (нет `debt_name`): возвращает один сценарий по `strategy` с `free_cashflow`
- FR4.4. Источник free_cashflow: `forecast.go` за текущий месяц по формуле A1; при пусто — fallback по E7
- FR4.5. Не мутирует БД

### FR5. Action `debt_fix_plan`
- FR5.1. Аргументы: `debt_name`, `monthly` (зафиксированный платёж)
- FR5.2. Сохраняет на Debt: `planned_monthly = monthly`, `target_payoff_date = today + months_from_PlanSingleDebt`
- FR5.3. Валидация: `monthly` должен пройти FR2 без `payment_below_accrual`
- FR5.4. Может вызываться повторно — перезаписывает план

### FR6. Action `debt_progress`
- FR6.1. Аргументы: `debt_name?` (без — все active)
- FR6.2. По каждому долгу с зафиксированным планом возвращает: `current_balance`, `expected_balance_today` (по плану), `delta` (опережение/отставание), `target_payoff_date`, `projected_payoff_date` (пересчёт от сегодня по фактическому темпу из `pay_debt` истории)

### FR7. Интеграция с forecast
- FR7.1. Forecast-агрегация исключает `Transaction` с категорией `"Платёж по долгу"` (sentinel из `internal/constants`) из `forecast_expense`. Реализуется добавлением фильтра в `getMonthlyExpenses` либо новой helper-функцией.
- FR7.2. Новая публичная функция `EstimateMonthlyIncomeExpense(ctx, lookbackMonths int, rates map[string]float64) (income, expense float64, err error)` в `internal/budget` (Store-метод). Возвращает усреднённый по `lookbackMonths` месячный доход и расход (с применением исключения из FR7.1).
- FR7.3. Pure-функция `FreeCashflowThisMonth(income, expense float64, activeDebts []Debt) float64` в `internal/budget` (без БД).

### FR8. Budget skill manifest
- FR8.1. Добавлены actions `debt_plan`, `debt_fix_plan`, `debt_progress` в Manifest рядом с `debt_status`
- FR8.2. Description Manifest достаточно специфичен чтобы LLM-роутер не путал `debt_plan` с `debt_status` (см. бд simpleAI-399 — пример коллизии триггеров)
- FR8.3. Все ответы в человекочитаемом текстовом формате (как остальные budget actions)

---

## Non-Functional Requirements

- NFR1. Производительность: `PlanPortfolio` для ≤20 долгов на горизонте ≤50 лет завершается за <50ms
- NFR2. Чистые функции расчёта (FR2, FR3) — без зависимостей от БД, покрыты unit-тестами
- NFR3. Денежные значения: `float64` в коде (как в существующем budget), округление до целых единиц на стадии форматирования вывода
- NFR4. Все суммы и проценты валидируются: `≥ 0` для денег, `0 ≤ rate ≤ 1` для месячной ставки
- NFR5. Логирование ошибок плана через существующий `apperr` пакет

---

## Data Requirements

### Schema delta (sqlite)
```sql
ALTER TABLE debts ADD COLUMN interest_rate REAL;        -- monthly rate, 0..1, NULL = unknown (treated as 0)
ALTER TABLE debts ADD COLUMN planned_monthly REAL;       -- fixed plan amount
ALTER TABLE debts ADD COLUMN target_payoff_date TEXT;    -- ISO-8601 'YYYY-MM-DD'
```

### Inputs
- Existing `debts`, `transactions`, recurring records
- `forecast.go` outputs

### Outputs
- Не вводит новых таблиц
- Все артефакты плана живут на самой записи Debt

---

## Out of Scope (re-stated)
- Напоминания о платежах
- Авто-создание `transaction` при фиксации платежа
- Аннуитет (точный график тело/%)
- Мультивалютные долги
- Stateful-диалог (многошаговый «спроси-ответь» с сохранением сессии)
- Telegram inline-кнопки

---

## Open Questions Carried to Constraints
| # | Question | Owner |
|---|----------|-------|
| OQ1 | Как именно `forecast.go` помечает «платёж по долгу» чтобы исключать | **Resolved (constraints OQ1):** sentinel category `"Платёж по долгу"` в `internal/constants`. Filter в `getMonthlyExpenses`. |
| OQ2 | Где хранится текущий курс валют если в будущем будем поддерживать мультивалюту (`internal/rates`?) — фиксируем дизайн на будущее, не реализуем | architect (step 02) |
| OQ3 | Стиль форматирования вывода (markdown vs plain) — следовать конвенции существующих budget actions | architect (step 02) |
