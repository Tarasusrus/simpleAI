package skills

import (
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

// formatSummary показывает блок Доходы и Баланс когда TotalIncome > 0.
func TestFormatSummary_WithIncome(t *testing.T) {
	rates := map[string]float64{"RUB": 1, "THB": 2.5}
	s := &budget.Summary{
		Period: budget.Period{
			From: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		},
		Currencies: []budget.CurrencyGroup{{
			Currency:     "RUB",
			TotalIncome:  100000,
			TotalExpense: 60000,
			Balance:      40000,
			ByCategory: []budget.CategoryTotal{
				{CategoryName: "Еда", Total: 60000, Icon: "🍔"},
			},
		}},
	}
	out := formatSummary(s, nil, rates)
	for _, want := range []string{"Доходы", "100000", "Всего потрачено", "60000", "Баланс", "+40000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
}

// formatSummary не показывает блоки Доходы/Баланс когда TotalIncome == 0.
func TestFormatSummary_NoIncome(t *testing.T) {
	rates := map[string]float64{"RUB": 1}
	s := &budget.Summary{
		Period: budget.Period{
			From: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		},
		Currencies: []budget.CurrencyGroup{{
			Currency:     "RUB",
			TotalIncome:  0,
			TotalExpense: 5000,
			ByCategory: []budget.CategoryTotal{
				{CategoryName: "Еда", Total: 5000, Icon: "🍔"},
			},
		}},
	}
	out := formatSummary(s, nil, rates)
	if strings.Contains(out, "Доходы") {
		t.Fatalf("Доходы block must be hidden when income=0: %s", out)
	}
	if strings.Contains(out, "Баланс") {
		t.Fatalf("Баланс block must be hidden when income=0: %s", out)
	}
	if !strings.Contains(out, "Всего потрачено") {
		t.Fatalf("expense block must be shown: %s", out)
	}
}

// formatSummary показывает отрицательный баланс когда расходы > доходов.
func TestFormatSummary_NegativeBalance(t *testing.T) {
	rates := map[string]float64{"RUB": 1}
	s := &budget.Summary{
		Period: budget.Period{
			From: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		},
		Currencies: []budget.CurrencyGroup{{
			Currency:     "RUB",
			TotalIncome:  10000,
			TotalExpense: 25000,
			ByCategory: []budget.CategoryTotal{
				{CategoryName: "Еда", Total: 25000, Icon: "🍔"},
			},
		}},
	}
	out := formatSummary(s, nil, rates)
	if !strings.Contains(out, "Баланс: -15000") {
		t.Fatalf("negative balance not rendered: %s", out)
	}
}
