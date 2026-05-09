# Verification: Debt Payoff Planner

Reviewed: `proposal.md`, `requirements.md`, `acceptance_criteria.md`, `constraints.md`.

Severity: **BLOCKER** halts implementation; **MAJOR** must fix before merge; **MINOR** can be filed as follow-up bd.

---

## Completeness

### MAJOR-1 — `forecast_expense` exclusion path is not closed
- **Where:** AC-G2 says debt-payment expenses must be excluded from `forecast_expense`. Constraints D5 + OQ1 acknowledge that `pay_debt` does not currently create a `Transaction`, so the exclusion is a no-op today.
- **Risk:** A user who manually adds an "expense" labelled as a debt payment via existing `add_expense` could end up with a value double-subtracted (once via `forecast_expense`, once via `Σ min_payments`).
- **Resolution:** Constraints OQ1 already mandates the sentinel category `"Платёж по долгу"`. Add explicit acceptance criterion: AC-G2 covers the case but does not specify *what makes* a row a debt payment. Update AC-G2 to reference the sentinel category. Add unit test that constructs a `Transaction` with that category and asserts exclusion. Filed below as required edit.

### MINOR-2 — `debt_progress` history source not specified
- **Where:** AC-F2/F3 require `projected_payoff_date` from "actual pace from `pay_debt` history". `Debt` only stores `paid_amount` (cumulative). No per-payment history table.
- **Risk:** Pace can only be computed if we know *when* payments happened. With cumulative-only state, "ahead/behind" must reduce to: comparing current `total - paid_amount` against expected balance assuming `planned_monthly` since `target_payoff_date − months`.
- **Resolution:** Soften AC-F2/F3 to compare current balance against expected balance derived from plan params alone (no per-payment history needed). Mark "real history-based projection" as a follow-up requiring a payments table — out of scope. Documented edit below.

### MINOR-3 — Locale of `now()` for `target_payoff_date`
- **Where:** AC-E1 says `today + months`. Not specified: server timezone? UTC?
- **Risk:** Off-by-one-day around midnight.
- **Resolution:** Pin to UTC date in constraints (CMP block). Add to constraints: "`target_payoff_date` **MUST** use `time.Now().UTC()` truncated to date."

## Consistency

### MAJOR-4 — `interest_rate` NULL semantics
- **Where:** DSC-6 says NULL → treat as 0. AC-H1 rejects `interest_rate < 0 || > 1` on `add_debt`. AC-D6 expects `payment ≤ balance*rate`. Edge case: legacy debt with NULL rate + tiny payment → rate=0 → no `payment_below_accrual`, plan converges. Consistent.
- **But** AC-D2 default min for legacy debt without rate: `floor(balance*0)+1 = 1`. That's a $1 monthly minimum which would take centuries.
- **Resolution:** When `rate` is NULL/0, default min **MUST** instead be `Debt.MonthlyPayment` if set, else `max(balance / 60, 1)` (close in 5 years). Patch constraints D8 + AC-D2 wording.

### MINOR-5 — `MonthlyPayment` semantics drift
- **Where:** Existing `Debt.MonthlyPayment` is documented as *current monthly payment* (model.go). New `planned_monthly` is *the fixed plan*. After `debt_fix_plan`, both fields exist with potentially different meanings.
- **Risk:** Future contributor confused which one to use.
- **Resolution:** Update `model.go` comment to clarify: `MonthlyPayment` = "minimum mandatory payment from the lender"; `PlannedMonthly` = "user's chosen payoff plan amount". Add to constraints under STY block.

### OK — DB schema covers all behaviour
- Plan storage = 3 columns + existing `total_amount`/`paid_amount`. AC verified.

### OK — No contradictions between portfolio strategy in AC-C3/C4 and constraints CMP-2.

## Implementability

### MAJOR-6 — `FreeCashflowThisMonth` API mismatch with existing forecast
- **Where:** CMP-4 declares `FreeCashflowThisMonth(forecastIncome, forecastExpense float64, activeDebts []Debt) float64`. CMP-6 introduces `GetForecastIncomeExpense(months, rates) (income, expense, err)`. But existing `Store.GetForecastData` returns `[]CategoryForecast` aggregated *over N months* and *converted to THB*.
- **Risk:** "forecast for the current month" doesn't exist as an API; the existing function averages over `months`. Naming and semantics drift.
- **Resolution:** Rename the helper to `EstimateMonthlyIncomeExpense(ctx, lookbackMonths, rates)` returning per-month *averages*. Update FR7, AC-G1 wording to use "estimated monthly income/expense (average over lookback)". This matches what `forecast.go` already does and avoids inventing a "current month" forecast that doesn't exist. Patch requirements + constraints.

### MINOR-7 — Currency on `Debt`
- **Where:** Constraints CUR-1 references "currency of active debts" but `internal/budget.Debt` has no `Currency` field (model.go shows: ID, Name, TotalAmount, PaidAmount, MonthlyPayment, Direction, Counterparty, Status, DueDate). Currency exists only on `Transaction`.
- **Risk:** Multi-currency assertion CUR-1 is unverifiable today.
- **Resolution:** Two options: (a) add `Currency` column to `debts` in same migration with default to user's primary currency; (b) declare in constraints that *all debts are assumed to be in the system's primary currency* and skip CUR-1's check. Recommend (a) — small, additive, future-proof. Update DSC-2: also `ALTER TABLE debts ADD COLUMN currency TEXT NOT NULL DEFAULT 'RUB'`. Update model + store scan sites.

### OK — No circular deps. `payoff.go` is leaf.
### OK — External deps: only stdlib + uuid (already present).

## Testability

### MINOR-8 — AC-I1..I4 LLM routing tests
- **Where:** RTG-3 says "via mocked LLM router OR via static manifest match (whichever pattern already exists)". Pattern not yet identified.
- **Risk:** Implementer could skip these tests claiming "no pattern exists".
- **Resolution:** Before coding, scan repo for existing routing tests (`grep -r "Manifest" internal/skills/*_test.go`); if none, drop AC-I1..I4 from the must-pass list and put them in a follow-up bd issue (manual eval until proper test infra). Document the decision in `verification.md` (this file is the source of truth for the change).

### OK — Each functional AC maps to a test in CMP/TST blocks.

---

## Required edits (apply to upstream docs before Step 04)

1. **`acceptance_criteria.md`:**
   - AC-D2: "default min = `Debt.MonthlyPayment` if set; else if `interest_rate > 0` use `floor(balance*rate)+1`; else use `max(balance/60, 1)`."
   - AC-F2/F3: drop "from `pay_debt` history"; replace with "expected balance derived from `planned_monthly` and elapsed months since plan was fixed".
   - AC-G2: replace "labelled as a debt payment" with "having category `"Платёж по долгу"`".

2. **`requirements.md`:**
   - FR7.1: rename helper `EstimateMonthlyIncomeExpense(lookbackMonths, rates)`; clarify expense aggregation excludes the sentinel category.
   - Open question OQ1 → resolved (sentinel category).

3. **`constraints.md`:**
   - DSC-2: add `currency TEXT NOT NULL DEFAULT 'RUB'` ALTER (or document chosen primary currency).
   - CMP-4 / CMP-6: rename per MAJOR-6.
   - CMP block: pin `target_payoff_date` to `time.Now().UTC()`.
   - STY block: add comment-clarification rule for `MonthlyPayment` vs `PlannedMonthly`.
   - D8: update default-min formula for `rate=0` case.

---

## Decision

**Status: GO with required edits above.**

No blockers. Two MAJOR items (MAJOR-1, MAJOR-4, MAJOR-6) are documentation-only edits, not redesign. After applying the edits, Step 04 (`task-list`) can run.

If any required edit cannot be applied as-is and triggers a real design change, re-run Step 03.
