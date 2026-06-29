package budgetskill

import (
	"context"
	"strings"
	"testing"
	"time"

	"simpleAI/internal/budget"
)

// mockDigestStore — фейк digestStore: фиксированная сводка + захват Period.
type mockDigestStore struct {
	summary    *budget.Summary
	rates      map[string]float64
	gotPeriod  budget.Period
	summaryErr error
}

func (m *mockDigestStore) GetSummary(_ context.Context, p budget.Period) (*budget.Summary, error) {
	m.gotPeriod = p
	if m.summaryErr != nil {
		return nil, m.summaryErr
	}
	return m.summary, nil
}

func (m *mockDigestStore) GetExchangeRates(_ context.Context) (map[string]float64, error) {
	return m.rates, nil
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
