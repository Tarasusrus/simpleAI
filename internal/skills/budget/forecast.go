package budgetskill

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

func (s *BudgetSkill) forecastAction(ctx context.Context, req budgetInput) (string, error) {
	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil || len(rates) == 0 {
		rates = rubRates
	} else {
		for k, v := range rubRates {
			if _, ok := rates[k]; !ok {
				rates[k] = v
			}
		}
	}

	forecasts, err := s.store.GetForecastData(ctx, req.Months, rates)
	if err != nil {
		return fmt.Sprintf("Не удалось посчитать прогноз: %v", err), nil
	}
	if len(forecasts) == 0 {
		return "Недостаточно данных для прогноза — добавь несколько трат и попробуй снова.", nil
	}

	now := time.Now()
	next := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	out := formatForecastBlock(forecasts, next, rates)

	if obligations := s.collectUpcomingObligations(ctx, next, rates); obligations != "" {
		out += "\n" + obligations
	}
	return out, nil
}

func (s *BudgetSkill) collectUpcomingObligations(ctx context.Context, nextMonth time.Time, rates map[string]float64) string {
	thbRate := rates["THB"]
	if thbRate == 0 {
		thbRate = rubRates["THB"]
	}
	if thbRate == 0 {
		return ""
	}

	monthStart := time.Date(nextMonth.Year(), nextMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, -1)
	monthEnd = time.Date(monthEnd.Year(), monthEnd.Month(), monthEnd.Day(), 23, 59, 59, 0, time.UTC)

	var recurringTHB float64
	var recurringCnt int
	if chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64); ok && chatID != 0 {
		if list, err := s.store.ListRecurring(ctx, chatID); err == nil {
			for _, r := range list {
				if !r.Enabled || r.Type != "expense" {
					continue
				}
				if r.NextDate.Before(monthStart) || r.NextDate.After(monthEnd) {
					continue
				}
				recurringTHB += toRUB(r.Amount, r.Currency, rates) / thbRate
				recurringCnt++
			}
		}
	}

	var debtTHB float64
	var debtCnt int
	if debts, err := s.store.ListDebts(ctx); err == nil {
		for _, d := range debts {
			if d.Status != "active" || d.Direction != "owe" {
				continue
			}
			if d.MonthlyPayment == nil || *d.MonthlyPayment <= 0 {
				continue
			}
			debtTHB += toRUB(*d.MonthlyPayment, "RUB", rates) / thbRate
			debtCnt++
		}
	}

	if recurringCnt == 0 && debtCnt == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("📌 Известные обязательства:\n")
	if recurringCnt > 0 {
		fmt.Fprintf(&sb, "  🔁 Подписки/recurring: ~%.0f ฿  (%d)\n", recurringTHB, recurringCnt)
	}
	if debtCnt > 0 {
		fmt.Fprintf(&sb, "  💳 Платежи по долгам: ~%.0f ฿  (%d)\n", debtTHB, debtCnt)
	}
	fmt.Fprintf(&sb, "  ──────────────────────\n  Итого обязательств: ~%.0f ฿\n", recurringTHB+debtTHB)
	sb.WriteString("ℹ️ Прогноз выше — среднее по истории. Recurring мог уже учтён в нём.\n")
	return sb.String()
}

type forecastItem struct {
	Icon        string
	Name        string
	TotalTHB    float64
	TrendPct    float64
	HasTrend    bool
	dominantTHB float64
}

func formatForecastBlock(forecasts []budget.CategoryForecast, nextMonth time.Time, rates map[string]float64) string {
	if len(forecasts) == 0 {
		return ""
	}

	thbRate := rates["THB"]
	if thbRate == 0 {
		thbRate = rubRates["THB"]
	}

	merged := map[string]*forecastItem{}
	for _, f := range forecasts {
		amountTHB := toRUB(f.ForecastAmount, f.Currency, rates) / thbRate
		icon := f.Icon
		if icon == "" {
			icon = "📦"
		}
		if m, ok := merged[f.CategoryName]; ok {
			m.TotalTHB += amountTHB
			if amountTHB > m.dominantTHB {
				m.TrendPct = f.TrendPct
				m.HasTrend = f.HasTrend
				m.dominantTHB = amountTHB
			}
		} else {
			merged[f.CategoryName] = &forecastItem{
				Icon: icon, Name: f.CategoryName, TotalTHB: amountTHB,
				TrendPct: f.TrendPct, HasTrend: f.HasTrend, dominantTHB: amountTHB,
			}
		}
	}

	items := make([]*forecastItem, 0, len(merged))
	for _, m := range merged {
		items = append(items, m)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TotalTHB > items[j].TotalTHB })

	const maxItems = 8
	var otherTHB float64
	var totalTHB float64
	for _, m := range items {
		totalTHB += m.TotalTHB
	}
	for i, m := range items {
		if i >= maxItems {
			otherTHB += m.TotalTHB
		}
	}

	months := [...]string{"", "январь", "февраль", "март", "апрель", "май", "июнь",
		"июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"}

	var sb strings.Builder
	fmt.Fprintf(&sb, "🔮 Прогноз на %s %d:\n", months[nextMonth.Month()], nextMonth.Year())

	limit := len(items)
	if limit > maxItems {
		limit = maxItems
	}
	for _, m := range items[:limit] {
		trend := trendLabel(m.TrendPct, m.HasTrend)
		if trend != "" {
			fmt.Fprintf(&sb, "  %s %-14s ~%.0f ฿  (%s)\n", m.Icon, m.Name, m.TotalTHB, trend)
		} else {
			fmt.Fprintf(&sb, "  %s %-14s ~%.0f ฿\n", m.Icon, m.Name, m.TotalTHB)
		}
	}
	if otherTHB > 0 {
		fmt.Fprintf(&sb, "  📦 другие        ~%.0f ฿\n", otherTHB)
	}
	fmt.Fprintf(&sb, "  ──────────────────────\n  Итого: ~%.0f ฿\n", totalTHB)

	return sb.String()
}

func trendLabel(pct float64, hasTrend bool) string {
	if !hasTrend {
		return ""
	}
	if pct > 200 || pct < -200 {
		return ""
	}
	switch {
	case pct > 5:
		return fmt.Sprintf("↑ +%.0f%%", pct)
	case pct < -5:
		return fmt.Sprintf("↓ %.0f%%", pct)
	default:
		return "→"
	}
}
