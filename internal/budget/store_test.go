package budget

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

// rapidRates генерирует map курсов (rate_to_rub > 0) гарантированно содержащий THB.
func rapidRates(t *rapid.T) map[string]float64 {
	rates := map[string]float64{
		"RUB": 1.0,
		"THB": rapid.Float64Range(0.5, 5.0).Draw(t, "thb_rate"),
		"USD": rapid.Float64Range(50.0, 100.0).Draw(t, "usd_rate"),
		"EUR": rapid.Float64Range(60.0, 120.0).Draw(t, "eur_rate"),
	}
	return rates
}

func rapidRow(t *rapid.T, label string) advisorRow {
	kind := rapid.SampledFrom([]string{"tx", "debt", "recurring"}).Draw(t, label+".kind")
	subtype := ""
	if kind == "tx" || kind == "recurring" {
		subtype = rapid.SampledFrom([]string{"income", "expense"}).Draw(t, label+".subtype")
	}
	currency := rapid.SampledFrom([]string{"RUB", "THB", "USD", "EUR"}).Draw(t, label+".currency")
	if kind == "debt" {
		currency = "RUB"
		subtype = ""
	}
	return advisorRow{
		Kind:     kind,
		Subtype:  subtype,
		Currency: currency,
		Category: rapid.SampledFrom([]string{"Еда", "Транспорт", "Жильё", "Прочее"}).Draw(t, label+".cat"),
		Total:    rapid.Float64Range(0, 100000).Draw(t, label+".total"),
		Cnt:      rapid.IntRange(0, 50).Draw(t, label+".cnt"),
	}
}

// FreeCash всегда == BalanceMTD - UpcomingRecurring - ActiveDebtDue, для любого набора rows и валидных rates.
func TestProperty_FreeCashInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rates := rapidRates(t)
		rows := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) advisorRow {
			return rapidRow(t, "row")
		}), 0, 30).Draw(t, "rows")

		snap, err := aggregateAdvisorSnapshot(rows, rates)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := snap.BalanceMTD - snap.UpcomingRecurring - snap.ActiveDebtDue
		if math.Abs(snap.FreeCash-want) > 1e-6 {
			t.Fatalf("FreeCash=%.6f want=%.6f", snap.FreeCash, want)
		}
	})
}

// LowData ⇔ TxCount < MinTxForConfidence, всегда.
func TestProperty_LowDataInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rates := rapidRates(t)
		rows := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) advisorRow {
			return rapidRow(t, "row")
		}), 0, 20).Draw(t, "rows")

		snap, err := aggregateAdvisorSnapshot(rows, rates)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		expected := snap.TxCount < MinTxForConfidence
		if snap.LowData != expected {
			t.Fatalf("LowData=%v expected=%v (TxCount=%d, threshold=%d)",
				snap.LowData, expected, snap.TxCount, MinTxForConfidence)
		}
	})
}

// Отсутствие THB rate в rates → ошибка, всегда. Прочие отсутствующие rates не падают.
func TestProperty_THBRateRequired(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rates := map[string]float64{
			"RUB": 1.0,
			"USD": rapid.Float64Range(50.0, 100.0).Draw(t, "usd"),
		}
		rows := []advisorRow{{Kind: "tx", Subtype: "expense", Currency: "RUB", Total: 100, Cnt: 1}}
		if _, err := aggregateAdvisorSnapshot(rows, rates); err == nil {
			t.Fatal("expected error when THB rate is missing")
		}
	})
}

// SpentByCategory сумма всегда совпадает с суммарным THB-эквивалентом expense-tx по известным валютам.
func TestProperty_SpentByCategoryConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rates := rapidRates(t)
		// только tx/expense строки с известной валютой
		n := rapid.IntRange(0, 15).Draw(t, "n")
		rows := make([]advisorRow, 0, n)
		for i := 0; i < n; i++ {
			rows = append(rows, advisorRow{
				Kind: "tx", Subtype: "expense",
				Currency: rapid.SampledFrom([]string{"RUB", "THB", "USD", "EUR"}).Draw(t, "cur"),
				Category: rapid.SampledFrom([]string{"Еда", "Жильё"}).Draw(t, "cat"),
				Total:    rapid.Float64Range(1, 10000).Draw(t, "total"),
				Cnt:      1,
			})
		}
		snap, err := aggregateAdvisorSnapshot(rows, rates)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		var sum float64
		for _, v := range snap.SpentByCategory {
			sum += v
		}
		// expected: same conversion via rates
		var expected float64
		for _, r := range rows {
			expected += r.Total * rates[r.Currency] / rates["THB"]
		}
		if math.Abs(sum-expected) > 1e-3 {
			t.Fatalf("sum=%.6f expected=%.6f", sum, expected)
		}
	})
}
