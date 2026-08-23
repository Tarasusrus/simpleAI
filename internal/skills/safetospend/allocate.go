package safetospend

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"simpleAI/internal/budget"
)

// Раскладка прихода по категорийным конвертам (ADR-008). Чистые функции: ни
// БД, ни LLM — числа не проходят через модель (инвариант ADR-007 §4).

// shareDraft — доля в процессе расчёта: имя ещё в отображаемом регистре, сумма
// ещё не округлена и может быть урезана. В budget.EnvelopeShare превращается
// только на последнем шаге, когда порядок и суммы окончательны.
type shareDraft struct {
	name       string   // отображаемый регистр («Еда»)
	amount     float64  // THB
	source     string   // budget.ShareSourceAuto | budget.ShareSourceOverride
	categories []string // нормализованные имена категорий доли
}

// normalizeShareName — ключ сопоставления долей, категорий и override'ов:
// обрезка пробелов + нижний регистр. Совпадает с нормализацией в budget
// (store.normalizeName), но та неэкспортируемая, а тащить ради этого зависимость
// на внутренности стора в чистую функцию незачем.
func normalizeShareName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// roundKopecks — округление суммы доли до копейки.
func roundKopecks(v float64) float64 {
	return math.Round(v*kopecksInUnit) / kopecksInUnit
}

// EnvelopePlanInput — вход раскладки прихода по конвертам. Всё, что читается из
// БД, собирает вызывающий (budget-скилл); здесь только счёт.
type EnvelopePlanInput struct {
	IncomeTHB  float64                   // приход, сконвертированный в THB
	Snapshot   *budget.AdvisorSnapshot   // обязательства за горизонт конверта
	PlannedTHB float64                   // ручные плановые траты
	Forecast   []budget.CategoryForecast // месячный прогноз по категориям
	Rates      map[string]float64
	Days       int                // длина горизонта конверта
	Overrides  map[string]float64 // ручные лимиты, THB, ключ нормализован
	History    map[string]int     // категория → полных месяцев данных
}

// EnvelopePlan — результат раскладки: детерминированные числа и доли.
// Инвариант ADR-008 §4: Σ Shares[i].Allocated = Result.FreeAfterObligations.
type EnvelopePlan struct {
	Result   Result
	Shares   []budget.EnvelopeShare
	Warnings []string
}

// PlanEnvelope — единственная точка входа раскладки для внешних пакетов.
// Экспортируется целиком, а не по кусочкам (computeSafeToSpend + allocateShares
// отдельно): порядок «сначала обязательства, потом делим ОСТАТОК» — инвариант
// ADR-008 §3, и разрешать вызывающему собрать его самому значит разрешить
// разложить сырой приход.
func PlanEnvelope(in EnvelopePlanInput) EnvelopePlan {
	snap := in.Snapshot
	if snap == nil {
		snap = &budget.AdvisorSnapshot{}
	}
	_, forecastTHB := buildForecastBreakdown(in.Forecast, in.Rates, in.Days)
	res := computeSafeToSpend(in.IncomeTHB, snap, in.PlannedTHB, forecastTHB)
	shares, warnings := allocateShares(
		res.FreeAfterObligations, in.Forecast, in.Rates, in.Days, in.Overrides, in.History)
	return EnvelopePlan{Result: res, Shares: shares, Warnings: warnings}
}

// SpendShares / SaveShares — разрез раскладки для показа: лимиты трат и то, что
// осталось свободным (уходит в накопления). Разрез по Kind, а не по имени:
// имя доли-накопления оператор может поменять, вид — нет.
func SpendShares(shares []budget.EnvelopeShare) []budget.EnvelopeShare {
	return filterShares(shares, budget.ShareKindSpend)
}

func SaveShares(shares []budget.EnvelopeShare) []budget.EnvelopeShare {
	return filterShares(shares, budget.ShareKindSave)
}

func filterShares(shares []budget.EnvelopeShare, kind string) []budget.EnvelopeShare {
	out := make([]budget.EnvelopeShare, 0, len(shares))
	for _, sh := range shares {
		if sh.Kind == kind {
			out = append(out, sh)
		}
	}
	return out
}

// allocateShares раскладывает free (Result.FreeAfterObligations, THB) по
// категорийным конвертам из истории трат. Чистая функция.
//
// Аргументы:
//   - fc, rates, days — прогноз трат; базовые лимиты считает существующая
//     buildForecastBreakdown (фикс-траты и движение денег уже отфильтрованы,
//     месячный прогноз пропорционирован на длину периода);
//   - overrides — ручные лимиты, ключ уже нормализован (Store.ListOverrides);
//   - history — сколько ПОЛНЫХ месяцев данных есть по категории.
//
// Правила (ADR-008):
//  1. один конверт = одна категория; лимит меньше minShareFraction от free
//     сливается в «прочее»;
//  2. категория с историей меньше minHistoryMonths месяцев собственного лимита
//     НЕ получает вовсе (сумму не выдумываем) — сама категория уходит в
//     «прочее», чтобы трате было куда падать, плюс warning. Из этого следует:
//     пустой history ⇒ ни одна категория не получит свой лимит, всё уйдёт в
//     накопления. Вызывающий обязан передать реальную глубину истории;
//  3. «прочее» создаётся ВСЕГДА, даже с нулевым лимитом: budget.ResolveShare
//     без fallback-доли возвращает nil, и трате в неизвестной категории
//     становится некуда падать;
//  4. override заменяет авто-лимит и помечает долю ShareSourceOverride;
//  5. Σ лимитов > free → пропорциональное усечение авто-долей; override режется
//     только если одних override'ов уже больше free;
//  6. непокрытый остаток уходит в долю «накопления» (ShareKindSave).
//
// Инвариант: Σ Allocated + свободно = free копейка в копейку. Держится тем, что
// доли трат округляются до копейки, а «накопления» берут ТОЧНЫЙ остаток
// free − Σ(остальные) без округления — накопленная ошибка округления оседает в
// них, а не размазывается по раскладке.
func allocateShares(
	free float64,
	fc []budget.CategoryForecast,
	rates map[string]float64,
	days int,
	overrides map[string]float64,
	history map[string]int,
) ([]budget.EnvelopeShare, []string) {
	drafts, warnings := buildDrafts(free, fc, rates, days, history, nil)
	drafts, warnings = applyOverrides(drafts, overrides, warnings)
	warnings = truncateToFree(drafts, free, warnings)

	return finalizeShares(drafts, free), warnings
}

// buildDrafts строит черновики долей из прогноза: одна категория — одна доля,
// мелочь и категории без истории — в «прочее». Последний черновик — всегда
// «прочее»: доля-приёмник обязана существовать даже с нулевым лимитом.
func buildDrafts(
	free float64,
	fc []budget.CategoryForecast,
	rates map[string]float64,
	days int,
	history map[string]int,
	warnings []string,
) ([]*shareDraft, []string) {
	items, _ := buildForecastBreakdown(fc, rates, days)
	threshold := free * minShareFraction

	fallback := &shareDraft{name: budget.FallbackShareName, source: budget.ShareSourceAuto}
	drafts := make([]*shareDraft, 0, len(items)+1)

	for _, it := range items {
		norm := normalizeShareName(it.Category)
		if historyMonths(history, it.Category) < minHistoryMonths {
			// Лимит не выдумываем: сумма по одному месяцу — не статистика.
			warnings = append(warnings, fmt.Sprintf("мало данных по «%s» — лимит не назначен", it.Category))
			fallback.categories = append(fallback.categories, norm)
			continue
		}
		if norm == budget.FallbackShareName || it.THB < threshold {
			// Мелочь сливаем в «прочее» вместе с её лимитом. Категория,
			// БУКВАЛЬНО названная «прочее», — туда же: отдельным черновиком она
			// стала бы вторым «прочее», и на сборке (finalizeShares) один из двух
			// затёр бы другой, а его лимит остался бы в Σ allocated и утёк из
			// раскладки — инвариант ADR-008 §4 не сошёлся бы.
			fallback.amount += it.THB
			fallback.categories = append(fallback.categories, norm)
			continue
		}
		if d := findDraft(drafts, norm); d != nil {
			// Регистровые дубли категорий («Еда» и «еда» — разные строки в
			// budget_category с разными id, ADR-008 §6) дают два черновика с
			// одним именем доли. Складываем, а не добавляем второй: две доли с
			// одним именем ломают и сборку, и UNIQUE(envelope_id,name).
			d.amount += it.THB
			continue
		}
		drafts = append(drafts, &shareDraft{
			name:       it.Category,
			amount:     it.THB,
			source:     budget.ShareSourceAuto,
			categories: []string{norm},
		})
	}

	return append(drafts, fallback), warnings
}

// historyMonths — глубина истории по категории. Ключ ищется в нормализованном
// виде и в исходном: вызывающие собирают карту по-разному, а цена промаха —
// молча потерянный лимит.
func historyMonths(history map[string]int, category string) int {
	if m, ok := history[normalizeShareName(category)]; ok {
		return m
	}
	return history[category]
}

// applyOverrides накладывает ручные лимиты поверх авто-раскладки. Override на
// имя, которого в раскладке нет, заводит новую долю: оператор мог назвать
// категорию, по которой прогноза ещё нет.
func applyOverrides(drafts []*shareDraft, overrides map[string]float64, warnings []string) ([]*shareDraft, []string) {
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names) // порядок обхода карты недетерминирован, а он влияет на раскладку

	for _, name := range names {
		amount := overrides[name]
		norm := normalizeShareName(name)
		if norm == savingsShareName {
			// «Накопления» — не лимит, а непокрытый остаток; задать его руками нельзя.
			warnings = append(warnings, fmt.Sprintf("«%s» считаются как остаток — ручной лимит не применяется", savingsShareName))
			continue
		}
		if amount < 0 {
			warnings = append(warnings, fmt.Sprintf("ручной лимит по «%s» отрицательный — игнорируем", name))
			continue
		}
		if d := findDraft(drafts, norm); d != nil {
			d.amount = amount
			d.source = budget.ShareSourceOverride
			continue
		}
		drafts = append(drafts, &shareDraft{
			name:       name,
			amount:     amount,
			source:     budget.ShareSourceOverride,
			categories: []string{norm},
		})
	}
	return drafts, warnings
}

func findDraft(drafts []*shareDraft, norm string) *shareDraft {
	for _, d := range drafts {
		if normalizeShareName(d.name) == norm {
			return d
		}
	}
	return nil
}

// truncateToFree ужимает лимиты в свободную сумму. Режутся авто-доли; override
// трогаем только если одних override'ов уже больше free — иначе ручное решение
// оператора молча переписывалось бы статистикой.
func truncateToFree(drafts []*shareDraft, free float64, warnings []string) []string {
	if free <= 0 {
		// Раскладывать нечего: обнуляем всё, доли остаются как маршруты трат.
		for _, d := range drafts {
			d.amount = 0
		}
		return append(warnings, "свободных денег нет — лимиты не назначены")
	}

	var sumAuto, sumOverride float64
	for _, d := range drafts {
		if d.source == budget.ShareSourceOverride {
			sumOverride += d.amount
		} else {
			sumAuto += d.amount
		}
	}
	if sumAuto+sumOverride <= free {
		return warnings
	}

	if sumOverride > free {
		k := free / sumOverride
		for _, d := range drafts {
			if d.source == budget.ShareSourceOverride {
				d.amount *= k
			} else {
				d.amount = 0
			}
		}
		return append(warnings, fmt.Sprintf(
			"ручные лимиты (%.0f) больше свободных %.0f — урезаны пропорционально", sumOverride, free))
	}

	var k float64
	if sumAuto > 0 {
		k = (free - sumOverride) / sumAuto
	}
	for _, d := range drafts {
		if d.source != budget.ShareSourceOverride {
			d.amount *= k
		}
	}
	return append(warnings, fmt.Sprintf(
		"лимиты (%.0f) не помещаются в свободные %.0f — авто-лимиты урезаны пропорционально", sumAuto+sumOverride, free))
}

// finalizeShares округляет суммы, добавляет «накопления» на непокрытый остаток
// и раскладывает доли по позициям: траты по убыванию лимита, затем «прочее»,
// последними — «накопления».
func finalizeShares(drafts []*shareDraft, free float64) []budget.EnvelopeShare {
	var allocated float64
	for _, d := range drafts {
		d.amount = roundKopecks(d.amount)
		allocated += d.amount
	}
	// Округление вверх могло выбить сумму за free на несколько копеек — снимаем
	// их с самой крупной доли, иначе «накопления» ушли бы в минус. Границу
	// держим на копейке с доли: перебор больше этого — не ошибка округления, а
	// несработавшее усечение, и заминать его здесь нельзя (иначе сумма сойдётся,
	// а раскладка будет врать).
	if excess := allocated - free; free > 0 && excess > 0 && excess <= float64(len(drafts))/kopecksInUnit {
		if d := largestDraft(drafts); d != nil {
			d.amount = roundKopecks(d.amount - excess)
			allocated = 0
			for _, d := range drafts {
				allocated += d.amount
			}
		}
	}

	savings := free - allocated
	if free <= 0 || savings < 0 {
		savings = 0
	}

	spend := make([]*shareDraft, 0, len(drafts))
	var fallback *shareDraft
	for _, d := range drafts {
		if normalizeShareName(d.name) == budget.FallbackShareName {
			fallback = d
			continue
		}
		spend = append(spend, d)
	}
	sort.SliceStable(spend, func(i, j int) bool {
		if spend[i].amount != spend[j].amount {
			return spend[i].amount > spend[j].amount
		}
		return spend[i].name < spend[j].name
	})
	if fallback != nil {
		spend = append(spend, fallback)
	}
	spend = append(spend, &shareDraft{
		name:   savingsShareName,
		amount: savings,
		source: budget.ShareSourceAuto,
	})

	shares := make([]budget.EnvelopeShare, 0, len(spend))
	for i, d := range spend {
		kind := budget.ShareKindSpend
		if d.name == savingsShareName {
			kind = budget.ShareKindSave
		}
		cats := make([]budget.EnvelopeShareCategory, 0, len(d.categories))
		for _, c := range d.categories {
			cats = append(cats, budget.EnvelopeShareCategory{CategoryName: c})
		}
		shares = append(shares, budget.EnvelopeShare{
			Name:       d.name,
			Kind:       kind,
			Allocated:  d.amount,
			Source:     d.source,
			Position:   i,
			Categories: cats,
		})
	}
	return shares
}

func largestDraft(drafts []*shareDraft) *shareDraft {
	var best *shareDraft
	for _, d := range drafts {
		if best == nil || d.amount > best.amount {
			best = d
		}
	}
	return best
}
