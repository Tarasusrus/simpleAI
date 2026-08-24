package safetospend

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

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

// normalizeShareName — ключ сопоставления долей, категорий и override'ов.
// Своей нормализации не имеет: делегирует budget.NormalizeName, которым стор
// пишет и читает те же ключи. Ключ раскладки и ключ стора обязаны совпадать —
// собственная копия правила развела бы их на первой же правке.
func normalizeShareName(s string) string {
	return budget.NormalizeName(s)
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
	// Recurring — ВСЕ регулярные платежи чата. Каждый попавший в окно
	// финансирования становится отдельным видимым конвертом kind='fixed'.
	// Заменяет собой Snapshot.UpcomingRecurring: сводная сумма обязательств и
	// пофамильные конверты — одни и те же деньги, и складывать их значит
	// вычесть обязательства дважды.
	Recurring []budget.RecurringPayment
	// From, To — границы периода конверта, ОБЕ включительно. Регулярный платёж
	// финансируется этим приходом тогда и только тогда, когда его дата лежит
	// внутри [From, To]; всё, что дальше, — забота следующего конверта.
	From, To time.Time
}

// EnvelopePlan — результат раскладки: детерминированные числа и доли.
// Инвариант ADR-008 §4: Σ Shares[i].Allocated = Result.FreeAfterObligations.
type EnvelopePlan struct {
	Result   Result
	Shares   []budget.EnvelopeShare
	Warnings []string
	// Upcoming — регулярные платежи, попадающие уже в СЛЕДУЮЩИЙ период. Деньги
	// на них этим приходом не откладываются, но пропасть из виду они не имеют
	// права: оператор, не увидев аренду, потратит её на еду.
	Upcoming []budget.EnvelopeShare
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
	fixed, upcoming, warnings := fixedShares(in.Recurring, in.Rates, in.From, in.To)

	// Обязательства снимаются с прихода РОВНО ОДИН раз — суммой пофамильных
	// fixed-конвертов. Snapshot.UpcomingRecurring здесь подменяется ею, а не
	// складывается: это одни и те же платежи. Границы у них теперь совпадают
	// (обе — период конверта), но подмена всё равно обязательна: снимок режет
	// ещё и долги, а fixed-доли собраны только из recurring.
	obligations := *snap
	obligations.UpcomingRecurring = sumAllocatedShares(fixed)

	_, forecastTHB := buildForecastBreakdown(in.Forecast, in.Rates, in.Days)
	res := computeSafeToSpend(in.IncomeTHB, &obligations, in.PlannedTHB, forecastTHB)

	flexible, allocWarn := allocateShares(
		res.FreeAfterObligations, in.Forecast, in.Rates, in.Days, in.Overrides, in.History)
	warnings = append(warnings, allocWarn...)

	shares := make([]budget.EnvelopeShare, 0, len(fixed)+len(flexible))
	shares = append(shares, fixed...)
	shares = append(shares, flexible...)
	for i := range shares {
		shares[i].Position = i
	}
	return EnvelopePlan{Result: res, Shares: shares, Warnings: warnings, Upcoming: upcoming}
}

// fixedShares превращает регулярные платежи в видимые конверты (simpleAI-faeq.11)
// и отдельно возвращает те, что придутся уже на следующий период.
//
// Почему платежи стали конвертами, а не остались скрытым вычетом: оператор
// отверг деление трат на «обязательные» и «на жизнь» — «есть мне тоже надо, или
// ты считаешь что еда необязательна?». Ресёрч конвертного бюджетирования на его
// стороне: у YNAB обязательные платежи — первые КАТЕГОРИИ плана, финансируемые
// первыми, а не вычет до раскладки. Скрытый вычет ещё и ломает сходимость: в
// ответе бота приход не сходился визуально, 12 332 ฿ исчезали без строки.
//
// Окно финансирования — РОВНО период конверта [from, to], обе границы
// включительно. Раньше здесь стоял месяц вперёд (sinking fund: «аренда 10.09
// при периоде до 06.09 всё равно должна быть отложена сейчас»), и это отменено
// оператором 24.08.2026: приход у него ДВАЖДЫ в месяц, период конверта совпадает
// с ритмом прихода, и платёж следующего периода профинансируется приходом того
// периода. Месячное окно при этом запирало 18 671 ฿ из 128 000 под платежи,
// до которых ещё придут деньги, и оператор читал свободный остаток как заниженный.
// Не возвращать 31 день обратно, не переспросив: на другом ритме прихода
// (раз в месяц) верным будет ровно прежнее поведение.
//
// Отсечённые платежи молча не пропадают — они уходят вторым результатом и
// печатаются строкой «впереди»: не увидев аренду, оператор потратит её на еду.
//
// Категорий у fixed-доли нет: её факт — сам recurring-платёж, а транзакции с
// recurring_id в факт долей не попадают (ADR-008 §5). Дай мы ей категорию,
// платёж посчитался бы и обязательством, и тратой конверта.
//
// Порядок — по убыванию суммы: колонка чисел читается сверху вниз, и крупное
// обязательство должно быть первым (ADR-008 §11 — числа считает Go, не LLM).
func fixedShares(rec []budget.RecurringPayment, rates map[string]float64, from, to time.Time) (fixed, upcoming []budget.EnvelopeShare, warnings []string) {
	if len(rec) == 0 {
		return nil, nil, nil
	}
	// Обе границы включительно: платёж день в день с концом периода — ещё этот
	// период. Полночь следующих суток берётся именно для этого.
	periodStart := dayStart(from)
	periodEnd := dayStart(to).AddDate(0, 0, 1)
	// «Впереди» заглядывает РОВНО на один следующий период, а не на фиксированные
	// 31 день. Блок озаглавлен «из следующего прихода», а приход у оператора
	// дважды в месяц: месячное окно затянуло бы туда платежи периода ПОСЛЕ
	// следующего и подписало бы их деньгами, которых к тому моменту ещё нет.
	// Длина окна равна длине самого периода — так оно следует за горизонтом,
	// а не за календарным месяцем.
	lookaheadEnd := periodEnd.AddDate(0, 0, int(periodEnd.Sub(periodStart).Hours()/24))

	fixed = make([]budget.EnvelopeShare, 0, len(rec))
	for _, r := range rec {
		if !r.Enabled || r.Type != "expense" {
			continue
		}
		if r.NextDate.Before(periodStart) || !r.NextDate.Before(lookaheadEnd) {
			continue
		}
		thb, ok := budget.ToTHB(r.Amount, r.Currency, rates)
		if !ok {
			// Курса нет — выдумывать сумму нельзя, но и молчать нельзя: платёж
			// исчезнет из плана, а деньги на него всё равно уйдут.
			warnings = append(warnings, fmt.Sprintf("«%s» — нет курса %s, платёж не заложен", r.Name, r.Currency))
			continue
		}
		due := r.NextDate
		share := budget.EnvelopeShare{
			Name:      r.Name,
			Kind:      budget.ShareKindFixed,
			Allocated: roundKopecks(thb),
			Source:    budget.ShareSourceAuto,
			DueDate:   &due,
		}
		if r.NextDate.Before(periodEnd) {
			fixed = append(fixed, share)
			continue
		}
		upcoming = append(upcoming, share)
	}
	sortSharesByAmount(fixed)
	// «Впереди» сортируется по ДАТЕ, а не по сумме: это список ближайших
	// платежей, и первым читается тот, что наступит раньше.
	sort.SliceStable(upcoming, func(i, j int) bool {
		return upcoming[i].DueDate.Before(*upcoming[j].DueDate)
	})
	return fixed, upcoming, warnings
}

// sortSharesByAmount — по убыванию суммы, при равенстве по имени (устойчиво).
func sortSharesByAmount(out []budget.EnvelopeShare) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Allocated != out[j].Allocated {
			return out[i].Allocated > out[j].Allocated
		}
		return out[i].Name < out[j].Name
	})
}

// dayStart — начало суток: next_date хранится DATE (полночь UTC), а from
// приходит с временем. Без обрезки платёж «сегодня» отсекался бы как прошлый.
func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func sumAllocatedShares(shares []budget.EnvelopeShare) float64 {
	var total float64
	for _, sh := range shares {
		total += sh.Allocated
	}
	return total
}

// FixedShares — конверты под регулярные платежи: заперты, тратить нельзя.
func FixedShares(shares []budget.EnvelopeShare) []budget.EnvelopeShare {
	return filterShares(shares, budget.ShareKindFixed)
}

// DailyLimit — сколько можно тратить в день: гибкие конверты, делённые на
// оставшиеся дни (simpleAI-faeq.11 §5).
//
// Приход в формуле НЕ участвует. Это не украшение: лимит обязан работать и при
// приходе в 10 рублей, и при нулевом — конверты уже наполнены прошлым приходом,
// и вопрос «сколько можно сегодня» от факта нового прихода не зависит.
//
// В числитель входят только гибкие доли (kind='spend'): fixed заперты под
// конкретный платёж, save — накопления. Тратить из них «в день» нельзя.
//
// Пересчитывается при каждом показе от ОСТАТКА долей, поэтому потраченные в
// первый день 2000 ฿ автоматически опускают планку на остаток дней. Это
// зеркало, а не запрет.
func DailyLimit(flexibleTHB float64, daysLeft int) float64 {
	if daysLeft < 1 {
		daysLeft = 1
	}
	if flexibleTHB <= 0 {
		return 0
	}
	return flexibleTHB / float64(daysLeft)
}

// FlexibleTHB — числитель дневного лимита на момент раскладки: лимиты гибких
// долей вместе с перенесённым остатком.
func FlexibleTHB(shares []budget.EnvelopeShare) float64 {
	var total float64
	for _, sh := range shares {
		if sh.Kind == budget.ShareKindSpend {
			total += sh.Allocated + sh.CarriedIn
		}
	}
	return total
}

// FlexibleRemainingTHB — тот же числитель, но по ОСТАТКУ долей: им считается
// дневной лимит после трат. Отдельная функция, а не флаг: у «сколько заложено»
// и «сколько осталось» разные источники, и путать их нельзя.
func FlexibleRemainingTHB(items []ShareRemaining) float64 {
	var total float64
	for _, it := range items {
		if it.Kind == budget.ShareKindSpend {
			total += it.Remaining
		}
	}
	return total
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
//  1. один конверт = одна категория; лимит меньше minShareMonthlyTHB (в пересчёте
//     на длину периода) сливается в «прочее»;
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
	// Планка мелочи — абсолютная, пропорционально длине периода. От free она
	// зависеть не имеет права: см. minShareMonthlyTHB (simpleAI-faeq.10, баг 3).
	threshold := minShareMonthlyTHB * float64(days) / prorationBaseDays

	fallback := &shareDraft{name: budget.FallbackShareName, source: budget.ShareSourceAuto}
	drafts := make([]*shareDraft, 0, len(items)+1)
	var lowData []string

	for _, it := range items {
		norm := normalizeShareName(it.Category)
		if historyMonths(history, it.Category) < minHistoryMonths {
			// Лимит не выдумываем: сумма по одному месяцу — не статистика.
			lowData = append(lowData, it.Category)
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

	return append(drafts, fallback), append(warnings, lowDataWarning(lowData)...)
}

// lowDataWarning сворачивает «мало данных» в ОДНУ строку.
//
// По строке на категорию было ровно тем антипаттерном, который ресёрч
// конвертного бюджетирования называет главной причиной развала метода: живой
// прогон дал одиннадцать предупреждений подряд — «ресторан», «корм», «связь»,
// «цветы», «посуда»… Одиннадцать строк шума прячут сам ответ, а действие у
// оператора на все них одно: подождать, пока накопится статистика, либо
// назначить лимит словами. Список обрезается — читать хвост из мелких
// категорий человек всё равно не станет.
func lowDataWarning(cats []string) []string {
	if len(cats) == 0 {
		return nil
	}
	shown := cats
	tail := ""
	if len(shown) > lowDataNamesShown {
		tail = fmt.Sprintf(" и ещё %d", len(shown)-lowDataNamesShown)
		shown = shown[:lowDataNamesShown]
	}
	return []string{fmt.Sprintf(
		"Мало истории: %s%s — пока считаю их в конверте «%s». Скажи «на %s хватит N», если нужен свой лимит.",
		strings.Join(shown, ", "), tail, budget.FallbackShareName, cats[0])}
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
