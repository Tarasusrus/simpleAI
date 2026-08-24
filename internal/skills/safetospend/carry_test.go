package safetospend

import (
	"testing"

	"simpleAI/internal/budget"
)

// prevShares — прошлая раскладка: расходная «Еда» и накопительная «Накопления»
// с уже перенесённой ранее суммой (carried_in участвует в остатке наравне с
// allocated, ADR-008 §8).
func prevShares() []budget.EnvelopeShare {
	return []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 20000, Position: 0,
			Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}}},
		{Name: "Накопления", Kind: budget.ShareKindSave, Allocated: 10000, CarriedIn: 1500, Position: 1},
	}
}

func nextShares() []budget.EnvelopeShare {
	return []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 18000, Position: 0,
			Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}}},
		{Name: "Накопления", Kind: budget.ShareKindSave, Allocated: 9000, Position: 1},
	}
}

func shareByName(t *testing.T, shares []budget.EnvelopeShare, name string) budget.EnvelopeShare {
	t.Helper()
	for _, sh := range shares {
		if sh.Name == name {
			return sh
		}
	}
	t.Fatalf("доля %q не найдена в %+v", name, shares)
	return budget.EnvelopeShare{}
}

// Накопительная доля переносит остаток (allocated + carried_in − факт), а
// расходная обнуляется: её остаток сгорает (ADR-008 §9).
func TestCarryOver_SaveCarriesSpendBurns(t *testing.T) {
	// Факт: 5000 по «еде». Остаток «Еда» = 15000 (сгорает),
	// «Накопления» = 10000 + 1500 − 0 = 11500 (переносится).
	got := CarryOver(CarryInput{
		PrevShares: prevShares(),
		PrevSpent:  []budget.CategorySpentRow{{CategoryName: "еда", Currency: "THB", Amount: 5000}},
		Rates:      testRates,
		NextShares: nextShares(),
	})

	if food := shareByName(t, got, "Еда"); food.CarriedIn != 0 {
		t.Errorf("расходная доля унесла остаток: carried_in = %.2f, ожидалось 0", food.CarriedIn)
	}
	save := shareByName(t, got, "Накопления")
	if !eq(save.CarriedIn, 11500) {
		t.Errorf("перенос накоплений = %.2f, ожидалось 11500 (10000 allocated + 1500 прошлый перенос)", save.CarriedIn)
	}
	if !eq(save.Allocated, 9000) {
		t.Errorf("перенос затёр allocated новой раскладки: %.2f, ожидалось 9000", save.Allocated)
	}
	if !eq(TotalCarriedIn(got), 11500) {
		t.Errorf("итого перенесено = %.2f, ожидалось 11500", TotalCarriedIn(got))
	}
}

// Save-доля, исчезнувшая из новой раскладки, создаётся принудительно:
// allocated=0, carried_in=остаток. Иначе накопленное пропало бы вместе с долей.
func TestCarryOver_VanishedSaveShareRecreated(t *testing.T) {
	next := []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 18000, Position: 0},
	}
	got := CarryOver(CarryInput{
		PrevShares: prevShares(),
		Rates:      testRates,
		NextShares: next,
	})

	save := shareByName(t, got, "Накопления")
	if save.Kind != budget.ShareKindSave {
		t.Errorf("восстановленная доля имеет kind %q, ожидалось %q", save.Kind, budget.ShareKindSave)
	}
	if save.Allocated != 0 {
		t.Errorf("восстановленной доле назначен лимит %.2f, ожидалось 0", save.Allocated)
	}
	if !eq(save.CarriedIn, 11500) {
		t.Errorf("накопления потерялись: carried_in = %.2f, ожидалось 11500", save.CarriedIn)
	}
	if got[len(got)-1].Position == next[0].Position {
		t.Errorf("восстановленная доля встала на занятую позицию %d", got[len(got)-1].Position)
	}
}

// Перенос ищет одноимённую долю по нормализованному имени: раскладка
// пересчитывается заново, регистр и позиция доли между конвертами не совпадают.
func TestCarryOver_MatchesByNormalizedName(t *testing.T) {
	next := []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 18000, Position: 0},
		{Name: "  накопления ", Kind: budget.ShareKindSave, Allocated: 9000, Position: 1},
	}
	got := CarryOver(CarryInput{PrevShares: prevShares(), Rates: testRates, NextShares: next})

	if len(got) != 2 {
		t.Fatalf("создана лишняя доля вместо переноса в одноимённую: %+v", got)
	}
	if !eq(got[1].CarriedIn, 11500) {
		t.Errorf("перенос по имени не сработал: carried_in = %.2f, ожидалось 11500", got[1].CarriedIn)
	}
}

// Пробитая накопительная доля не переносит долг: отрицательный carried_in молча
// съел бы часть нового прихода.
func TestCarryOver_NegativeRemainingNotCarried(t *testing.T) {
	prev := []budget.EnvelopeShare{
		{Name: "Накопления", Kind: budget.ShareKindSave, Allocated: 1000, Position: 0,
			Categories: []budget.EnvelopeShareCategory{{CategoryName: "еда"}}},
	}
	got := CarryOver(CarryInput{
		PrevShares: prev,
		PrevSpent:  []budget.CategorySpentRow{{CategoryName: "еда", Currency: "THB", Amount: 4000}},
		Rates:      testRates,
		NextShares: nextShares(),
	})
	if c := TotalCarriedIn(got); c != 0 {
		t.Errorf("перенесён долг: итого carried_in = %.2f, ожидалось 0", c)
	}
}

// Первый приход: переносить нечего, carried_in обнуляется явно — доля с
// ненулевым carried_in из чужого источника удвоила бы деньги.
func TestCarryOver_NoPreviousEnvelopeZeroes(t *testing.T) {
	next := nextShares()
	next[1].CarriedIn = 777
	got := CarryOver(CarryInput{Rates: testRates, NextShares: next})
	if c := TotalCarriedIn(got); c != 0 {
		t.Errorf("без прошлого конверта перенесено %.2f, ожидалось 0", c)
	}
	if next[1].CarriedIn != 777 {
		t.Errorf("CarryOver испортил входной срез вызывающего")
	}
}
