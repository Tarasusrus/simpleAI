package budget

import (
	"math"
	"testing"
	"time"
)

func mar() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) }
func apr() time.Time { return time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC) }
func feb() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) }

func near(t *testing.T, got, want, eps float64, label string) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Fatalf("%s: got %.2f want %.2f (eps %.2f)", label, got, want, eps)
	}
}

// Платёж за жильё в марте (RUB) и апреле (THB) — один и тот же расход в разных валютах.
// До фикса: avg(RUB)+avg(THB) → двойной счёт. Сейчас: per-month THB → среднее ~18000.
func TestAggregateForecast_CrossCurrencySameCategory(t *testing.T) {
	rates := map[string]float64{"RUB": 1, "THB": 2.5}
	rows := []MonthlyCategoryExpense{
		{CategoryName: "Жильё", Icon: "🏠", Currency: "RUB", Month: mar(), Total: 50000},
		{CategoryName: "Жильё", Icon: "🏠", Currency: "THB", Month: mar(), Total: 241},
		{CategoryName: "Жильё", Icon: "🏠", Currency: "THB", Month: apr(), Total: 18000},
	}
	out := AggregateForecast(rows, rates)
	if len(out) != 1 {
		t.Fatalf("want 1 forecast, got %d", len(out))
	}
	near(t, out[0].ForecastAmount, 19120.5, 0.5, "ForecastAmount") // март: 50000/2.5+241=20241; апрель: 18000; avg=19120.5
	if out[0].Currency != "THB" {
		t.Fatalf("want Currency=THB, got %s", out[0].Currency)
	}
	if out[0].Icon != "🏠" {
		t.Fatalf("want icon 🏠, got %s", out[0].Icon)
	}
	if !out[0].HasTrend {
		t.Fatalf("want trend (2 months)")
	}
	// last (apr) = 18000, prev (mar) = 20241 → trend ≈ -11%
	near(t, out[0].TrendPct, -11.07, 0.5, "TrendPct")
}

// Одна категория, один месяц — без тренда.
func TestAggregateForecast_SingleMonth_NoTrend(t *testing.T) {
	rates := map[string]float64{"RUB": 1, "THB": 2.5}
	rows := []MonthlyCategoryExpense{
		{CategoryName: "Еда", Icon: "🍔", Currency: "THB", Month: apr(), Total: 4000},
	}
	out := AggregateForecast(rows, rates)
	if len(out) != 1 || out[0].HasTrend {
		t.Fatalf("expected single forecast without trend, got %+v", out)
	}
	near(t, out[0].ForecastAmount, 4000, 0.01, "ForecastAmount")
}

// Сортировка по убыванию ForecastAmount.
func TestAggregateForecast_SortedDesc(t *testing.T) {
	rates := map[string]float64{"RUB": 1, "THB": 2.5}
	rows := []MonthlyCategoryExpense{
		{CategoryName: "A", Icon: "📦", Currency: "THB", Month: apr(), Total: 100},
		{CategoryName: "B", Icon: "📦", Currency: "THB", Month: apr(), Total: 500},
		{CategoryName: "C", Icon: "📦", Currency: "THB", Month: apr(), Total: 300},
	}
	out := AggregateForecast(rows, rates)
	if len(out) != 3 {
		t.Fatalf("want 3, got %d", len(out))
	}
	if out[0].CategoryName != "B" || out[1].CategoryName != "C" || out[2].CategoryName != "A" {
		t.Fatalf("wrong order: %+v", out)
	}
}

// Строки с неизвестной валютой пропускаются, а не падают.
func TestAggregateForecast_UnknownCurrencySkipped(t *testing.T) {
	rates := map[string]float64{"RUB": 1, "THB": 2.5}
	rows := []MonthlyCategoryExpense{
		{CategoryName: "X", Icon: "📦", Currency: "EUR", Month: apr(), Total: 100},
		{CategoryName: "X", Icon: "📦", Currency: "THB", Month: apr(), Total: 200},
	}
	out := AggregateForecast(rows, rates)
	if len(out) != 1 {
		t.Fatalf("want 1 forecast, got %d", len(out))
	}
	near(t, out[0].ForecastAmount, 200, 0.01, "ForecastAmount")
}

// Без курса THB прогноз пуст.
func TestAggregateForecast_NoTHBRate(t *testing.T) {
	rates := map[string]float64{"RUB": 1}
	rows := []MonthlyCategoryExpense{
		{CategoryName: "X", Icon: "📦", Currency: "RUB", Month: apr(), Total: 1000},
	}
	out := AggregateForecast(rows, rates)
	if out != nil {
		t.Fatalf("want nil, got %+v", out)
	}
}

// Тренд с тремя месяцами: используются последние два.
func TestAggregateForecast_TrendUsesLastTwo(t *testing.T) {
	rates := map[string]float64{"RUB": 1, "THB": 2.5}
	rows := []MonthlyCategoryExpense{
		{CategoryName: "Еда", Icon: "🍔", Currency: "THB", Month: feb(), Total: 1000}, // старый
		{CategoryName: "Еда", Icon: "🍔", Currency: "THB", Month: mar(), Total: 2000}, // prev
		{CategoryName: "Еда", Icon: "🍔", Currency: "THB", Month: apr(), Total: 3000}, // last
	}
	out := AggregateForecast(rows, rates)
	if !out[0].HasTrend {
		t.Fatalf("want trend")
	}
	// last 3000, prev 2000 → +50%
	near(t, out[0].TrendPct, 50, 0.01, "TrendPct")
	near(t, out[0].ForecastAmount, 2000, 0.01, "avg of 3 months")
}
