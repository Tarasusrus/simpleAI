# Technical Constraints: Debt Payoff Planner

## Project Context (real)
- Go 1.24+, sqlite via `internal/db/migrations/*.sql` (numbered, sequential)
- Single-user telegram bot; LLM-routed budget skill in `internal/skills/budget_skill.go`
- Budget core in `internal/budget/`: `model.go` (entities), `store.go` (DB CRUD + forecast aggregation), `store_test.go`, `forecast_test.go`
- Existing forecast: `Store.GetForecastData(months, rates) → []CategoryForecast` aggregates expenses, **converts everything to THB** via supplied rates, returns sorted descending
- Existing debt actions in skill: `add_debt`, `pay_debt`, `debt_status`
- ADR directory: `docs/adr/`. Architecture diagram: `docs/architecture.puml`. DB schema doc: `docs/db_schema.md`

---

## DB Schema

- DSC-1. **MUST** add migration `internal/db/migrations/00014_debt_payoff_planner.sql` (next free number; verify by `ls` before writing).
- DSC-2. Migration **MUST** be additive only:
  ```sql
  ALTER TABLE debts ADD COLUMN interest_rate REAL;          -- monthly rate, 0..1
  ALTER TABLE debts ADD COLUMN planned_monthly REAL;        -- user-fixed plan amount
  ALTER TABLE debts ADD COLUMN target_payoff_date TEXT;     -- ISO-8601 'YYYY-MM-DD'
  ALTER TABLE debts ADD COLUMN currency TEXT NOT NULL DEFAULT 'RUB';
  ```
  The `currency` column closes the gap that `Debt` had no currency field while `Transaction` did. Default `'RUB'` matches the project's primary cashflow currency; user can override at `add_debt` time.
- DSC-3. **MUST NOT** add a `down` migration that drops columns (sqlite ALTER DROP is unsafe; project convention is forward-only).
- DSC-4. **MUST** update `docs/db_schema.md` and `docs/db_schema.sql` to reflect the new columns.
- DSC-5. **MUST NOT** create new tables. Plan and progress are computed in-memory; only `debts` row is persisted state.
- DSC-6. Existing rows after migration **MUST** read back with `interest_rate=NULL`, `planned_monthly=NULL`, `target_payoff_date=NULL`. Code **MUST** treat NULL `interest_rate` as `0` for calculation.

## Component Design

- CMP-1. Pure calculation **MUST** live in a new file `internal/budget/payoff.go`. Test file `internal/budget/payoff_test.go`. **MUST NOT** import `database/sql` or take a `*Store` — only primitive structs.
- CMP-2. Public functions in `payoff.go`:
  - `PlanSingleDebt(balance, monthlyRate, payment float64) (PlanSingleResult, error)`
  - `PlanPortfolio(debts []PortfolioDebt, freeCashflow float64, strategy Strategy) (PortfolioResult, error)`
  - `Strategy` is a typed string with constants `StrategyAvalanche`, `StrategySnowball`.
- CMP-3. Errors **MUST** be sentinel values exported from `payoff.go`: `ErrPaymentBelowAccrual`, `ErrPayoffTooLong`, `ErrInsufficientCashflow`, `ErrInvalidRate`. Wrap with `fmt.Errorf("...: %w", err)` at call sites; never bare strings.
- CMP-4. `internal/budget/forecast_free.go` (new) **MUST** export `FreeCashflowThisMonth(income, expense float64, activeDebts []Debt) float64`. Pure function, no DB. Inputs are *averaged monthly* income/expense from CMP-6.
- CMP-5. `Store` (in `store.go`) **MUST** gain:
  - `UpdateDebtPlan(ctx, id uuid.UUID, plannedMonthly float64, targetDate time.Time) error`
  - `UpdateDebtRate(ctx, id uuid.UUID, rate float64) error` (used by `add_debt` extension and future edits)
  - Update `AddDebt` to persist `interest_rate` if set; update row scanning everywhere `Debt` is materialised (search for `&d.MonthlyPayment` / `rows.Scan`).
- CMP-6. **MUST NOT** modify `GetForecastData` signature. Add a new method `Store.EstimateMonthlyIncomeExpense(ctx, lookbackMonths int, rates map[string]float64) (income, expense float64, err error)` returning per-month *averages* over the lookback window. Expense aggregation **MUST** exclude rows whose category equals the sentinel `"Платёж по долгу"`. Rationale: keep blast radius small; semantics match what `forecast_test.go` already proves (per-month averaging).
- CMP-8. `target_payoff_date` **MUST** be set from `time.Now().UTC()` truncated to date (`YYYY-MM-DD` zero-time). Same anchor used everywhere `today` is referenced in this feature.
- CMP-9. `Debt` **MUST** gain a `Currency string` field; `model.go` and every `rows.Scan` site that materialises `Debt` must include it. Default value `'RUB'` matches DSC-2.
- CMP-7. Skill in `internal/skills/budget_skill.go`:
  - **MUST** add three new cases: `debt_plan`, `debt_fix_plan`, `debt_progress`.
  - **MUST** extend the Manifest's `action` enum and update its description string (see SimpleAI bd `simpleAI-399` — vague descriptions cause router collisions).
  - **MUST** add fields to the existing args struct: `MinPayment *float64`, `MaxPayment *float64`, `Strategy string`. **MUST NOT** add new top-level structs.
  - Output **MUST** be human-readable Russian text (existing convention). Append the underlying error tag in parens on errors per AC-H3.

## API / contract surface

- API-1. **MUST NOT** introduce HTTP endpoints. The feature is exposed only through MCP/budget skill tool calls.
- API-2. The MCP tool name and JSON-schema Manifest changes **MUST** stay backward compatible: existing actions keep their schemas; the new actions are additive.
- API-3. Action argument names **MUST** use `snake_case` to match existing actions (`debt_name`, `min_payment`, `max_payment`, `strategy`, `monthly`).
- API-4. `debt_plan` returns text. **MUST** include: source of free cashflow value, chosen min, chosen max, scenario table (single-debt) or per-debt rows (portfolio).

## Currency handling

- CUR-1. MVP **MUST** assert single currency: if `len(distinct currencies of active debts) > 1` → return error `multi_currency_not_supported` with the list. No silent conversion.
- CUR-2. Free cashflow **MUST** be computed in the same currency as the debts. Reuse the existing `rates` plumbing from `GetForecastData` only if every active debt's currency equals the chart base; otherwise reject (CUR-1).
- CUR-3. **MUST NOT** call `internal/rates` directly from `payoff.go`. Currency conversion is the caller's responsibility.

## Code style

- STY-1. **MUST** follow existing patterns: comments on exported identifiers in Russian (matching `model.go`), tests in English-or-mixed but with `t.Helper()` and `t.Fatalf` like `forecast_test.go`.
- STY-2. **MUST NOT** introduce new dependencies. Stick to `time`, `math`, `errors`, `fmt`, `github.com/google/uuid` (already in `go.mod`).
- STY-3. **MUST NOT** use `panic` outside test setup. All error paths through returned error.
- STY-4. **MUST** reuse `apperr` patterns for skill-level errors (see how `pay_debt` reports today). **MUST NOT** print stack traces to user.
- STY-5. **MUST** keep money as `float64` consistent with the rest of the codebase. **MUST NOT** introduce `decimal` libraries in this PR.
- STY-6. Rounding **MUST** happen only at the formatting layer (string output). Internal calc keeps full `float64`.
- STY-7. `model.go` field comments **MUST** disambiguate `MonthlyPayment` ("minimum monthly payment required by the lender") from `PlannedMonthly` ("amount the user committed to pay each month per `debt_fix_plan`"). Avoid contributors confusing them.

## Testing strategy

- TST-1. `payoff_test.go` **MUST** cover all of Group B and Group C of acceptance criteria with table-driven tests; **MUST** include the avalanche/snowball ordering cases (AC-C3, AC-C4) explicitly.
- TST-2. `store_test.go` **MUST** add a test for `UpdateDebtPlan` round-trip (write → read → fields match).
- TST-3. Skill tests in `internal/skills/budget_skill_test.go` **MUST** cover at least: AC-D1, AC-D6, AC-D7 (no DB write), AC-E1, AC-E4, AC-F4. Use a real sqlite store (existing pattern), not mocks.
- TST-4. **MUST NOT** add a forecast-free test that hits the network or expects external rates — pass `rates` map inline, mirroring `forecast_test.go`.
- TST-5. `go test ./internal/budget/... ./internal/skills/...` **MUST** pass green before any PR merge. Project also runs `go vet` and `gofmt -l` — **MUST** be clean.

## Routing & Manifest hygiene

- RTG-1. The Manifest description for `debt_plan` **MUST** include the verb cluster {"plan", "scenario", "min vs max", "payoff date", "распиши", "план", "сравни"} and explicitly delineate from `debt_status` ("show progress / current balance"). Reference bd `simpleAI-399` in the bd notes for the trigger-collision rationale.
- RTG-2. The Manifest description for `debt_fix_plan` **MUST** include {"зафиксируй", "сохрани план", "план на N в месяц", "fix"}.
- RTG-3. **MUST** add a routing test (eval table or skill_test) that asserts AC-I1..I4 with mocked LLM router OR via static manifest description match (whichever pattern already exists in repo — check before adding).

## Logging & observability

- LOG-1. **MUST** log every failed plan call (with debt id and error tag) via the existing logger pattern used by `pay_debt`.
- LOG-2. **MUST NOT** log full debt amounts at INFO level — keep amounts at DEBUG only. Log balance/payment rounded to nearest 1000 to limit PII.

## Validation rules (MUST hold at boundary)

- VAL-1. `interest_rate ∈ [0, 1]`. Reject otherwise → `invalid_rate`.
- VAL-2. `monthly`, `balance`, `min_payment`, `max_payment` **MUST** be `> 0` (or `≥ 0` for balance). Negative → validation error pre-call.
- VAL-3. `strategy ∈ {"", "avalanche", "snowball"}`. Empty defaults to `avalanche`. Other values → error.
- VAL-4. `debt_name` lookup **MUST** be case-insensitive substring on `Debt.Name`, matching the existing `pay_debt` convention (verify in code before implementing).

---

## Decisions & Trade-offs

### D1. Flat monthly % over annuity amortization
- **Picked:** Flat `balance_{n+1} = balance_n*(1+rate) − payment` model.
- **Rejected:** Bank-style annuity with split principal/interest schedule.
- **Why:** User explicitly chose flat in interview. Russian credit cards work this way. Annuity adds a payment-table data structure and per-month interest derivation that we'd throw away when actual transactions arrive. Adding it later is a single new function call site, not a redesign.

### D2. No `debt_plans` history table
- **Picked:** Plan stored as three columns on `debts`.
- **Rejected:** Separate `debt_plans` table with snapshots.
- **Why:** Single-user app, MVP. History of "previous plans" has no consumer in the spec. Adding the table means migration + repo methods + retention policy with zero benefit. Adding it later is one migration, no data migration required.

### D3. Pure calc functions, separate file
- **Picked:** `payoff.go` with no DB dependency.
- **Rejected:** Methods on `*Store`.
- **Why:** Calculation is the part most likely to be wrong; isolating it makes table-driven tests trivial and keeps `store.go` (already 700+ lines) from growing. Also lets us swap to amortization in D1's future without touching the store.

### D4. Stateless dialog, fixation as separate action
- **Picked:** `debt_plan` is read-only + `debt_fix_plan` mutates.
- **Rejected:** Stateful conversation with session storage.
- **Why:** User picked this in interview; bot has no session storage today; LLM router is single-shot per message. Adding dialog state requires a new abstraction (chat session FSM) that touches every action — out of scope.

### D5. Forecast-derived free cashflow with explicit min-payment subtraction
- **Picked:** `free = forecast_income − forecast_expense_excluding_debts − Σ min_payments`.
- **Rejected:** (a) use rolling 3-month avg balance; (b) trust forecast as-is and pray double-count doesn't happen.
- **Why:** Forecast already aggregates recurring expenses; if a debt payment is in there, naive use double-subtracts. Explicit Σ min and explicit exclusion from forecast_expense make the math auditable. Cost: must label/identify "this expense is a debt payment" in `forecast_expense` aggregation — see OQ1 below.

### D6. Avalanche default
- **Picked:** Avalanche (highest rate first).
- **Rejected:** Snowball as default; "always show both".
- **Why:** Mathematically optimal. Snowball is a behavioural-finance crutch; user already manages own finances, doesn't need motivation hack as default. Both strategies remain available via `strategy=` param.

### D7. Single currency in MVP
- **Picked:** Reject mixed-currency portfolios with a clear error.
- **Rejected:** Convert everything to one currency via `internal/rates` on the fly.
- **Why:** Conversion introduces FX risk into the plan numbers, requires a stable rate decision (today's rate? forward rate?), and adds a stateful dependency on `internal/rates` to a pure calc function. Single-currency covers the user's actual case (one credit card) and ships fast.

### D8. Existing `Debt.MonthlyPayment` becomes "preferred min"
- **Picked:** Default min = `Debt.MonthlyPayment` if set; else if `interest_rate > 0` then `floor(balance*rate)+1`; else `max(balance/60, 1)` (5-year amortization fallback).
- **Rejected:** Always require min in the request; ignore `MonthlyPayment` going forward.
- **Why:** `MonthlyPayment` is already populated by users who tracked monthly payments before this feature; reusing it preserves their data. The fallback formula guarantees a converging plan when `MonthlyPayment` is absent, instead of a confusing `payment_below_accrual` error.

### D9. No `transaction` auto-creation
- **Picked:** `debt_fix_plan` only updates plan fields. Actual payment recording stays via existing `pay_debt`.
- **Rejected:** Auto-debit a `transaction` row at fixation time.
- **Why:** Out of scope per interview. Also: fixing a plan is intent, not payment. Coupling them creates phantom expenses that didn't actually happen yet.

### D10. Manifest description duplication acceptable
- **Picked:** Verb-rich descriptions per action even with overlap.
- **Rejected:** Compact descriptions to keep prompt small.
- **Why:** Bd `simpleAI-399` shows real cost of vague descriptions (router collisions). Verbose pays for itself. Total Manifest still well under any practical token budget.

### D11. `interest_rate` as monthly, not APR
- **Picked:** Decimal monthly rate, e.g. `0.024` = 2.4%/month.
- **Rejected:** APR (annual percentage rate).
- **Why:** User's mental model is "по карте 2% в месяц" — Russian credit cards and microloans quote monthly. APR storage with conversion at calc time invites errors. Document this in `model.go` field comment.

---

## Open Questions resolved
- OQ1 (how to label debt payments in forecast): **MUST** introduce a sentinel category name `"Платёж по долгу"` in `internal/constants` and treat any `Transaction` with this category as a debt payment for the purpose of `forecast_expense` exclusion (FR7.1, AC-G2). Alternative considered: dedicated `is_debt_payment` flag on Transaction; rejected as schema churn for a single use case. If `pay_debt` doesn't currently create a Transaction row, that gap is documented but **not closed in this MVP** — see scope: "auto-create transaction" is OOS. Until then, `forecast_expense` exclusion is a no-op (nothing to exclude), and `Σ min_payments` alone is the correction.
- OQ2 (multi-currency design): deferred. CUR-1 makes the boundary explicit. No code today uses `internal/rates` from this feature.
- OQ3 (output style): plain text + bullet list, no markdown. Match existing `debt_status` formatting (verify by reading its handler before implementing).
