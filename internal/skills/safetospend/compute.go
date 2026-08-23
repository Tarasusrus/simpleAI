// Package safetospend — read-only LLM-reasoning skill (ADR-002, ADR-007):
// «пришло X, сколько свободно на период?». Приход берётся ИЗ СООБЩЕНИЯ и НЕ
// пишется в ledger. Свободный остаток считается ДЕТЕРМИНИРОВАННО в Go
// (computeSafeToSpend), LLM только нарративит раскладку.
package safetospend

import (
	"fmt"
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

// ShareRemaining — производный остаток ОДНОЙ доли конверта (ADR-008 §8):
//
//	Remaining = Allocated + CarriedIn − факт по категориям доли за период
//
// Остаток не хранится нигде: у каждого слагаемого ровно один источник.
type ShareRemaining struct {
	Name      string
	Kind      string  // budget.ShareKindSpend | budget.ShareKindSave
	Source    string  // budget.ShareSourceAuto | budget.ShareSourceOverride
	Allocated float64 // THB
	CarriedIn float64 // THB, перенос с прошлого конверта
	LimitTHB  float64 // Allocated + CarriedIn — то, что показываем как «лимит»
	SpentTHB  float64 // факт по категориям доли, БЕЗ recurring
	Remaining float64 // LimitTHB − SpentTHB, может быть отрицательным (доля пробита)
}

// Overspent — доля пробита: потрачено больше лимита.
func (s ShareRemaining) Overspent() bool { return s.Remaining < 0 }

// computeShareRemaining — чистая функция остатка по каждой доле (ADR-008 §8,
// §11: числа не проходят через LLM).
//
// spentByCategory — сырой факт расходов за период конверта, уже БЕЗ транзакций
// с recurring_id (их отсекает SpentByCategoryExcludingRecurring; учитывать их
// здесь значило бы посчитать обязательства дважды, ADR-008 §5).
//
// В факт доли попадают только ПЕРЕМЕННЫЕ ежедневные траты
// (budget.IsVariableDailyExpense) — по ним и строится раскладка. Фиксированные
// категории и движение денег («Переводы», «Кредит») не трогают ни одну долю:
// они уже учтены в обязательствах и показываются строкой «вне конвертов»
// (ADR-008 §4). Классификатор тот же, что у прогноза и раскладки, — один
// доменный источник.
//
// Матчинг траты к доле — budget.ResolveShare (id, затем имя, затем fallback
// «прочее»), чтобы факт не терялся молча (ADR-008 §6). Категория без своей доли
// уменьшает долю-приёмник «прочее».
//
// Порядок долей на выходе повторяет порядок входа: он задан Position раскладки.
func computeShareRemaining(shares []budget.EnvelopeShare, spentByCategory []budget.CategorySpentRow, rates map[string]float64) []ShareRemaining {
	if len(shares) == 0 {
		return nil
	}
	spent := make([]float64, len(shares))
	idx := map[string]int{} // ключ доли (position+имя) → индекс в shares
	for i := range shares {
		idx[shareKey(shares[i])] = i
	}

	for _, row := range spentByCategory {
		if !budget.IsVariableDailyExpense(row.CategoryName) {
			continue // фикс и движение денег — «вне конвертов», не факт доли
		}
		thb, ok := budget.ToTHB(row.Amount, row.Currency, rates)
		if !ok {
			continue // курса нет — раздувать факт выдуманным числом нельзя
		}
		sh := budget.ResolveShare(shares, row.CategoryID, row.CategoryName)
		if sh == nil {
			continue // нет даже fallback-доли — падать некуда (ADR-008 §6)
		}
		if i, ok := idx[shareKey(*sh)]; ok {
			spent[i] += thb
		}
	}

	out := make([]ShareRemaining, 0, len(shares))
	for i, sh := range shares {
		limit := sh.Allocated + sh.CarriedIn
		out = append(out, ShareRemaining{
			Name:      sh.Name,
			Kind:      sh.Kind,
			Source:    sh.Source,
			Allocated: sh.Allocated,
			CarriedIn: sh.CarriedIn,
			LimitTHB:  limit,
			SpentTHB:  spent[i],
			Remaining: limit - spent[i],
		})
	}
	return out
}

// shareKey — идентификатор доли внутри одной раскладки. ID использовать нельзя:
// у долей, посчитанных PlanEnvelope и ещё не сохранённых, он нулевой, и все
// доли слиплись бы в одну. Имя уникально в пределах конверта (UNIQUE(envelope_id,
// name), ADR-008 §9), position добавлен как страховка от рассинхронизации.
func shareKey(sh budget.EnvelopeShare) string {
	return fmt.Sprintf("%d|%s", sh.Position, normalizeShareName(sh.Name))
}
