package skills

import (
	"encoding/json"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"simpleAI/internal/budget"
)

// Manifest description всегда содержит positive triggers и negative phrase.
func TestProperty_ManifestDescriptionContract(t *testing.T) {
	s := NewAdvisorSkill(nil, nil, nil)
	desc := s.Manifest().Description
	for _, phrase := range []string{"purchase", "afford", "prioritization", "Do NOT", "budget skill"} {
		if !strings.Contains(desc, phrase) {
			t.Fatalf("manifest description must contain %q; got: %s", phrase, desc)
		}
	}
}

// computeForecastRemaining всегда <= BalanceMTD (вычитает неотрицательный remainingExpected).
func TestProperty_ForecastRemainingUpperBound(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rates := map[string]float64{
			"RUB": 1.0,
			"THB": rapid.Float64Range(0.5, 5.0).Draw(t, "thb"),
			"USD": rapid.Float64Range(50.0, 100.0).Draw(t, "usd"),
		}
		balance := rapid.Float64Range(-100000, 100000).Draw(t, "balance")
		nCats := rapid.IntRange(0, 5).Draw(t, "nCats")
		spent := map[string]float64{}
		for i := 0; i < nCats; i++ {
			spent[rapid.SampledFrom([]string{"Еда", "Жильё", "Прочее"}).Draw(t, "cat")] +=
				rapid.Float64Range(0, 50000).Draw(t, "spent")
		}
		nFc := rapid.IntRange(0, 8).Draw(t, "nFc")
		forecasts := make([]budget.CategoryForecast, 0, nFc)
		for i := 0; i < nFc; i++ {
			forecasts = append(forecasts, budget.CategoryForecast{
				Currency:       rapid.SampledFrom([]string{"RUB", "THB", "USD"}).Draw(t, "fcCur"),
				ForecastAmount: rapid.Float64Range(0, 50000).Draw(t, "fcAmt"),
			})
		}
		snap := &budget.AdvisorSnapshot{BalanceMTD: balance, SpentByCategory: spent}
		got := computeForecastRemaining(snap, forecasts, rates)
		if got > snap.BalanceMTD+1e-6 {
			t.Fatalf("ForecastRemaining %.6f must be <= BalanceMTD %.6f", got, snap.BalanceMTD)
		}
	})
}

// parseAdvisorLLMResponse: roundtrip — корректно собранный JSON парсится без ошибки.
func TestProperty_ParseLLMRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		verdict := rapid.SampledFrom([]string{"Да", "Нет", "Условно"}).Draw(t, "verdict")
		expl := rapid.StringMatching(`[a-zA-Zа-яА-Я]{1,50}`).Draw(t, "expl")
		rec := ""
		if verdict != "Да" {
			rec = rapid.StringMatching(`[a-zA-Zа-яА-Я]{1,50}`).Draw(t, "rec")
		}
		obj := map[string]any{
			"verdict": verdict,
			"numbers": map[string]any{
				"free_cash_thb":          rapid.Float64Range(-1e6, 1e6).Draw(t, "fc"),
				"forecast_remaining_thb": rapid.Float64Range(-1e6, 1e6).Draw(t, "fr"),
				"obligations_thb":        rapid.Float64Range(0, 1e6).Draw(t, "ob"),
			},
			"explanation":    expl,
			"recommendation": rec,
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		parsed, err := parseAdvisorLLMResponse(string(raw))
		if err != nil {
			t.Fatalf("valid input rejected: %v (raw: %s)", err, raw)
		}
		if parsed.Verdict != verdict {
			t.Fatalf("verdict mismatch: %q != %q", parsed.Verdict, verdict)
		}
	})
}

// parseAdvisorLLMResponse: невалидный verdict отвергается всегда.
func TestProperty_ParseLLMRejectsBadVerdict(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		bad := rapid.StringMatching(`[A-Za-z]{1,15}`).Draw(t, "bad")
		// исключаем случайное совпадение
		if bad == "Да" || bad == "Нет" || bad == "Условно" {
			return
		}
		obj := map[string]any{
			"verdict": bad,
			"numbers": map[string]any{
				"free_cash_thb": 1.0, "forecast_remaining_thb": 1.0, "obligations_thb": 1.0,
			},
			"explanation": "x",
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := parseAdvisorLLMResponse(string(raw)); err == nil {
			t.Fatalf("invalid verdict %q must be rejected", bad)
		}
	})
}

// formatAdvisorReply: вердикт всегда присутствует; non-THB amount всегда показывает обе суммы.
func TestProperty_FormatReplyContract(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		verdict := rapid.SampledFrom([]string{"Да", "Нет", "Условно"}).Draw(t, "verdict")
		r := &advisorLLMResponse{Verdict: verdict, Explanation: "expl"}
		if verdict != "Да" {
			r.Recommendation = "alt"
		}
		r.Numbers.FreeCashTHB = rapid.Float64Range(-1e5, 1e5).Draw(t, "fc")
		r.Numbers.ForecastRemainingTHB = rapid.Float64Range(-1e5, 1e5).Draw(t, "fr")
		r.Numbers.ObligationsTHB = rapid.Float64Range(0, 1e5).Draw(t, "ob")

		nonTHB := rapid.Bool().Draw(t, "nonTHB")
		var origAmt, amtTHB float64
		var origCur string
		if nonTHB {
			origAmt = rapid.Float64Range(1, 100000).Draw(t, "origAmt")
			origCur = rapid.SampledFrom([]string{"RUB", "USD", "EUR"}).Draw(t, "origCur")
			amtTHB = rapid.Float64Range(1, 100000).Draw(t, "amtTHB")
		} else {
			origCur = "THB"
		}

		out := formatAdvisorReply(r, origAmt, origCur, amtTHB)
		if !strings.Contains(out, "*Вердикт:* "+verdict) {
			t.Fatalf("verdict missing in output: %s", out)
		}
		if nonTHB {
			if !strings.Contains(out, origCur) || !strings.Contains(out, "THB") {
				t.Fatalf("non-THB amount must show both currencies; got: %s", out)
			}
		}
		if verdict != "Да" && !strings.Contains(out, "Рекомендация") {
			t.Fatalf("recommendation must render when verdict != Да; got: %s", out)
		}
	})
}
