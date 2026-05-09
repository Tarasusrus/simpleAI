# Acceptance Criteria: Debt Payoff Planner

Format: WHEN (precondition/trigger) — THEN (action/input) — SHALL (observable outcome).
Each criterion independently testable. Behaviour-only, no implementation.

---

## Group A — Schema & migration

- AC-A1. **WHEN** the migration runs on a database with existing `debts` rows **THEN** the migration completes successfully **SHALL** without data loss and existing rows have `interest_rate = NULL`, `planned_monthly = NULL`, `target_payoff_date = NULL`.
- AC-A2. **WHEN** existing actions `add_debt` / `pay_debt` / `debt_status` are called without the new fields **THEN** the call succeeds **SHALL** with identical observable behaviour to pre-migration.
- AC-A3. **WHEN** `add_debt` is called with `interest_rate=0.024` **THEN** the new debt is persisted **SHALL** with `interest_rate=0.024` and retrievable via `debt_status`.

## Group B — Single-debt plan calculation

- AC-B1. **WHEN** balance=100000, monthly_rate=0.02, payment=10000 **THEN** PlanSingleDebt is invoked **SHALL** return months=12, overpayment≈12300 (within ±1 unit rounding tolerance), no error.
- AC-B2. **WHEN** balance=100000, monthly_rate=0.02, payment=2000 (i.e. payment ≤ 100000*0.02=2000) **THEN** PlanSingleDebt is invoked **SHALL** return error `payment_below_accrual` with `min_required = 2001`.
- AC-B3. **WHEN** balance=60000, monthly_rate=0, payment=10000 **THEN** PlanSingleDebt is invoked **SHALL** return months=6, overpayment=0, no error.
- AC-B4. **WHEN** balance>0, rate is set so payoff would exceed 50 years **THEN** PlanSingleDebt is invoked **SHALL** return error `payoff_too_long` instead of looping.

## Group C — Portfolio plan calculation

- AC-C1. **WHEN** active debts list is empty **THEN** PlanPortfolio is invoked **SHALL** return an empty plan with explicit `no_active_debts` marker (not an error).
- AC-C2. **WHEN** Σ min_payments > free_cashflow **THEN** PlanPortfolio is invoked **SHALL** return error `insufficient_cashflow` including numeric `deficit` field.
- AC-C3. **WHEN** strategy="avalanche" with debts {A: 100k @ 24%, B: 50k @ 12%} and surplus after min payments **THEN** PlanPortfolio is invoked **SHALL** direct the entire surplus to debt A until A is closed, then redirect freed cash (A's prior min + surplus) to B.
- AC-C4. **WHEN** strategy="snowball" with the same inputs as AC-C3 **THEN** PlanPortfolio is invoked **SHALL** direct surplus to debt B (smaller balance) first.
- AC-C5. **WHEN** all input is valid and plan completes **THEN** PlanPortfolio **SHALL** return per-debt `payoff_date`, total months, total overpayment, and a per-month payment matrix.

## Group D — Action `debt_plan`

- AC-D1. **WHEN** user invokes `debt_plan` with `debt_name="Tinkoff"`, `min_payment=10000`, `max_payment=30000` **THEN** the bot **SHALL** respond with two scenarios (min and max), each containing months-to-payoff, overpayment, and total paid.
- AC-D2. **WHEN** user invokes `debt_plan` with `debt_name="Tinkoff"` and no `min_payment` **THEN** the bot **SHALL** default min to: `Debt.MonthlyPayment` if set; else if `interest_rate > 0` then `floor(balance*rate) + 1`; else `max(balance/60, 1)` (close in 5 years for zero-rate). The chosen value is included in the response.
- AC-D3. **WHEN** user invokes `debt_plan` with `debt_name="Tinkoff"` and no `max_payment` **THEN** the bot **SHALL** default max to `FreeCashflowThisMonth` and include that value plus its formula source in the response.
- AC-D4. **WHEN** user invokes `debt_plan` without `debt_name` and there are 2+ active debts **THEN** the bot **SHALL** respond with a portfolio plan using the default strategy `avalanche` and free cashflow from forecast.
- AC-D5. **WHEN** user invokes `debt_plan` with `strategy="snowball"` (no `debt_name`) **THEN** the bot **SHALL** order debts by ascending balance for surplus allocation.
- AC-D6. **WHEN** the user-provided `min_payment ≤ balance*rate` **THEN** the bot **SHALL** return a human-readable error referencing `payment_below_accrual` and the minimum required payment.
- AC-D7. **WHEN** `debt_plan` runs **THEN** no rows in `debts` table **SHALL** be modified.
- AC-D8. **WHEN** `forecast` has no data for the current month and last 3 months also empty **THEN** the bot **SHALL** return `no_cashflow_data` and refuse to auto-suggest max.

## Group E — Action `debt_fix_plan`

- AC-E1. **WHEN** user invokes `debt_fix_plan` with `debt_name="Tinkoff"` and `monthly=30000` (valid) **THEN** the corresponding `debts` row **SHALL** be updated with `planned_monthly=30000` and `target_payoff_date` equal to today + months derived from PlanSingleDebt.
- AC-E2. **WHEN** user invokes `debt_fix_plan` with `monthly` that fails PlanSingleDebt validation (`payment_below_accrual` or `payoff_too_long`) **THEN** the bot **SHALL** return the corresponding error and **SHALL NOT** modify the row.
- AC-E3. **WHEN** `debt_fix_plan` is invoked twice for the same debt with different `monthly` **THEN** the second call **SHALL** overwrite the first plan (last-write-wins).
- AC-E4. **WHEN** `debt_name` does not match any debt **THEN** the bot **SHALL** return error `not_found`.

## Group F — Action `debt_progress`

- AC-F1. **WHEN** a debt has `planned_monthly` and `target_payoff_date` set, and current balance equals expected-by-plan balance **THEN** `debt_progress` **SHALL** report `delta=0` ("on track").
- AC-F2. **WHEN** current balance < expected-by-plan balance (where expected balance is derived purely from `planned_monthly`, `interest_rate` and elapsed months since plan creation — no per-payment history) **THEN** `debt_progress` **SHALL** report a positive `delta` ("ahead of plan") and a `projected_payoff_date` earlier than `target_payoff_date`.
- AC-F3. **WHEN** current balance > expected-by-plan balance (computed as in AC-F2) **THEN** `debt_progress` **SHALL** report a negative `delta` ("behind plan") and a `projected_payoff_date` later than `target_payoff_date`.
- AC-F4. **WHEN** a debt has no `planned_monthly` set **THEN** `debt_progress` for that debt **SHALL** return marker `no_plan_set` instead of progress numbers.
- AC-F5. **WHEN** invoked without arguments **THEN** `debt_progress` **SHALL** return entries for every active debt.

## Group G — Free cashflow integration

- AC-G1. **WHEN** the user has `forecast_income=200000`, `forecast_expense=120000` (excluding debt payments), and Σ min payments by active debts = 15000 **THEN** `FreeCashflowThisMonth` **SHALL** return 65000.
- AC-G2. **WHEN** any `Transaction` row has category name equal to the sentinel `"Платёж по долгу"` **THEN** that row **SHALL** be excluded from the expense aggregation that feeds `FreeCashflowThisMonth` (no double counting against the explicit Σ min).
- AC-G3. **WHEN** `forecast_income − forecast_expense ≤ 0` **THEN** `FreeCashflowThisMonth` **SHALL** return 0 and `debt_plan` **SHALL** treat that as `insufficient_cashflow`.

## Group H — Validation & errors

- AC-H1. **WHEN** `add_debt` is called with `interest_rate < 0` or `> 1` **THEN** the bot **SHALL** return error `invalid_rate` and **SHALL NOT** persist the row.
- AC-H2. **WHEN** any monetary input is negative **THEN** the bot **SHALL** return a validation error and **SHALL NOT** mutate state.
- AC-H3. **WHEN** any error from groups B/C/D/E surfaces to the user **THEN** the response **SHALL** be human-readable Russian text with the underlying error tag visible (e.g. "(payment_below_accrual)") for debuggability.

## Group I — LLM routing

- AC-I1. **WHEN** the user types "покажи план по моему долгу Тинькофф" **THEN** the LLM router **SHALL** select action `debt_plan` (not `debt_status`).
- AC-I2. **WHEN** the user types "сколько я уже выплатил по Тинькофф" **THEN** the LLM router **SHALL** select action `debt_status` (not `debt_plan`).
- AC-I3. **WHEN** the user types "зафиксируй план 30000 на Тинькофф" **THEN** the LLM router **SHALL** select action `debt_fix_plan` with `monthly=30000`.
- AC-I4. **WHEN** the user types "опережаю ли я план по долгам" **THEN** the LLM router **SHALL** select action `debt_progress`.

## Group J — Idempotency & safety

- AC-J1. **WHEN** any plan/progress action is invoked **THEN** repeated identical calls within the same minute **SHALL** return identical results (no nondeterministic ordering of debts in portfolio output).
- AC-J2. **WHEN** the test suite runs `internal/budget/...` and `internal/skills/...` **THEN** all unit tests **SHALL** pass on a clean DB.
