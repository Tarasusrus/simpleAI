// Package safetospend — read-only LLM-reasoning skill (ADR-002, ADR-007):
// «пришло X, сколько свободно на период?». Приход берётся ИЗ СООБЩЕНИЯ и НЕ
// пишется в ledger. Свободный остаток считается ДЕТЕРМИНИРОВАННО в Go
// (computeSafeToSpend), LLM только нарративит раскладку.
package safetospend

import (
	"math"
	"sort"

	"simpleAI/internal/budget"
)

// CategorySpend — ожидаемая трата по одной потребительской категории за период.
type CategorySpend struct {
	Category string
	THB      float64
}

// buildForecastBreakdown агрегирует прогноз по ПОТРЕБИТЕЛЬСКИМ категориям
// (движение денег исключено), пропорционально длине периода (days/30), и
// сортирует по убыванию. Чистая функция — отвечает на «на что уйдут деньги».
// Возвращает разбивку и итог.
func buildForecastBreakdown(fc []budget.CategoryForecast, rates map[string]float64, days int) ([]CategorySpend, float64) {
	merged := map[string]float64{}
	for _, f := range fc {
		if !budget.IsVariableDailyExpense(f.CategoryName) {
			continue // фикс (аренда/подписки) и движение денег — не «ежедневные»
		}
		thb, ok := budget.ToTHB(f.ForecastAmount, f.Currency, rates)
		if !ok {
			continue
		}
		merged[f.CategoryName] += thb * float64(days) / prorationBaseDays
	}
	items := make([]CategorySpend, 0, len(merged))
	var total float64
	for cat, thb := range merged {
		items = append(items, CategorySpend{Category: cat, THB: thb})
		total += thb
	}
	sort.Slice(items, func(i, j int) bool { return items[i].THB > items[j].THB })
	return items, total
}

// classifyIncome детерминированно (ADR-007 §7): «регулярный», если сумма близка
// (±regularIncomeTolerance) к известному регулярному доходу; иначе «разовый».
func classifyIncome(incomeTHB, recurringIncomeTHB float64) string {
	if recurringIncomeTHB > 0 && math.Abs(incomeTHB-recurringIncomeTHB)/recurringIncomeTHB < regularIncomeTolerance {
		return "регулярный"
	}
	return "разовый"
}

// Result — детерминированный расчёт safe-to-spend, все суммы в THB.
//
// Формула (ADR-007 §4, одно слагаемое — один источник):
//
//	FreeAfterObligations = Income − (Recurring + Debt)
//	RealisticFree        = FreeAfterObligations − ForecastSpend
//
// Уже потраченное за период НЕ вычитается из прихода (это прошлые деньги, не из
// нового прихода); ожидаемые будущие траты моделируются ForecastSpend.
type Result struct {
	IncomeTHB            float64
	RecurringTHB         float64
	DebtTHB              float64
	PlannedTHB           float64 // ручные разовые плановые траты
	ObligationsTHB       float64 // Recurring + Debt + Planned (всё известное)
	ForecastSpendTHB     float64 // ожидаемые бытовые траты по статистике (справочно)
	FreeAfterObligations float64 // Income − Obligations
	RealisticFree        float64 // FreeAfterObligations − ForecastSpend
	Classification       string  // «регулярный» | «разовый»
}

// computeSafeToSpend — чистая функция. Числа НЕ проходят через LLM.
// plannedTHB — ручные плановые траты; forecastSpendTHB — очищенный прогноз быта.
func computeSafeToSpend(incomeTHB float64, snap *budget.AdvisorSnapshot, plannedTHB, forecastSpendTHB float64) Result {
	obligations := snap.UpcomingRecurring + snap.ActiveDebtDue + plannedTHB
	freeAfter := incomeTHB - obligations
	return Result{
		IncomeTHB:            incomeTHB,
		RecurringTHB:         snap.UpcomingRecurring,
		DebtTHB:              snap.ActiveDebtDue,
		PlannedTHB:           plannedTHB,
		ObligationsTHB:       obligations,
		ForecastSpendTHB:     forecastSpendTHB,
		FreeAfterObligations: freeAfter,
		RealisticFree:        freeAfter - forecastSpendTHB,
		Classification:       classifyIncome(incomeTHB, snap.UpcomingRecurringIncome),
	}
}

// RemainingResult — производный остаток живого конверта (ADR-007 H3/T5), THB.
// Остаток НЕ хранится: RemainingTHB считается из фактических данных, поэтому
// недвойного учёта — каждое слагаемое из одного источника (ADR §4):
//
//	Remaining = Income − Recurring − Debt − Planned − ActualSpent
type RemainingResult struct {
	IncomeTHB      float64
	RecurringTHB   float64
	DebtTHB        float64
	PlannedTHB     float64
	ActualSpentTHB float64 // фактически потрачено (дискреционное) с начала конверта
	RemainingTHB   float64
}

// computeRemaining — чистая функция (числа не через LLM).
func computeRemaining(incomeTHB float64, snap *budget.AdvisorSnapshot, plannedTHB, actualSpentTHB float64) RemainingResult {
	rem := incomeTHB - snap.UpcomingRecurring - snap.ActiveDebtDue - plannedTHB - actualSpentTHB
	return RemainingResult{
		IncomeTHB:      incomeTHB,
		RecurringTHB:   snap.UpcomingRecurring,
		DebtTHB:        snap.ActiveDebtDue,
		PlannedTHB:     plannedTHB,
		ActualSpentTHB: actualSpentTHB,
		RemainingTHB:   rem,
	}
}

// consumptionSpentTHB — сумма фактических трат по потребительским категориям
// (движение денег исключено, чтобы не задваивать обязательства; ADR-007 §4).
func consumptionSpentTHB(spentByCategory map[string]float64) float64 {
	var total float64
	for cat, thb := range spentByCategory {
		if budget.IsConsumptionCategory(cat) {
			total += thb
		}
	}
	return total
}
