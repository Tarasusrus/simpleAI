package budgetskill

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"simpleAI/internal/budget"
)

// nowFunc — источник текущего времени, подменяется в тестах для проверки
// границы таймзоны.
var nowFunc = time.Now

// Пороги умного дайджеста — единый ориентир для тюнинга. Обоснование и
// правила подбора — ADR-006. Менять здесь, не в теле функций.
const (
	// baselineDays — окно среднего дневного расхода для детекта аномалий.
	baselineDays = 30
	// minLedgerDays — пока ledger моложе, аномалию не считаем: 30-дневный
	// бэйзлайн ещё не наполнен, отклонение было бы мусорным (ADR-001 —
	// graceful degradation при нехватке истории).
	minLedgerDays = 14
	// anomalyThreshold — минимальное относительное отклонение вчерашних трат
	// от среднего, при котором показываем заметку. Дневной шум высокий,
	// поэтому порог грубее трендовых ±3% из ADR-001.
	anomalyThreshold = 0.25
	// wowNoisePct — ниже этого |WoW| в процентах изменение считаем шумом и
	// показываем «≈ как на прошлой» (иначе округлилось бы до 0%).
	wowNoisePct = 0.5
	// topCategories — сколько категорий показываем в строке топа.
	topCategories = 2
)

// digestStore — узкий контракт стора для дайджеста (тестируемость).
// *budget.Store его удовлетворяет.
type digestStore interface {
	GetSummary(ctx context.Context, p budget.Period) (*budget.Summary, error)
	GetExchangeRates(ctx context.Context) (map[string]float64, error)
	EarliestTransactionDate(ctx context.Context) (time.Time, bool, error)
}

// DigestProvider строит текстовый дайджест трат за вчера для напоминаний.
// Реализует notify.DigestSource структурно (метод YesterdayDigest), что держит
// пакет notify развязанным: рендер и конверсия валют живут здесь (budget/skills),
// notify знает только сигнатуру интерфейса.
type DigestProvider struct {
	store digestStore
}

// NewDigestProvider создаёт провайдер дайджеста.
func NewDigestProvider(store digestStore) *DigestProvider {
	return &DigestProvider{store: store}
}

// YesterdayDigest возвращает многострочный дайджест трат за вчера в THB-экв.
//
// chatID НЕ участвует в выборке данных: бюджет — глобальный single-household
// ledger (budget_transaction без chat_id, store.go GetSummary). chatID и loc
// определяют только календарный день «вчера» в таймзоне получателя.
//
// Инвариант доставки: любой под-инсайт (аномалия/топ/WoW) деградирует молча —
// наружу отдаётся максимум частичный текст. Пустой день (нет трат) → ""
// (вызывающий не показывает блок).
func (d *DigestProvider) YesterdayDigest(ctx context.Context, chatID int64, loc *time.Location) (string, error) {
	if loc == nil {
		loc = time.UTC
	}
	_ = chatID // global ledger: chatID только для адресации, не фильтр данных

	// rates тянем один раз на весь дайджест (несколько под-инсайтов).
	rates := d.rates(ctx)

	// Календарный день «вчера» в loc.
	yesterday := nowFunc().In(loc).AddDate(0, 0, -1)

	sum, err := d.store.GetSummary(ctx, dayPeriod(yesterday, yesterday))
	if err != nil {
		return "", fmt.Errorf("digest: get summary: %w", err)
	}

	totalTHB := summaryTotalTHB(sum, rates)
	if totalTHB <= 0 {
		// Пусто вчера — ранний выход до любых инсайтов (инвариант "" ).
		return "", nil
	}

	// Строка 1 — базовая, всегда. Заметка-аномалия аппендится, если считается.
	line1 := fmt.Sprintf("💸 Вчера: ~%.0f ฿", totalTHB)
	if note := d.anomalyNote(ctx, yesterday, totalTHB, rates); note != "" {
		line1 += " " + note
	}

	// Строка 2 — топ-категории вчера. Опциональна: молча опускается при < 2
	// категориях или ошибке. Переиспользует уже загруженный sum (без доп. запроса).
	lines := []string{line1}
	if l2 := topCategoriesLine(sum, rates); l2 != "" {
		lines = append(lines, l2)
	}

	// Строка 3 — неделя-к-неделе. Опциональна: молча опускается при пустой
	// прошлой неделе (делёж на 0) или ошибке.
	if l3 := d.weekOverWeekLine(ctx, yesterday, rates); l3 != "" {
		lines = append(lines, l3)
	}

	return strings.Join(lines, "\n"), nil
}

// weekOverWeekLine сравнивает траты последних 7 дней (кончая вчера) с
// предыдущими 7 днями. "" при ошибке стора или пустой прошлой неделе.
// Окна якорятся UTC-midnight как «вчера» (dayPeriod).
func (d *DigestProvider) weekOverWeekLine(ctx context.Context, yesterday time.Time, rates map[string]float64) string {
	thisSum, err := d.store.GetSummary(ctx, dayPeriod(yesterday.AddDate(0, 0, -6), yesterday))
	if err != nil {
		return ""
	}
	prevSum, err := d.store.GetSummary(ctx, dayPeriod(yesterday.AddDate(0, 0, -13), yesterday.AddDate(0, 0, -7)))
	if err != nil {
		return ""
	}
	return wowLine(summaryTotalTHB(thisSum, rates), summaryTotalTHB(prevSum, rates))
}

// wowLine — чистый калькулятор WoW-строки. "" если прошлая неделя пуста
// (защита от делёжа на 0). ↑ рост трат, ↓ падение; при ~нулевом изменении —
// «≈ как на прошлой».
func wowLine(thisWeek, prevWeek float64) string {
	if prevWeek <= 0 {
		return ""
	}
	delta := (thisWeek - prevWeek) / prevWeek
	if math.Abs(delta)*100 < wowNoisePct { // ниже порога шума → округлилось бы до 0%
		return "📈 Неделя: ≈ как на прошлой"
	}
	arrow := "↑"
	if delta < 0 {
		arrow = "↓"
	}
	return fmt.Sprintf("📈 Неделя: %s%.0f%% к прошлой", arrow, math.Abs(delta)*100)
}

// topCategoriesLine — строка топ-2 категорий вчера по доле расхода.
// Категории сводятся кросс-валютно (summaryCategories → RUB; доли валюто-
// инвариантны, поэтому считаем в RUB без конверсии в THB). "" при < 2
// категориях: одиночная «еда 100%» неинформативна.
func topCategoriesLine(sum *budget.Summary, rates map[string]float64) string {
	cats := summaryCategories(sum, rates) // map[name]RUB, кросс-валютно
	type kv struct {
		name string
		val  float64
	}
	items := make([]kv, 0, len(cats))
	var total float64
	for n, v := range cats {
		if v <= 0 {
			continue
		}
		items = append(items, kv{n, v})
		total += v
	}
	if len(items) < topCategories || total <= 0 {
		return ""
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].val != items[j].val {
			return items[i].val > items[j].val
		}
		return items[i].name < items[j].name // детерминизм при равных долях
	})

	parts := make([]string, 0, topCategories)
	for i := 0; i < len(items) && i < topCategories; i++ {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", items[i].name, items[i].val/total*100))
	}
	return "📊 Топ: " + strings.Join(parts, ", ")
}

// anomalyNote возвращает заметку об отклонении вчерашних трат от среднего за
// baselineDays, либо "" если считать нечего (молодой ledger, нет бэйзлайна,
// отклонение ниже порога, ошибка стора). Всё — молчаливая деградация.
func (d *DigestProvider) anomalyNote(ctx context.Context, yesterday time.Time, yesterdayTHB float64, rates map[string]float64) string {
	// Гейт возраста: молодой ledger → нет достоверного бэйзлайна.
	earliest, ok, err := d.store.EarliestTransactionDate(ctx)
	if err != nil || !ok {
		return ""
	}
	if ledgerAgeDays(earliest, yesterday) < minLedgerDays {
		return ""
	}

	// Окно бэйзлайна — baselineDays, заканчивается вчера включительно.
	// Якорь дат — UTC-midnight от календарной даты в loc (как dayPeriod).
	start := yesterday.AddDate(0, 0, -(baselineDays - 1))
	sum, err := d.store.GetSummary(ctx, dayPeriod(start, yesterday))
	if err != nil {
		return ""
	}
	avgDaily := summaryTotalTHB(sum, rates) / baselineDays
	return anomalyLine(yesterdayTHB, avgDaily)
}

// anomalyLine — чистый калькулятор заметки об отклонении от среднего за
// baselineDays. "" если средний невалиден или отклонение ниже порога.
// ⚠️ только при перерасходе (тратил больше среднего): расход ниже среднего —
// хорошая новость, предупреждать не о чем.
func anomalyLine(yesterdayTHB, avgDaily float64) string {
	if avgDaily <= 0 {
		return ""
	}
	dev := (yesterdayTHB - avgDaily) / avgDaily
	if math.Abs(dev) < anomalyThreshold {
		return ""
	}
	dir, warn := "ниже", ""
	if dev > 0 {
		dir, warn = "выше", " ⚠️"
	}
	return fmt.Sprintf("(на %.0f%% %s среднего %.0f ฿ за %d дней%s)",
		math.Abs(dev)*100, dir, avgDaily, baselineDays, warn)
}

// ledgerAgeDays — возраст ledger'а в днях: от первой транзакции до reference
// (обычно «вчера»). Обе даты сводятся к календарному дню UTC.
func ledgerAgeDays(earliest, reference time.Time) int {
	e := time.Date(earliest.Year(), earliest.Month(), earliest.Day(), 0, 0, 0, 0, time.UTC)
	r := time.Date(reference.Year(), reference.Month(), reference.Day(), 0, 0, 0, 0, time.UTC)
	return int(r.Sub(e).Hours() / 24)
}

// dayPeriod строит Period [from-день .. to-день] с якорем UTC-midnight от
// календарных дат from/to. transaction_date это DATE, важна только дата
// (консистентно с query.go и исходным digest).
func dayPeriod(from, to time.Time) budget.Period {
	return budget.Period{
		From: time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC),
		To:   time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 0, time.UTC),
	}
}

// rates возвращает курсы из стора с фолбэком на rubRates (как в callback.go).
func (d *DigestProvider) rates(ctx context.Context) map[string]float64 {
	rates, err := d.store.GetExchangeRates(ctx)
	if err != nil || len(rates) == 0 {
		return rubRates
	}
	for k, v := range rubRates {
		if _, ok := rates[k]; !ok {
			rates[k] = v
		}
	}
	return rates
}

// summaryTotalTHB сводит расходы сводки к THB-экв.
// Все валюты → RUB (summaryTotalRUB), затем делёж на курс THB→RUB.
// Курс THB берётся из rates с фолбэком на rubRates; защита от нуля.
func summaryTotalTHB(s *budget.Summary, rates map[string]float64) float64 {
	thbRate := rates["THB"]
	if thbRate <= 0 {
		thbRate = rubRates["THB"]
	}
	return summaryTotalRUB(s, rates) / thbRate
}
