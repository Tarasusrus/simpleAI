package safetospend

import (
	"math"
	"testing"
	"time"

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

// Регулярные платежи становятся видимыми конвертами, а не скрытым вычетом
// (simpleAI-faeq.11 §1–§4): каждый — своей строкой с датой, а итог сходится с
// приходом до бата. Платёж ЗА границей периода в конверт не попадает
// (simpleAI-agz4) — он уходит в Upcoming и проверяется отдельным тестом.
func TestPlanEnvelope_RecurringBecomeVisibleShares(t *testing.T) {
	from := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	rec := []budget.RecurringPayment{
		{Name: "аренда", Type: "expense", Amount: 18000, Currency: "THB", Enabled: true,
			NextDate: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)}, // ЗА границей периода — в Upcoming
		{Name: "Кредит потребительский Сбербанк", Type: "expense", Amount: 28500, Currency: "RUB", Enabled: true,
			NextDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)},
		{Name: "уборка", Type: "expense", Amount: 2500, Currency: "THB", Enabled: true,
			NextDate: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)}, // день в день с концом — ВНУТРИ
		{Name: "кредитная карта", Type: "expense", Amount: 12500, Currency: "RUB", Enabled: false,
			NextDate: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)}, // выключен — не платёж
		{Name: "зарплата", Type: "income", Amount: 100000, Currency: "RUB", Enabled: true,
			NextDate: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)}, // доход — не расход
		{Name: "страховка", Type: "expense", Amount: 50000, Currency: "RUB", Enabled: true,
			NextDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}, // вне окна финансирования
	}
	rates := map[string]float64{"RUB": 1, "THB": 3.1}

	plan := PlanEnvelope(EnvelopePlanInput{
		IncomeTHB: 40967.74,
		// Сводная сумма обязательств из снимка обязана быть ПОДМЕНЕНА суммой
		// пофамильных конвертов, а не сложена с ней: это одни и те же деньги.
		Snapshot:  &budget.AdvisorSnapshot{UpcomingRecurring: 99999},
		Forecast:  []budget.CategoryForecast{{CategoryName: "Еда", Currency: "THB", ForecastAmount: 11571}},
		Rates:     rates,
		Days:      14,
		History:   map[string]int{"еда": 3},
		Recurring: rec,
		From:      from,
		To:        to,
	})

	fixed := FixedShares(plan.Shares)
	if len(fixed) != 2 {
		t.Fatalf("видимых платежей %d, ожидалось 2 (кредит и уборка): %+v", len(fixed), fixed)
	}
	if fixed[0].Name != "Кредит потребительский Сбербанк" || !eq(fixed[0].Allocated, roundKopecks(28500/3.1)) {
		t.Errorf("первым платежом ожидался кредит: %+v", fixed[0])
	}
	if fixed[1].Name != "уборка" {
		t.Errorf("платёж день в день с концом периода обязан войти в конверт: %+v", fixed)
	}
	if fixed[0].DueDate == nil || fixed[0].DueDate.Format("02.01") != "27.08" {
		t.Errorf("платёж потерял дату: %+v", fixed[0])
	}
	if !eq(plan.Result.RecurringTHB, 2500+roundKopecks(28500/3.1)) {
		t.Errorf("обязательства = %.2f — сводная сумма снимка не подменена суммой конвертов", plan.Result.RecurringTHB)
	}

	var total float64
	for _, sh := range plan.Shares {
		total += sh.Allocated
	}
	if math.Abs(total-40967.74) > 0.005 {
		t.Errorf("Σ конвертов = %.2f, приход = 40967.74 — итог обязан сходиться до бата", total)
	}
	checkEnvelopeInvariant(t, plan.Shares, 40967.74, 0)
}

// Малый приход не ломает раскладку: конверты урезаются, дневной лимит считается
// от них, а не от прихода (simpleAI-faeq.11 §5).
func TestPlanEnvelope_TinyIncomeStillPlans(t *testing.T) {
	plan := PlanEnvelope(EnvelopePlanInput{
		IncomeTHB: 3.2, // «пришло 10 рублей»
		Snapshot:  &budget.AdvisorSnapshot{},
		Forecast:  []budget.CategoryForecast{{CategoryName: "Еда", Currency: "THB", ForecastAmount: 11571}},
		Rates:     map[string]float64{"RUB": 1, "THB": 3.1},
		Days:      14,
		History:   map[string]int{"еда": 3},
		From:      time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
	})
	checkEnvelopeInvariant(t, plan.Shares, 3.2, 0)
	if DailyLimit(FlexibleTHB(plan.Shares), 14) < 0 {
		t.Error("дневной лимит ушёл в минус")
	}
}
