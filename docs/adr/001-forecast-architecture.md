# ADR-001: Forecast Architecture — Stage 1 (Baseline)

**Date:** 2026-04-04  
**Status:** Accepted  
**Issues:** #38, #39, #40

---

## Context

~1.5 months of transaction data available. Need a forecasting foundation that:
- Is useful now (Baseline)
- Can be extended to ML later (Prophet, LightGBM)
- Is a shared service used by: summary, advisor (#43), debt manager (#44)

---

## Decision

### Where to compute
**Go on-demand via SQL aggregates.** No Python microservice for Stage 1.

Rationale: average/median/trend is trivially expressible in SQL + Go arithmetic. Python overhead (deployment, latency, ops) is not justified until Stage 3 (ML models).

### Where to live in code
**New `forecast` action inside BudgetSkill**, not a separate plugin.

Rationale: forecast reads from `budget_transaction` via the same Store — no new dependencies. Extracting to a separate ForecastSkill or microservice deferred to Stage 3 when ML is introduced.

### Storage
**On-demand, no caching table.** 

Rationale: with < 3 months of data the query is fast (< 50ms). A `budget_forecast` cache table will be added in Stage 3 when ML computation becomes expensive.

### Integration with summary
Forecast section appended to `summary` action output when ≥ 2 months of data exist. If only 1 month — show averages without trend arrows.

---

## Stage 1 Algorithm

**Input:** last N months of transactions (default: all available, min 1)

**Per category:**
1. `avg_amount` = average monthly spend over last N months
2. `trend` = (last_month - prev_month) / prev_month × 100% — requires ≥ 2 months
3. `forecast_amount` = avg_amount (Stage 1; will use EWM in Stage 2)

**Output:** `CategoryForecast{ Category, Icon, Currency, ForecastAmount, TrendPct, HasTrend }`

**Format in summary:**
```
🔮 Прогноз на <next month>:
  🍔 Еда        ~9 200 THB  (↑ +8%)
  🏠 Жильё      ~30 000 THB (→ стабильно)
  🚕 Транспорт  ~3 100 THB  (↓ -5%)
  ─────────────────────
  Итого: ~44 100 THB | ~8 000 RUB
```

Trend thresholds: < ±3% → "стабильно", ≥ 3% → show arrow + percent.

---

## Future Stages

| Stage | Method | Trigger |
|-------|--------|---------|
| 2 | EWM, seasonal decomposition | 4+ months data |
| 3 | Prophet / LightGBM, Python microservice, forecast cache | 8+ months data |

---

## Consequences

- `forecast` action added to BudgetSkill manifest (shared entry point)
- `summary` action extended: appends forecast block when data available
- No new tables, no new services for Stage 1
- ForecastSkill extraction deferred — interface boundary defined now via `CategoryForecast` struct in `internal/budget/model.go`
