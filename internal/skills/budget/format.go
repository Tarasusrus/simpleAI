package budgetskill

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"simpleAI/internal/budget"
)

var rubRates = map[string]float64{
	"RUB": 1.0,
	"THB": 2.5,
	"USD": 82.0,
	"EUR": 90.0,
}

func toRUB(amount float64, currency string, rates map[string]float64) float64 {
	if rate, ok := rates[currency]; ok {
		return amount * rate
	}
	return amount
}

func summaryTotalRUB(s *budget.Summary, rates map[string]float64) float64 {
	var total float64
	for _, cg := range s.Currencies {
		total += toRUB(cg.TotalExpense, cg.Currency, rates)
	}
	return total
}

func summaryIncomeRUB(s *budget.Summary, rates map[string]float64) float64 {
	var total float64
	for _, cg := range s.Currencies {
		total += toRUB(cg.TotalIncome, cg.Currency, rates)
	}
	return total
}

func summaryCategories(s *budget.Summary, rates map[string]float64) map[string]float64 {
	m := map[string]float64{}
	for _, cg := range s.Currencies {
		for _, c := range cg.ByCategory {
			m[c.CategoryName] += toRUB(c.Total, cg.Currency, rates)
		}
	}
	return m
}

func formatSummary(s *budget.Summary, prev *budget.Summary, rates map[string]float64) string {
	return formatSummaryFiltered(s, prev, rates, "")
}

func formatSummaryFiltered(s *budget.Summary, prev *budget.Summary, rates map[string]float64, filter string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — сводка\n", formatPeriodName(s.Period))

	totalRUB := summaryTotalRUB(s, rates)
	incomeRUB := summaryIncomeRUB(s, rates)

	if filter == "income" {
		if incomeRUB <= 0 {
			fmt.Fprintf(&sb, "\nДоходов за %s нет.", formatPeriodName(s.Period))
			return sb.String()
		}
		fmt.Fprintf(&sb, "\n💰 Доходы: ~%.0f ₽ экв.\n", incomeRUB)
		for _, cg := range s.Currencies {
			if cg.TotalIncome <= 0 {
				continue
			}
			sym := currencySymbol(cg.Currency)
			if cg.Currency == "RUB" {
				fmt.Fprintf(&sb, "  %.0f ₽\n", cg.TotalIncome)
			} else {
				fmt.Fprintf(&sb, "  %.0f %s\n", cg.TotalIncome, sym)
			}
		}
		return sb.String()
	}

	if len(s.Currencies) == 0 {
		sb.WriteString("\nОпераций нет.")
		return sb.String()
	}

	if filter == "" && incomeRUB > 0 {
		fmt.Fprintf(&sb, "\n💰 Доходы: ~%.0f ₽ экв.\n", incomeRUB)
	}
	fmt.Fprintf(&sb, "\n💸 Всего потрачено: ~%.0f ₽ экв.\n", totalRUB)
	if filter == "" && incomeRUB > 0 {
		balance := incomeRUB - totalRUB
		sign := "+"
		if balance < 0 {
			sign = ""
		}
		fmt.Fprintf(&sb, "📊 Баланс: %s%.0f ₽ экв.\n", sign, balance)
	}

	type catEntry struct {
		name     string
		icon     string
		rubTotal float64
		origAmt  float64
		origCur  string
	}
	catMap := map[string]*catEntry{}
	for _, cg := range s.Currencies {
		for _, c := range cg.ByCategory {
			key := c.CategoryName
			rubAmt := toRUB(c.Total, cg.Currency, rates)
			if e, ok := catMap[key]; ok {
				e.rubTotal += rubAmt
				if cg.Currency != "RUB" {
					e.origAmt += c.Total
					e.origCur = cg.Currency
				}
			} else {
				orig := ""
				origAmt := 0.0
				if cg.Currency != "RUB" {
					orig = cg.Currency
					origAmt = c.Total
				}
				icon := c.Icon
				if icon == "" {
					icon = "•"
				}
				catMap[key] = &catEntry{
					name:     c.CategoryName,
					icon:     icon,
					rubTotal: rubAmt,
					origAmt:  origAmt,
					origCur:  orig,
				}
			}
		}
	}

	cats := make([]*catEntry, 0, len(catMap))
	for _, e := range catMap {
		cats = append(cats, e)
	}
	sort.Slice(cats, func(i, j int) bool {
		return cats[i].rubTotal > cats[j].rubTotal
	})

	sb.WriteString("\nПо категориям:\n")
	for _, e := range cats {
		pct := 0.0
		if totalRUB > 0 {
			pct = e.rubTotal / totalRUB * 100
		}
		sym := currencySymbol(e.origCur)
		if e.origCur != "" && e.origAmt > 0 {
			fmt.Fprintf(&sb, "%s %-14s ~%6.0f ₽  %2.0f%%  (%.0f %s)\n",
				e.icon, e.name, e.rubTotal, pct, e.origAmt, sym)
		} else {
			fmt.Fprintf(&sb, "%s %-14s %7.0f ₽  %2.0f%%\n",
				e.icon, e.name, e.rubTotal, pct)
		}
	}

	if prev != nil && len(prev.Currencies) > 0 {
		prevTotal := summaryTotalRUB(prev, rates)
		if prevTotal > 0 {
			diff := totalRUB - prevTotal
			sign := "+"
			if diff < 0 {
				sign = ""
			}
			pct := diff / prevTotal * 100
			prevName := formatPeriodName(prev.Period)
			fmt.Fprintf(&sb, "\nvs %s: %s%.0f ₽ (%s%.0f%%)\n", prevName, sign, diff, sign, pct)

			prevCats := summaryCategories(prev, rates)
			type catDiff struct {
				name string
				pct  float64
			}
			var diffs []catDiff
			for _, e := range cats {
				if pv, ok := prevCats[e.name]; ok && pv > 0 {
					d := (e.rubTotal - pv) / pv * 100
					if d > 15 || d < -15 {
						diffs = append(diffs, catDiff{e.name, d})
					}
				} else if _, ok := prevCats[e.name]; !ok && e.rubTotal > 1000 {
					diffs = append(diffs, catDiff{e.name + " (новое)", 100})
				}
			}
			sort.Slice(diffs, func(i, j int) bool {
				ai, aj := diffs[i].pct, diffs[j].pct
				if ai < 0 {
					ai = -ai
				}
				if aj < 0 {
					aj = -aj
				}
				return ai > aj
			})
			for i, d := range diffs {
				if i >= 2 {
					break
				}
				arrow := "↑"
				if d.pct < 0 {
					arrow = "↓"
				}
				fmt.Fprintf(&sb, "  %s %s %+.0f%%\n", arrow, d.name, d.pct)
			}
		}
	}

	return sb.String()
}

// summary — action='summary': сводка за период.
func (s *BudgetSkill) summary(ctx context.Context, req budgetInput) (string, error) {
	p := parsePeriod(req.Period)
	curr, err := s.store.GetSummary(ctx, p)
	if err != nil {
		return "", fmt.Errorf("get summary: %w", err)
	}
	prev, err := s.store.GetSummary(ctx, prevMonthPeriod(p))
	if err != nil {
		prev = nil
	}

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

	filter := strings.ToLower(strings.TrimSpace(req.TransactionType))
	if filter != "income" && filter != "expense" {
		filter = ""
	}
	result := formatSummaryFiltered(curr, prev, rates, filter)

	next := time.Date(p.To.Year(), p.To.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	currentMonth := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	if filter != "income" && next.After(currentMonth) {
		forecasts, forecastErr := s.store.GetForecastData(ctx, 0, rates)
		if forecastErr == nil && len(forecasts) > 0 {
			result += "\n" + formatForecastBlock(forecasts, next, rates)
		}
	}

	return result, nil
}

func renderTransactionList(periodLabel string, txs []budget.Transaction) string {
	if len(txs) == 0 {
		return fmt.Sprintf("Транзакций за %s нет.", periodLabel)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Транзакции за %s:\n\n", periodLabel)
	for _, t := range txs {
		icon, sign := "🔴", "-"
		if t.Type == "income" {
			icon, sign = "🟢", "+"
		}
		cat := t.CategoryName
		if cat == "" {
			cat = "без категории"
		}
		desc := t.Description
		if desc != "" {
			desc = " — " + desc
		}
		fmt.Fprintf(&sb, "[%s] %s %s%.0f %s | %s%s | %s\n",
			t.ID.String()[:8], icon, sign, t.Amount, currencySymbol(t.Currency), cat, desc, t.Date.Format("02.01"))
	}
	return sb.String()
}

func renderGoalList(goals []budget.Goal) string {
	if len(goals) == 0 {
		return "Целей пока нет. Создайте цель, например: «хочу накопить 100000 на отпуск»."
	}
	var sb strings.Builder
	sb.WriteString("🎯 Цели накоплений:\n\n")
	for _, g := range goals {
		pct := 0.0
		if g.TargetAmount > 0 {
			pct = g.CurrentAmount / g.TargetAmount * 100
		}
		status := ""
		switch g.Status {
		case "completed":
			status = " ✅"
		case "cancelled":
			status = " ❌"
		}
		fmt.Fprintf(&sb, "• %s%s: %.0f / %.0f ₽ (%.0f%%)\n",
			g.Name, status, g.CurrentAmount, g.TargetAmount, pct)
		if g.Deadline != nil && g.Status == "active" {
			fmt.Fprintf(&sb, "  Дедлайн: %s\n", g.Deadline.Format("02.01.2006"))
		}
	}
	return sb.String()
}

func renderDebtList(debts []budget.Debt) string {
	if len(debts) == 0 {
		return "Долгов нет 🎉"
	}
	var sb strings.Builder
	sb.WriteString("📋 Долги:\n\n")
	for _, d := range debts {
		remaining := d.TotalAmount - d.PaidAmount
		pct := 0.0
		if d.TotalAmount > 0 {
			pct = d.PaidAmount / d.TotalAmount * 100
		}
		dir := "я должен"
		if d.Direction == "owed" {
			dir = "мне должны"
		}
		status := ""
		if d.Status == "paid" {
			status = " ✅"
		}
		fmt.Fprintf(&sb, "• %s%s: осталось %.0f ₽ из %.0f ₽ (%.0f%% погашено) — %s\n",
			d.Name, status, remaining, d.TotalAmount, pct, dir)
	}
	return sb.String()
}

func renderRecurringList(list []budget.RecurringPayment) string {
	if len(list) == 0 {
		return "Повторяющихся платежей нет. Чтобы добавить, скажи: «каждое 1-е число списывай аренду 30000 THB»."
	}
	var sb strings.Builder
	sb.WriteString("🔄 *Повторяющиеся платежи:*\n\n")
	for _, r := range list {
		status := "✅"
		if !r.Enabled {
			status = "⏸"
		}
		day := "?"
		if r.DayOfMonth != nil {
			day = fmt.Sprintf("%d-е", *r.DayOfMonth)
		}
		fmt.Fprintf(&sb, "%s %s — %.0f %s | каждое %s | след. %s | ID: %s\n",
			status, r.Name, r.Amount, r.Currency, day,
			r.NextDate.Format("02.01"), r.ID.String()[:8])
	}
	return sb.String()
}

func formatPeriodName(p budget.Period) string {
	months := []string{
		"", "январь", "февраль", "март", "апрель", "май", "июнь",
		"июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь",
	}
	m := int(p.From.Month())
	if m >= 1 && m <= 12 {
		return fmt.Sprintf("%s %d", months[m], p.From.Year())
	}
	return fmt.Sprintf("%s – %s", p.From.Format("02.01.2006"), p.To.Format("02.01.2006"))
}

func prevMonthPeriod(p budget.Period) budget.Period {
	from := time.Date(p.From.Year(), p.From.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(p.From.Year(), p.From.Month(), 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)
	return budget.Period{From: from, To: to}
}

func currentMonthPeriod() budget.Period {
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)
	return budget.Period{From: from, To: to}
}

func parsePeriod(s string) budget.Period {
	if s == "" || s == "month" {
		return currentMonthPeriod()
	}
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return currentMonthPeriod()
	}
	from := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)
	return budget.Period{From: from, To: to}
}

func parseFlexibleDate(s string) time.Time {
	for _, layout := range []string{"2006-01-02", "02.01.2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func currencySymbol(currency string) string {
	switch currency {
	case "USD":
		return "$"
	case "EUR":
		return "€"
	case "THB":
		return "฿"
	case "CNY":
		return "¥"
	default:
		return "₽"
	}
}

func monthsUntil(deadline time.Time) int {
	now := time.Now()
	months := (deadline.Year()-now.Year())*12 + int(deadline.Month()) - int(now.Month())
	if months < 1 {
		return 1
	}
	return months
}
