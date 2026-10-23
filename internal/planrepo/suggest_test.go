package planrepo

import (
	"context"
	"testing"
	"time"

	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
)

type fakeSuggestSource struct {
	rows  []budget.MonthlyCategoryExpense
	rates map[string]float64
}

func (f fakeSuggestSource) PlanLimitHistory(context.Context) ([]budget.MonthlyCategoryExpense, error) {
	return f.rows, nil
}

func (f fakeSuggestSource) GetExchangeRates(context.Context) (map[string]float64, error) {
	return f.rates, nil
}

func monthRow(cat string, m time.Month, total float64) budget.MonthlyCategoryExpense {
	return budget.MonthlyCategoryExpense{CategoryName: cat, Currency: "THB", Month: time.Date(2026, m, 1, 0, 0, 0, 0, time.UTC), Total: total}
}

func withCount(r budget.MonthlyCategoryExpense, n int) budget.MonthlyCategoryExpense {
	r.Count = n
	return r
}

// AvgCount — сколько трат категории приходится на окно: count/месяцев·days/30, округление, не меньше 1.
func TestLimitSuggester_AvgCount(t *testing.T) {
	rates := map[string]float64{"THB": 2.5, "RUB": 1}
	rows := []budget.MonthlyCategoryExpense{
		withCount(monthRow("Еда", 1, 8000), 6), withCount(monthRow("Еда", 2, 10000), 8), // 14 трат за 2 мес = 7/мес → 3,27 за 14 дн.
		withCount(monthRow("Редкая", 1, 900), 1), withCount(monthRow("Редкая", 2, 900), 0), // 1/2 мес → 0,23 → не меньше 1
		withCount(monthRow("Кафе", 2, 5000), 9), // один месяц — AvgCount нет
	}
	s := NewLimitSuggester(fakeSuggestSource{rows: rows, rates: rates})
	s.now = func() time.Time { return time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC) }
	if got := mustSuggest(t, s, "Еда", 14); got.AvgCount != 3 {
		t.Fatalf("Еда: AvgCount=%d, ждали 3", got.AvgCount)
	}
	if got := mustSuggest(t, s, "Редкая", 14); got.AvgCount != 1 {
		t.Fatalf("Редкая: AvgCount=%d, ждали 1", got.AvgCount)
	}
	if got := mustSuggest(t, s, "Кафе", 14); got.AvgCount != 0 || got.Enough {
		t.Fatalf("Кафе: %+v — при одном месяце цифр нет", got)
	}
	if got := mustSuggest(t, s, "Еда", 28); got.AvgCount != 7 {
		t.Fatalf("Еда, 28 дн.: AvgCount=%d, ждали 7", got.AvgCount)
	}
}

func TestLimitSuggester(t *testing.T) {
	rates := map[string]float64{"THB": 2.5, "RUB": 1}
	rows := []budget.MonthlyCategoryExpense{
		monthRow("Еда", 1, 8000), monthRow("Еда", 2, 10000), // среднее 9000 ฿/мес
		monthRow("Кафе", 2, 5000), // один месяц — мало
		monthRow("Такси", 1, 300), monthRow("Такси", 2, 300),
	}
	s := NewLimitSuggester(fakeSuggestSource{rows: rows, rates: rates})
	s.now = func() time.Time { return time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC) }
	ctx := context.Background()

	got, err := s.Suggest(ctx, " еда ", 14)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enough || got.Months != 2 || got.AmountTHBMinor != 420_000 {
		t.Fatalf("Еда: %+v, ждали 2 месяца и 420000 (9000·14/30 ฿)", got)
	}
	if got := mustSuggest(t, s, "Кафе", 14); got.Enough || got.Months != 1 || got.AmountTHBMinor != 0 {
		t.Fatalf("Кафе, один месяц: %+v — подсказки быть не должно", got)
	}
	if got := mustSuggest(t, s, "Нет такой", 14); got.Enough || got.Months != 0 {
		t.Fatalf("нет истории: %+v", got)
	}
	// месяцы без трат входят в делитель: Дек 6000 и Фев 3000 за три месяца — 3000 ฿/мес, а не 4500
	gaps := []budget.MonthlyCategoryExpense{monthRow("Подарки", 2, 3000), {CategoryName: "Подарки", Currency: "THB", Month: time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), Total: 6000}}
	sg := NewLimitSuggester(fakeSuggestSource{rows: gaps, rates: rates})
	sg.now = s.now
	if got := mustSuggest(t, sg, "Подарки", 14); !got.Enough || got.Months != 3 || got.AmountTHBMinor != 140_000 {
		t.Fatalf("Подарки: %+v, ждали 3 месяца и 140000 (3000·14/30 ฿)", got)
	}
	noRate := NewLimitSuggester(fakeSuggestSource{rows: rows, rates: map[string]float64{"RUB": 1}})
	noRate.now = s.now
	if got := mustSuggest(t, noRate, "Еда", 14); got.Enough {
		t.Fatalf("нет курса бата: %+v", got)
	}
}

func mustSuggest(t *testing.T, s *LimitSuggester, cat string, days int) plan.LimitSuggestion {
	t.Helper()
	got, err := s.Suggest(context.Background(), cat, days)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	return got
}
