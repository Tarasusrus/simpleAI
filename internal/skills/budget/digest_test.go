package budgetskill

import (
	"context"
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

// mockDigestStore — фейк digestStore: фиксированная сводка + захват Period.
//
// baselineSummary (если задан) отдаётся для окна шире одного дня (бэйзлайн
// аномалии); иначе на все запросы идёт summary. earliest/hasEarliest управляют
// гейтом возраста: по умолчанию hasEarliest=false → аномалия не считается
// (старые тесты без истории не затрагиваются).
type mockDigestStore struct {
	summary         *budget.Summary
	baselineSummary *budget.Summary
	rates           map[string]float64
	gotPeriod       budget.Period
	summaryErr      error
	earliest        time.Time
	hasEarliest     bool
	// summaryFunc, если задан, полностью определяет ответ GetSummary по Period
	// (нужно WoW-тестам: два разных 7-дневных окна не различить по ширине).
	summaryFunc func(budget.Period) (*budget.Summary, error)
}

func (m *mockDigestStore) GetSummary(_ context.Context, p budget.Period) (*budget.Summary, error) {
	if m.summaryErr != nil {
		return nil, m.summaryErr
	}
	if m.summaryFunc != nil {
		return m.summaryFunc(p)
	}
	// Многодневное окно (>1 суток) — это запрос бэйзлайна/недель.
	if p.To.Sub(p.From) > 48*time.Hour {
		if m.baselineSummary != nil {
			return m.baselineSummary, nil
		}
		return m.summary, nil
	}
	// Однодневное окно — «вчера»; захватываем именно его.
	m.gotPeriod = p
	return m.summary, nil
}

func (m *mockDigestStore) GetExchangeRates(_ context.Context) (map[string]float64, error) {
	return m.rates, nil
}

func (m *mockDigestStore) EarliestTransactionDate(_ context.Context) (time.Time, bool, error) {
	return m.earliest, m.hasEarliest, nil
}

func expenseSummary(groups ...budget.CurrencyGroup) *budget.Summary {
	return &budget.Summary{Currencies: groups}
}

// withFixedNow подменяет nowFunc на время для проверки и восстанавливает.
func withFixedNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = prev })
}

// Непустой день в RUB → строка с символом ฿ и THB-эквивалентом (RUB/2.5).
func TestYesterdayDigest_NonEmpty(t *testing.T) {
	store := &mockDigestStore{
		summary: expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 2500}),
		rates:   map[string]float64{"RUB": 1, "THB": 2.5},
	}
	out, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, time.UTC)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// 2500 RUB / 2.5 = 1000 ฿
	if !strings.Contains(out, "฿") || !strings.Contains(out, "1000") {
		t.Fatalf("want THB total 1000 ฿, got %q", out)
	}
}

// Пустой день (нет трат) → "".
func TestYesterdayDigest_Empty(t *testing.T) {
	store := &mockDigestStore{
		summary: expenseSummary(),
		rates:   map[string]float64{"RUB": 1, "THB": 2.5},
	}
	out, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, time.UTC)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "" {
		t.Fatalf("empty day must yield \"\", got %q", out)
	}
}

// Мультивалюта сводится к THB-экв: RUB напрямую, THB как есть, USD через RUB.
func TestYesterdayDigest_MultiCurrency(t *testing.T) {
	store := &mockDigestStore{
		summary: expenseSummary(
			budget.CurrencyGroup{Currency: "RUB", TotalExpense: 250}, // /2.5 = 100 ฿
			budget.CurrencyGroup{Currency: "THB", TotalExpense: 300}, // = 300 ฿
			budget.CurrencyGroup{Currency: "USD", TotalExpense: 10},  // *82/2.5 = 328 ฿
		),
		rates: map[string]float64{"RUB": 1, "THB": 2.5, "USD": 82},
	}
	out, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, time.UTC)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// 100 + 300 + 328 = 728 ฿
	if !strings.Contains(out, "728") {
		t.Fatalf("want 728 ฿, got %q", out)
	}
}

// Граница таймзоны: «вчера» считается в loc, не в UTC.
func TestYesterdayDigest_TimezoneBoundary(t *testing.T) {
	// 18:00 UTC 2026-06-29 == 01:00 2026-06-30 в Asia/Bangkok (+7).
	withFixedNow(t, time.Date(2026, 6, 29, 18, 0, 0, 0, time.UTC))
	bangkok, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	store := &mockDigestStore{
		summary: expenseSummary(budget.CurrencyGroup{Currency: "THB", TotalExpense: 100}),
		rates:   map[string]float64{"RUB": 1, "THB": 2.5},
	}
	if _, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, bangkok); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// В Бангкоке сейчас 30 июня → вчера = 29 июня. В UTC было бы 28 июня.
	if got := store.gotPeriod.From.Day(); got != 29 {
		t.Fatalf("yesterday in loc must be day 29, got day %d (%s)", got, store.gotPeriod.From)
	}
}

// Молодой ledger (возраст < minLedgerDays) → только базовая строка, без
// мусорной аномалии, даже если бэйзлайн задан.
func TestYesterdayDigest_YoungLedgerNoAnomaly(t *testing.T) {
	withFixedNow(t, time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)) // вчера = 29 июня
	store := &mockDigestStore{
		summary:         expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 2500}),
		baselineSummary: expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 46125}),
		rates:           map[string]float64{"RUB": 1, "THB": 2.5},
		earliest:        time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), // возраст 2 дня
		hasEarliest:     true,
	}
	out, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, time.UTC)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.Contains(out, "среднего") {
		t.Fatalf("young ledger must not show anomaly, got %q", out)
	}
	if !strings.Contains(out, "1000") {
		t.Fatalf("want base line with 1000 ฿, got %q", out)
	}
}

// Зрелый ledger + перерасход → заметка «выше среднего» с ⚠️.
func TestYesterdayDigest_AnomalyOverspend(t *testing.T) {
	withFixedNow(t, time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC))
	store := &mockDigestStore{
		summary:         expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 2500}),  // 1000 ฿
		baselineSummary: expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 46125}), // avg 615 ฿/день
		rates:           map[string]float64{"RUB": 1, "THB": 2.5},
		earliest:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), // возраст > 14
		hasEarliest:     true,
	}
	out, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, time.UTC)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	for _, want := range []string{"выше", "среднего", "615", "⚠️"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in overspend anomaly, got %q", want, out)
		}
	}
}

// Зрелый ledger + недорасход → «ниже среднего», без ⚠️ (хорошая новость).
func TestYesterdayDigest_AnomalyUnderspend(t *testing.T) {
	withFixedNow(t, time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC))
	store := &mockDigestStore{
		summary:         expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 750}),   // 300 ฿
		baselineSummary: expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 46125}), // avg 615 ฿/день
		rates:           map[string]float64{"RUB": 1, "THB": 2.5},
		earliest:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		hasEarliest:     true,
	}
	out, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, time.UTC)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "ниже") || strings.Contains(out, "⚠️") {
		t.Fatalf("underspend must be 'ниже' without ⚠️, got %q", out)
	}
}

// anomalyLine — чистый калькулятор: порог, направление, ⚠️, делёж на 0.
func TestAnomalyLine(t *testing.T) {
	if got := anomalyLine(650, 615); got != "" {
		t.Fatalf("within threshold must be empty, got %q", got) // dev ~5.7%
	}
	if got := anomalyLine(1000, 0); got != "" {
		t.Fatalf("zero avg must be empty (no div by zero), got %q", got)
	}
	over := anomalyLine(1000, 615)
	if !strings.Contains(over, "выше") || !strings.Contains(over, "⚠️") {
		t.Fatalf("overspend want 'выше'+⚠️, got %q", over)
	}
	under := anomalyLine(300, 615)
	if !strings.Contains(under, "ниже") || strings.Contains(under, "⚠️") {
		t.Fatalf("underspend want 'ниже' no ⚠️, got %q", under)
	}
}

// Топ-категории: 2+ категории → строка «📊 Топ:» с топ-2 по доле, сортировка
// по убыванию.
func TestTopCategoriesLine_TopTwo(t *testing.T) {
	sum := expenseSummary(budget.CurrencyGroup{
		Currency: "RUB",
		ByCategory: []budget.CategoryTotal{
			{CategoryName: "еда", Total: 500},
			{CategoryName: "транспорт", Total: 300},
			{CategoryName: "развлечения", Total: 200},
		},
	})
	out := topCategoriesLine(sum, map[string]float64{"RUB": 1, "THB": 2.5})
	// total 1000: еда 50%, транспорт 30%. развлечения (20%) вне топ-2.
	if !strings.HasPrefix(out, "📊 Топ:") {
		t.Fatalf("want prefix '📊 Топ:', got %q", out)
	}
	for _, want := range []string{"еда 50%", "транспорт 30%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q, got %q", want, out)
		}
	}
	if strings.Contains(out, "развлечения") {
		t.Fatalf("3rd category must be outside top-2, got %q", out)
	}
}

// Одна категория → строка опускается (одиночная «100%» неинформативна).
func TestTopCategoriesLine_SingleOmitted(t *testing.T) {
	sum := expenseSummary(budget.CurrencyGroup{
		Currency:   "RUB",
		ByCategory: []budget.CategoryTotal{{CategoryName: "еда", Total: 500}},
	})
	if got := topCategoriesLine(sum, map[string]float64{"RUB": 1, "THB": 2.5}); got != "" {
		t.Fatalf("single category must omit line, got %q", got)
	}
}

// Кросс-валютное сведение: одна категория в двух валютах суммируется, а не
// даёт дубли еда(RUB)/еда(THB).
func TestTopCategoriesLine_CrossCurrency(t *testing.T) {
	sum := expenseSummary(
		budget.CurrencyGroup{Currency: "RUB", ByCategory: []budget.CategoryTotal{
			{CategoryName: "еда", Total: 250},       // 250 RUB
			{CategoryName: "транспорт", Total: 100}, // 100 RUB
		}},
		budget.CurrencyGroup{Currency: "THB", ByCategory: []budget.CategoryTotal{
			{CategoryName: "еда", Total: 100}, // 100 THB * 2.5 = 250 RUB → еда всего 500 RUB
		}},
	)
	out := topCategoriesLine(sum, map[string]float64{"RUB": 1, "THB": 2.5})
	// total: еда 500 + транспорт 100 = 600. еда 83%, транспорт 17%.
	if strings.Count(out, "еда") != 1 {
		t.Fatalf("еда must appear once (consolidated), got %q", out)
	}
	if !strings.Contains(out, "еда 83%") {
		t.Fatalf("want consolidated 'еда 83%%', got %q", out)
	}
}

// wowLine — чистый калькулятор: делёж на 0, рост ↑, падение ↓, ~ноль.
func TestWowLine(t *testing.T) {
	if got := wowLine(8000, 0); got != "" {
		t.Fatalf("empty prev week must omit (no div0), got %q", got)
	}
	up := wowLine(12000, 10000)
	if !strings.Contains(up, "↑") || !strings.Contains(up, "20%") {
		t.Fatalf("growth want ↑20%%, got %q", up)
	}
	down := wowLine(8000, 10000)
	if !strings.Contains(down, "↓") || !strings.Contains(down, "20%") {
		t.Fatalf("drop want ↓20%%, got %q", down)
	}
	if got := wowLine(10000, 10000); !strings.Contains(got, "≈") {
		t.Fatalf("no change want '≈', got %q", got)
	}
}

// Полный дайджест: зрелый ledger, 3 строки — база+аномалия, топ-категории, WoW.
// Проверяет проводку всех под-инсайтов через summaryFunc-роутинг по окнам.
func TestYesterdayDigest_FullThreeLines(t *testing.T) {
	withFixedNow(t, time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)) // вчера = 29 июня
	rates := map[string]float64{"RUB": 1, "THB": 2.5}
	yesterdaySum := expenseSummary(budget.CurrencyGroup{
		Currency: "RUB", TotalExpense: 2500, // 1000 ฿
		ByCategory: []budget.CategoryTotal{
			{CategoryName: "еда", Total: 1500},      // 60%
			{CategoryName: "транспорт", Total: 1000}, // 40%
		},
	})
	baselineSum := expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 46125}) // avg 615 ฿
	thisWeekSum := expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 20000}) // 8000 ฿
	prevWeekSum := expenseSummary(budget.CurrencyGroup{Currency: "RUB", TotalExpense: 25000}) // 10000 ฿ → ↓20%

	store := &mockDigestStore{
		rates:       rates,
		earliest:    time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		hasEarliest: true,
		summaryFunc: func(p budget.Period) (*budget.Summary, error) {
			spanDays := int(p.To.Sub(p.From).Hours() / 24)
			switch {
			case spanDays == 0:
				return yesterdaySum, nil
			case spanDays >= 20: // 30-дневный бэйзлайн
				return baselineSum, nil
			case p.From.Day() == 23: // this-week 06-23..06-29
				return thisWeekSum, nil
			default: // prev-week 06-16..06-22
				return prevWeekSum, nil
			}
		},
	}
	out, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, time.UTC)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	for _, want := range []string{"💸 Вчера: ~1000", "выше", "⚠️", "📊 Топ:", "еда 60%", "📈 Неделя: ↓20%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in full digest, got %q", want, out)
		}
	}
	if lines := strings.Count(out, "\n"); lines != 2 {
		t.Fatalf("want 3 lines (2 newlines), got %d in %q", lines, out)
	}
}

// Пустая строка при ошибке GetSummary (вызывающий деградирует non-fatal).
func TestYesterdayDigest_SummaryError(t *testing.T) {
	store := &mockDigestStore{summaryErr: context.DeadlineExceeded, rates: map[string]float64{"THB": 2.5}}
	out, err := NewDigestProvider(store).YesterdayDigest(context.Background(), 1, time.UTC)
	if err == nil {
		t.Fatal("want error from GetSummary")
	}
	if out != "" {
		t.Fatalf("error must yield empty string, got %q", out)
	}
}
