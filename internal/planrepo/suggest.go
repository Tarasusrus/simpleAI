package planrepo

import (
	"context"
	"time"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
	"simpleAI/internal/skills/safetospend"
)

// MinSuggestMonths — сколько полных календарных месяцев истории нужно категории для
// подсказки: по одному месяцу среднего нет (ADR-011).
const MinSuggestMonths = 2

// SuggestSource — то, что подсказке суммы нужно от budget.Store.
type SuggestSource interface {
	PlanLimitHistory(ctx context.Context) ([]budget.MonthlyCategoryExpense, error)
	GetExchangeRates(ctx context.Context) (map[string]float64, error)
}

// LimitSuggester считает «сколько обычно уходит на категорию за окно».
type LimitSuggester struct {
	src SuggestSource
	now func() time.Time
}

var _ plan.LimitSuggester = (*LimitSuggester)(nil)

func NewLimitSuggester(src SuggestSource) *LimitSuggester {
	return &LimitSuggester{src: src, now: time.Now}
}

// Suggest: сумма трат категории за всю историю (без повторяющихся), делённая на число
// ПОЛНЫХ календарных месяцев от первого месяца с тратой до последнего завершённого, и
// пересчитанная на days дней (days/30). Месяцы без трат входят в делитель: иначе две
// траты за полгода дали бы «среднее» по двум месяцам и завысили бы лимит.
func (s *LimitSuggester) Suggest(ctx context.Context, category string, days int) (plan.LimitSuggestion, error) {
	rows, err := s.src.PlanLimitHistory(ctx)
	if err != nil {
		return plan.LimitSuggestion{}, err
	}
	rates, err := s.src.GetExchangeRates(ctx)
	if err != nil {
		return plan.LimitSuggestion{}, err
	}
	key := budget.NormalizeName(category)
	var first time.Time
	var sum float64
	for _, r := range rows {
		if budget.NormalizeName(r.CategoryName) != key {
			continue
		}
		if first.IsZero() || r.Month.Before(first) {
			first = r.Month
		}
		if thb, ok := budget.ToTHB(r.Total, r.Currency, rates); ok {
			sum += thb
		}
	}
	if first.IsZero() {
		return plan.LimitSuggestion{}, nil
	}
	n := s.now().UTC()
	last := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	span := (last.Year()-first.Year())*12 + int(last.Month()-first.Month()) + 1
	out := plan.LimitSuggestion{Months: max(span, 0)}
	if span < MinSuggestMonths {
		return out, nil
	}
	fc := []budget.CategoryForecast{{CategoryName: category, Currency: "THB", ForecastAmount: sum / float64(span)}}
	amount, ok := safetospend.WindowAmountTHBMinor(fc, category, days, rates)
	if !ok || amount <= 0 {
		return out, nil
	}
	out.Enough, out.AmountTHBMinor = true, amount
	return out, nil
}
