package advisorskill

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// parseAnalyzeLLMResponse: валидный JSON принимается, advice сохраняется.
func TestParseAnalyzeLLMResponse_Valid(t *testing.T) {
	raw := `{"anomalies":["a1"],"trends":["t1","t2"],"advice":["adv1","adv2"]}`
	r, err := parseAnalyzeLLMResponse(raw)
	if err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if len(r.Advice) != 2 || r.Advice[0] != "adv1" {
		t.Fatalf("advice mismatch: %v", r.Advice)
	}
	if len(r.Anomalies) != 1 || len(r.Trends) != 2 {
		t.Fatalf("counts mismatch: anomalies=%d trends=%d", len(r.Anomalies), len(r.Trends))
	}
}

// parseAnalyzeLLMResponse: пустой advice — ошибка.
func TestParseAnalyzeLLMResponse_EmptyAdvice(t *testing.T) {
	raw := `{"anomalies":[],"trends":[],"advice":[]}`
	if _, err := parseAnalyzeLLMResponse(raw); err == nil {
		t.Fatal("empty advice must be rejected")
	}
}

// parseAnalyzeLLMResponse: обрамление ```json ... ``` снимается.
func TestParseAnalyzeLLMResponse_Codefence(t *testing.T) {
	raw := "```json\n{\"anomalies\":[],\"trends\":[],\"advice\":[\"x\"]}\n```"
	r, err := parseAnalyzeLLMResponse(raw)
	if err != nil {
		t.Fatalf("codefence input rejected: %v", err)
	}
	if len(r.Advice) != 1 {
		t.Fatalf("advice not parsed: %v", r.Advice)
	}
}

// parseAnalyzePeriod: month → текущий месяц, YYYY-MM → конкретный месяц.
func TestParseAnalyzePeriod(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in        string
		wantLabel string
		wantErr   bool
	}{
		{"", "текущий месяц", false},
		{"month", "текущий месяц", false},
		{"2026-03", "2026-03", false},
		{"garbage", "", true},
		{"2026-13", "", true},
	}
	for _, c := range cases {
		_, label, err := parseAnalyzePeriod(c.in, now)
		if (err != nil) != c.wantErr {
			t.Fatalf("%q: err=%v want_err=%v", c.in, err, c.wantErr)
		}
		if !c.wantErr && label != c.wantLabel {
			t.Fatalf("%q: label=%q want=%q", c.in, label, c.wantLabel)
		}
	}
}

// formatAnalyzeReply: советы и аномалии всегда отрисовываются.
func TestFormatAnalyzeReply(t *testing.T) {
	r := &analyzeLLMResponse{
		Anomalies: []string{"еда +40%"},
		Trends:    []string{"топ-3 категории = 70% бюджета"},
		Advice:    []string{"совет 1", "совет 2"},
	}
	out := formatAnalyzeReply("март", r)
	for _, want := range []string{"Анализ за март", "Аномалии", "Тренды", "Советы", "совет 1", "совет 2", "еда"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
}

// buildAnalyzePrompt включает label, текущий и прошлый период, top-N в строку.
func TestBuildAnalyzePrompt(t *testing.T) {
	cur := &budget.AdvisorSnapshot{
		BalanceMTD: 10000, FreeCash: 5000, TxCount: 12,
		SpentByCategory: map[string]float64{"Еда": 3000},
	}
	prev := &budget.AdvisorSnapshot{
		SpentByCategory: map[string]float64{"Еда": 2000, "Транспорт": 1500},
	}
	top := []budget.TopExpense{
		{Date: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC), Category: "Еда", AmountTHB: 1500},
	}
	out := buildAnalyzePrompt("текущий месяц", cur, prev, top, 20)
	for _, want := range []string{"текущий месяц", "Еда", "Транспорт", "2026-05-03", "1500"} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q: %s", want, out)
		}
	}
}
