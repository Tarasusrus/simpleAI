package safetospend

import (
	"math"
	"testing"

	"simpleAI/internal/budget"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestComputeSafeToSpend(t *testing.T) {
	snap := &budget.AdvisorSnapshot{
		UpcomingRecurring: 20000,
		ActiveDebtDue:     5000,
	}
	// income 100000, recurring 20000, debt 5000, planned 10000, forecast 30000
	got := computeSafeToSpend(100000, snap, 10000, 30000)

	if !approx(got.ObligationsTHB, 35000) {
		t.Errorf("Obligations: want 35000 (20000+5000+10000), got %.2f", got.ObligationsTHB)
	}
	if !approx(got.FreeAfterObligations, 65000) {
		t.Errorf("FreeAfterObligations: want 65000 (100000-35000), got %.2f", got.FreeAfterObligations)
	}
	if !approx(got.RealisticFree, 35000) {
		t.Errorf("RealisticFree: want 35000 (65000-30000), got %.2f", got.RealisticFree)
	}
}

// TestAcceptance_127k_to_27800 (ADR-007 задача 6, эталон bd l960): детерминир.
// проверка математики без LLM и БД. Приход 127000₽, известные обязательства =
// Сбер-кредит 28000 (recurring) + разовые плановые 71200 (кредитка 36400 + виза
// 15600 + электричество 6720 + пылесос 5200 + корм 3380 + прививки 3900) =
// 99200 → свободно 27800₽. Единицы: THB==₽ (курс 1:1) для прямого сравнения.
func TestAcceptance_127k_to_27800(t *testing.T) {
	snap := &budget.AdvisorSnapshot{UpcomingRecurring: 28000, ActiveDebtDue: 0}
	const planned = 36400 + 15600 + 6720 + 5200 + 3380 + 3900 // = 71200
	got := computeSafeToSpend(127000, snap, planned, 0)

	if !approx(got.ObligationsTHB, 99200) {
		t.Errorf("известные обязательства: want 99200, got %.0f", got.ObligationsTHB)
	}
	if !approx(got.FreeAfterObligations, 27800) {
		t.Errorf("свободно после известных: want 27800 (эталон), got %.0f", got.FreeAfterObligations)
	}
}

func TestBuildForecastBreakdown(t *testing.T) {
	rates := map[string]float64{"RUB": 1.0, "THB": 1.0} // 1:1 для прямого сравнения
	fc := []budget.CategoryForecast{
		{CategoryName: "Транспорт", Currency: "RUB", ForecastAmount: 6000},
		{CategoryName: "Еда", Currency: "RUB", ForecastAmount: 30000},
		{CategoryName: "Переводы", Currency: "RUB", ForecastAmount: 90000}, // движение денег → исключить
		{CategoryName: "Жильё", Currency: "RUB", ForecastAmount: 50000},    // фикс → исключить из «ежедневных»
	}
	// days=30 (без пропорции): Переводы и Жильё исключены, сортировка desc.
	items, total := buildForecastBreakdown(fc, rates, 30)
	if !approx(total, 36000) {
		t.Errorf("total: want 36000 (Переводы+Жильё исключены), got %.0f", total)
	}
	if len(items) != 2 || items[0].Category != "Еда" {
		t.Errorf("ожидалась сортировка desc с Еда первой, got %+v", items)
	}
	// days=15 → пропорция вдвое.
	if _, half := buildForecastBreakdown(fc, rates, 15); !approx(half, 18000) {
		t.Errorf("пропорция 15 дней: want 18000, got %.0f", half)
	}
}

func TestComputeRemaining(t *testing.T) {
	snap := &budget.AdvisorSnapshot{UpcomingRecurring: 10000, ActiveDebtDue: 5000}
	// 100000 − 10000 − 5000 − 20000(planned) − 15000(spent) = 50000
	got := computeRemaining(100000, snap, 20000, 15000)
	if !approx(got.RemainingTHB, 50000) {
		t.Errorf("Remaining: want 50000, got %.0f", got.RemainingTHB)
	}
}

func TestDiscretionarySpentTHB(t *testing.T) {
	m := map[string]float64{"Еда": 1000, "Переводы": 5000, "Транспорт": 500, "кредит": 9000}
	// discretionary = Еда + Транспорт = 1500 (Переводы/кредит исключены)
	if !approx(consumptionSpentTHB(m), 1500) {
		t.Errorf("discretionarySpent: want 1500, got %.0f", consumptionSpentTHB(m))
	}
}

func TestClassifyIncome(t *testing.T) {
	// recurring income известен = 100000; приход 105000 (±5%) → регулярный.
	if got := classifyIncome(105000, 100000); got != "регулярный" {
		t.Errorf("105000 vs recurring 100000: want регулярный, got %s", got)
	}
	// приход 127000 при recurring 100000 (+27%) → разовый.
	if got := classifyIncome(127000, 100000); got != "разовый" {
		t.Errorf("127000 vs recurring 100000: want разовый, got %s", got)
	}
	// нет данных о recurring income → разовый.
	if got := classifyIncome(50000, 0); got != "разовый" {
		t.Errorf("no recurring: want разовый, got %s", got)
	}
}

func TestIsDiscretionary(t *testing.T) {
	for _, c := range []string{"Переводы", "кредит", "долг"} {
		if budget.IsConsumptionCategory(c) {
			t.Errorf("%q должна быть non-discretionary", c)
		}
	}
	for _, c := range []string{"Еда", "Транспорт", "Развлечения"} {
		if !budget.IsConsumptionCategory(c) {
			t.Errorf("%q должна быть discretionary", c)
		}
	}
}

func TestToTHB(t *testing.T) {
	rates := map[string]float64{"RUB": 1.0, "THB": 2.6}
	// 2600 RUB / 2.6 = 1000 THB
	got, ok := budget.ToTHB(2600, "RUB", rates)
	if !ok || !approx(got, 1000) {
		t.Errorf("budget.ToTHB(2600 RUB): want 1000, got %.2f ok=%v", got, ok)
	}
	if _, ok := budget.ToTHB(100, "XYZ", rates); ok {
		t.Error("unknown currency должна давать ok=false")
	}
	if _, ok := budget.ToTHB(100, "RUB", map[string]float64{"RUB": 1}); ok {
		t.Error("отсутствие THB-курса должно давать ok=false")
	}
}
