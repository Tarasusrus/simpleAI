package safetospend

import (
	"math"
	"testing"

	"github.com/google/uuid"

	"simpleAI/internal/budget"
)

// Курс 1:1 — тест про формулу остатка, а не про конвертацию.
var testRates = map[string]float64{"THB": 1, "RUB": 1}

func testShares() []budget.EnvelopeShare {
	foodID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	return []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 20000, Position: 0,
			Categories: []budget.EnvelopeShareCategory{{CategoryID: &foodID, CategoryName: "еда"}}},
		{Name: "Транспорт", Kind: budget.ShareKindSpend, Allocated: 5000, Position: 1,
			Categories: []budget.EnvelopeShareCategory{{CategoryName: "транспорт"}}},
		{Name: "прочее", Kind: budget.ShareKindSpend, Allocated: 3000, Position: 2},
		{Name: "Накопления", Kind: budget.ShareKindSave, Allocated: 10000, CarriedIn: 1500, Position: 3},
	}
}

func byName(t *testing.T, items []ShareRemaining, name string) ShareRemaining {
	t.Helper()
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("доля %q не найдена в %+v", name, items)
	return ShareRemaining{}
}

func eq(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// Трата 3000 в категории доли «Еда» уменьшает её остаток ровно на 3000,
// соседние доли не двигаются.
func TestShareRemaining_SpendHitsOwnShareOnly(t *testing.T) {
	shares := testShares()
	base := computeShareRemaining(shares, nil, testRates)
	got := computeShareRemaining(shares, []budget.CategorySpentRow{
		{CategoryName: "Еда", Currency: "THB", Amount: 3000},
	}, testRates)

	food := byName(t, got, "Еда")
	if !eq(food.SpentTHB, 3000) {
		t.Errorf("факт по «Еда» = %.2f, ожидалось 3000", food.SpentTHB)
	}
	if !eq(food.Remaining, 20000-3000) {
		t.Errorf("остаток «Еда» = %.2f, ожидалось 17000", food.Remaining)
	}
	for _, name := range []string{"Транспорт", "прочее", "Накопления"} {
		if b, g := byName(t, base, name), byName(t, got, name); !eq(b.Remaining, g.Remaining) {
			t.Errorf("соседняя доля %q сдвинулась: %.2f → %.2f", name, b.Remaining, g.Remaining)
		}
	}
}

// Матчинг по category_id работает даже при другом имени категории (ADR-008 §6).
func TestShareRemaining_MatchByCategoryID(t *testing.T) {
	foodID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	got := computeShareRemaining(testShares(), []budget.CategorySpentRow{
		{CategoryID: &foodID, CategoryName: "Продукты", Currency: "THB", Amount: 1000},
	}, testRates)
	if f := byName(t, got, "Еда"); !eq(f.Remaining, 19000) {
		t.Errorf("остаток «Еда» = %.2f, ожидалось 19000", f.Remaining)
	}
}

// Трата в «Переводы» — движение денег: не трогает ни одну долю (ADR-008 §4).
func TestShareRemaining_TransfersTouchNothing(t *testing.T) {
	shares := testShares()
	base := computeShareRemaining(shares, nil, testRates)
	got := computeShareRemaining(shares, []budget.CategorySpentRow{
		{CategoryName: "Переводы", Currency: "THB", Amount: 50000},
	}, testRates)
	for i := range base {
		if !eq(base[i].Remaining, got[i].Remaining) {
			t.Errorf("доля %q сдвинулась от траты в «Переводы»: %.2f → %.2f",
				base[i].Name, base[i].Remaining, got[i].Remaining)
		}
	}
}

// Транзакция с recurring_id не прожигает долю: такие траты уже вычтены как
// обязательства (ADR-008 §5). Инвариант держится на СТЫКЕ со стором, поэтому
// проверяется двумя тестами: здесь — что фильтрующий источник вообще
// подключён к режиму конвертов (см. TestRunShares_UsesRecurringFreeSource в
// skill_test.go), а на реальном SQL —
// budget.TestSpentByCategoryExcludingRecurring_Integration.
//
// Чистая функция recurring отличить не может и не должна: у CategorySpentRow
// такого поля нет — сюда доезжает уже очищенный факт.

// Трата в переменной категории без своей доли уходит в приёмник «прочее».
func TestShareRemaining_UnknownCategoryGoesToFallback(t *testing.T) {
	got := computeShareRemaining(testShares(), []budget.CategorySpentRow{
		{CategoryName: "Развлечения", Currency: "THB", Amount: 700},
	}, testRates)
	if o := byName(t, got, "прочее"); !eq(o.Remaining, 3000-700) {
		t.Errorf("остаток «прочее» = %.2f, ожидалось 2300", o.Remaining)
	}
	if f := byName(t, got, "Еда"); !eq(f.Remaining, 20000) {
		t.Errorf("«Еда» не должна была двигаться: %.2f", f.Remaining)
	}
}

// Лимит доли = allocated + carried_in; пробитие видно флагом.
func TestShareRemaining_CarriedInAndOverspent(t *testing.T) {
	shares := []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 1000, CarriedIn: 500, Position: 0,
			Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}}},
		{Name: "прочее", Kind: budget.ShareKindSpend, Position: 1},
	}
	got := computeShareRemaining(shares, []budget.CategorySpentRow{
		{CategoryName: "Еда", Currency: "THB", Amount: 2000},
	}, testRates)
	f := byName(t, got, "Еда")
	if !eq(f.LimitTHB, 1500) {
		t.Errorf("лимит = %.2f, ожидалось 1500 (allocated + carried_in)", f.LimitTHB)
	}
	if !eq(f.Remaining, -500) || !f.Overspent() {
		t.Errorf("ожидалось пробитие −500, got %.2f overspent=%v", f.Remaining, f.Overspent())
	}
}

// Валюта факта конвертируется по курсу; неизвестная валюта не раздувает факт.
func TestShareRemaining_CurrencyConversion(t *testing.T) {
	rates := map[string]float64{"THB": 2.5, "RUB": 1}
	got := computeShareRemaining(testShares(), []budget.CategorySpentRow{
		{CategoryName: "Еда", Currency: "RUB", Amount: 2500},
		{CategoryName: "Еда", Currency: "XXX", Amount: 999999},
	}, rates)
	if f := byName(t, got, "Еда"); !eq(f.SpentTHB, 1000) {
		t.Errorf("факт = %.2f THB, ожидалось 1000 (2500 RUB / 2.5)", f.SpentTHB)
	}
}
