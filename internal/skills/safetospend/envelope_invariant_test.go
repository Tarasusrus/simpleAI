package safetospend

import (
	"math"
	"testing"

	"simpleAI/internal/budget"
)

// Регрессии, вскрытые прогоном на реплике 2026-08-24 (simpleAI-faeq.10).
//
// Общий инвариант, которого не хватало и который эти тесты держат:
//
//	Σ allocated + Σ carried_in  ≤  приход + реальный остаток прошлого периода
//
// «Реальный остаток» — остаток конверта, который ПРОЖИЛ хотя бы день. Конверт,
// закрытый в день своего создания, не прожил ничего: его allocated профинансирован
// тем же приходом, который сейчас раскладывается заново, и перенос этого allocated
// печатает деньги из воздуха.

// checkEnvelopeInvariant — сумма всех конвертов не превышает приход плюс реальный
// остаток прошлого периода. Отдельно от checkConvergence: тот проверяет равенство
// на Allocated внутри одной раскладки, этот — верхнюю границу на Allocated+CarriedIn
// между раскладками.
func checkEnvelopeInvariant(t *testing.T, shares []budget.EnvelopeShare, incomeTHB, prevRealRemainder float64) {
	t.Helper()
	var total float64
	for _, sh := range shares {
		total += sh.Allocated + sh.CarriedIn
	}
	limit := incomeTHB + prevRealRemainder
	if total > limit+0.005 {
		t.Errorf("Σ (allocated + carried_in) = %.2f > приход %.2f + реальный остаток %.2f = %.2f — деньги взялись из ниоткуда",
			total, incomeTHB, prevRealRemainder, limit)
	}
}

// Баг 1 (реплика 2026-08-24): повторная раскладка ТОГО ЖЕ прихода в тот же день
// наращивала carried_in 0 → 26681 → 53362 → 65335 THB.
//
// Механика: конверт, заведённый и закрытый в один день, имеет период нулевой
// длины (ADR-008 §10), факта по нему нет, и CarryOver переносил его остаток
// целиком — то есть allocated «накоплений», профинансированный тем же приходом,
// который в этот момент раскладывается заново.
//
// Правильное поведение: у вытесненного конверта переносится только ЕГО
// собственный carried_in (он пришёл из прошлого, реально прожитого периода),
// а его allocated не переносится вовсе.
func TestCarryOver_SupersededEnvelopeDoesNotMultiplyCarriedIn(t *testing.T) {
	prev := []budget.EnvelopeShare{
		{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5384, Position: 0},
		{Name: "Накопления", Kind: budget.ShareKindSave, Allocated: 26681, CarriedIn: 1500, Position: 1},
	}
	got := CarryOver(CarryInput{
		PrevShares:     prev,
		Rates:          testRates,
		NextShares:     nextShares(),
		PrevSuperseded: true,
	})

	save := shareByName(t, got, "Накопления")
	if !eq(save.CarriedIn, 1500) {
		t.Errorf("перенос с вытесненного конверта = %.2f, ожидалось 1500 (только его собственный carried_in; allocated 26681 профинансирован тем же приходом)", save.CarriedIn)
	}
}

// Повтор раскладки не должен ничего накапливать: три подряд «пришло 127000,
// разложи» в один день обязаны дать одинаковый результат.
func TestCarryOver_RepeatedSameDayPlanIsIdempotent(t *testing.T) {
	const incomeTHB = 40968

	plan := func() []budget.EnvelopeShare {
		return []budget.EnvelopeShare{
			{Name: "Еда", Kind: budget.ShareKindSpend, Allocated: 5384, Position: 0},
			{Name: "Накопления", Kind: budget.ShareKindSave, Allocated: incomeTHB - 5384, Position: 1},
		}
	}

	var prev []budget.EnvelopeShare
	for i := 1; i <= 3; i++ {
		cur := CarryOver(CarryInput{
			PrevShares:     prev,
			Rates:          testRates,
			NextShares:     plan(),
			PrevSuperseded: prev != nil, // прошлый конверт закрыт в день создания
		})
		// Прошлый период не прожил ни дня → реального остатка нет.
		checkEnvelopeInvariant(t, cur, incomeTHB, 0)
		if c := TotalCarriedIn(cur); !eq(c, 0) {
			t.Fatalf("раскладка #%d: перенесено %.2f, ожидалось 0 — повтор той же раскладки печатает деньги", i, c)
		}
		prev = cur
	}
}

// Прожитый период переносится как раньше: PrevSuperseded не должен превратиться
// в «перенос выключен всегда» — это стёрло бы реальные накопления.
func TestCarryOver_LivedEnvelopeStillCarriesRemaining(t *testing.T) {
	got := CarryOver(CarryInput{
		PrevShares: prevShares(),
		PrevSpent:  []budget.CategorySpentRow{{CategoryName: "еда", Currency: "THB", Amount: 5000}},
		Rates:      testRates,
		NextShares: nextShares(),
	})
	if save := shareByName(t, got, "Накопления"); !eq(save.CarriedIn, 11500) {
		t.Errorf("прожитый конверт перенёс %.2f, ожидалось 11500", save.CarriedIn)
	}
}

// Баг 3 (реплика 2026-08-24): «Транспорт 985» и «Здоровье 798» то появлялись
// отдельными конвертами, то молча уезжали в «прочее» и накопления.
//
// Механика: порог схлопывания мелочи считался как доля СВОБОДНЫХ денег
// (free * minShareFraction). free двигается от прихода к приходу (обязательства
// со сдвигом next_date), порог двигается вместе с ним, и категория с фиксированным
// прогнозом перепрыгивает планку туда-обратно.
//
// Правильное поведение: планка не зависит от прихода — она про то, «отслеживает ли
// человек такой конверт», а это свойство суммы, а не остатка после обязательств.
func TestAllocateShares_CategorySetDoesNotDependOnFree(t *testing.T) {
	fc := []budget.CategoryForecast{
		fcRUB("Еда", 11850),
		fcRUB("Транспорт", 2110),
		fcRUB("Здоровье", 1710),
	}
	history := map[string]int{"еда": 3, "транспорт": 3, "здоровье": 3}

	names := func(free float64) []string {
		shares, _ := allocateShares(free, fc, oneToOne, 14, nil, history)
		var out []string
		for _, sh := range shares {
			if sh.Allocated > 0 && sh.Kind == budget.ShareKindSpend {
				out = append(out, sh.Name)
			}
		}
		return out
	}

	// Тот же прогноз, разный остаток после обязательств.
	rich := names(41000)
	poor := names(23000)
	if len(rich) != len(poor) {
		t.Fatalf("набор конвертов зависит от прихода: при free=41000 %v, при free=23000 %v", rich, poor)
	}
	for i := range rich {
		if rich[i] != poor[i] {
			t.Fatalf("набор конвертов зависит от прихода: при free=41000 %v, при free=23000 %v", rich, poor)
		}
	}
	for _, want := range []string{"Транспорт", "Здоровье"} {
		found := false
		for _, n := range rich {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("категория %q с трёхмесячной историей не получила своего конверта: %v", want, rich)
		}
	}
}

// Инвариант на всей раскладке: конверты не обещают больше, чем есть.
func TestPlanEnvelope_InvariantHolds(t *testing.T) {
	in := EnvelopePlanInput{
		IncomeTHB: 40968,
		Snapshot:  &budget.AdvisorSnapshot{},
		Forecast:  []budget.CategoryForecast{fcRUB("Еда", 11850), fcRUB("Транспорт", 2110)},
		Rates:     oneToOne,
		Days:      14,
		History:   map[string]int{"еда": 3, "транспорт": 3},
	}
	plan := PlanEnvelope(in)
	checkEnvelopeInvariant(t, plan.Shares, in.IncomeTHB, 0)

	var total float64
	for _, sh := range plan.Shares {
		total += sh.Allocated
	}
	if math.Abs(total-in.IncomeTHB) > 0.005 {
		t.Errorf("Σ allocated = %.2f, приход = %.2f — итог обязан сходиться с приходом до бата", total, in.IncomeTHB)
	}
}
