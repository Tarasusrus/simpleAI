package advisorskill

import (
	"fmt"
	"strings"
	"time"

	"context"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	botformat "simpleAI/internal/bot/format"
	"simpleAI/internal/prompts"
)

// advisorLLMResponse — JSON-структура которую обязана вернуть LLM.
type advisorLLMResponse struct {
	Verdict string `json:"verdict"` // "Да" | "Нет" | "Условно"
	Numbers struct {
		FreeCashTHB          float64 `json:"free_cash_thb"`
		ForecastRemainingTHB float64 `json:"forecast_remaining_thb"`
		ObligationsTHB       float64 `json:"obligations_thb"`
	} `json:"numbers"`
	Explanation    string `json:"explanation"`
	Recommendation string `json:"recommendation,omitempty"`
}


func (s *AdvisorSkill) runAdvice(ctx context.Context, req advisorInput) (string, error) {
	start := time.Now()

	if strings.TrimSpace(req.Question) == "" {
		return "", fmt.Errorf("advisor: question is required for action='advice'")
	}

	chatID, hasChatID := ctx.Value(agent.ChatIDKey{}).(int64)
	if !hasChatID {
		s.logger.WarnContext(ctx, "advisor: chatID missing in context — recurring snapshot будет пуст")
		chatID = 0
	}

	rates, err := s.store.GetExchangeRates(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor: get exchange rates", "err", err, "chat_id", chatID)
		return "Временная ошибка с курсами валют — попробуй позже.", nil
	}
	if _, ok := rates["THB"]; !ok || rates["THB"] == 0 {
		s.logger.ErrorContext(ctx, "advisor: THB rate missing", "chat_id", chatID)
		return "Не могу посчитать в THB — обнови курс валют командой /rates.", nil
	}

	origAmount, origCurrency, amountTHB := req.Amount, strings.ToUpper(req.Currency), req.Amount
	if origCurrency == "" {
		origCurrency = "THB"
	}
	if origAmount > 0 && origCurrency != "THB" {
		rubRate, ok := rates[origCurrency]
		if !ok || rubRate == 0 {
			s.logger.ErrorContext(ctx, "advisor: currency rate missing", "currency", origCurrency, "chat_id", chatID)
			return fmt.Sprintf("Не знаю курс %s — обнови курс или укажи сумму в THB.", origCurrency), nil
		}
		amountTHB = origAmount * rubRate / rates["THB"]
	}

	snap, err := s.store.GetAdvisorSnapshot(ctx, chatID, time.Now(), 0, rates)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor: get snapshot", "err", err, "chat_id", chatID)
		return "Временная ошибка при сборе финансового снимка — попробуй позже.", nil
	}

	forecasts, err := s.store.GetForecastData(ctx, 0, rates)
	if err != nil {
		s.logger.WarnContext(ctx, "advisor: get forecast (continuing without)", "err", err, "chat_id", chatID)
	}
	snap.ForecastRemaining = computeForecastRemaining(snap, forecasts, rates)

	prompt := buildAdvisorPrompt(req, snap, amountTHB, origAmount, origCurrency)

	raw, err := s.llm.Ask(ctx, prompt)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor: llm call", "err", err, "chat_id", chatID)
		return "Не удалось получить совет — попробуй позже.", nil
	}

	parsed, err := parseAdvisorLLMResponse(raw)
	if err != nil {
		s.logger.ErrorContext(ctx, "advisor: parse llm response", "err", err, "raw", raw, "chat_id", chatID)
		return "Не смог разобрать ответ советника — попробуй переформулировать вопрос.", nil
	}

	reply := botformat.FormatAdvisorReply(botformat.AdvisorReplyData{
		Verdict: parsed.Verdict,
		Numbers: botformat.AdvisorNumbers{
			FreeCashTHB:          parsed.Numbers.FreeCashTHB,
			ForecastRemainingTHB: parsed.Numbers.ForecastRemainingTHB,
			ObligationsTHB:       parsed.Numbers.ObligationsTHB,
		},
		Explanation:    parsed.Explanation,
		Recommendation: parsed.Recommendation,
		OrigAmount:     origAmount,
		OrigCurrency:   origCurrency,
		AmountTHB:      amountTHB,
	})

	q := truncateRunes(req.Question, 200)
	s.logger.InfoContext(ctx, "advisor",
		"skill", "advisor",
		"action", "advice",
		"chat_id", chatID,
		"question", q,
		"verdict", parsed.Verdict,
		"free_cash_thb", parsed.Numbers.FreeCashTHB,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return reply, nil
}

func computeForecastRemaining(snap *budget.AdvisorSnapshot, forecasts []budget.CategoryForecast, rates map[string]float64) float64 {
	thbRate := rates["THB"]
	if thbRate == 0 {
		return snap.BalanceMTD
	}
	var projectedTotal float64
	for _, f := range forecasts {
		rubRate, ok := rates[f.Currency]
		if !ok || rubRate == 0 {
			continue
		}
		projectedTotal += f.ForecastAmount * rubRate / thbRate
	}
	var spentSoFar float64
	for _, v := range snap.SpentByCategory {
		spentSoFar += v
	}
	remainingExpected := projectedTotal - spentSoFar
	if remainingExpected < 0 {
		remainingExpected = 0
	}
	return snap.BalanceMTD - remainingExpected
}

func buildAdvisorPrompt(req advisorInput, snap *budget.AdvisorSnapshot, amountTHB, origAmount float64, origCurrency string) string {
	var amountLine string
	if origAmount > 0 {
		if origCurrency == "THB" {
			amountLine = fmt.Sprintf("Сумма из вопроса: %.0f THB", amountTHB)
		} else {
			amountLine = fmt.Sprintf("Сумма из вопроса: %.0f %s ≈ %.0f THB", origAmount, origCurrency, amountTHB)
		}
	}

	lowDataNote := ""
	if snap.LowData {
		lowDataNote = fmt.Sprintf(" (low_data=true, порог=%d — данных недостаточно для уверенного прогноза)", budget.MinTxForConfidence)
	}

	return fmt.Sprintf(prompts.Get("advisor/advice.tmpl"),
		strings.TrimSpace(req.Question),
		amountLine,
		snap.BalanceMTD,
		snap.ForecastRemaining,
		snap.FreeCash,
		snap.UpcomingRecurring,
		snap.ActiveDebtDue,
		snap.TxCount,
		lowDataNote,
		formatSpentByCategory(snap.SpentByCategory),
	)
}

func parseAdvisorLLMResponse(raw string) (*advisorLLMResponse, error) {
	s := stripMarkdownFences(raw)
	var r advisorLLMResponse
	if err := unmarshalJSON(s, &r); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	switch r.Verdict {
	case "Да", "Нет", "Условно":
	default:
		return nil, fmt.Errorf("invalid verdict: %q", r.Verdict)
	}
	if strings.TrimSpace(r.Explanation) == "" {
		return nil, fmt.Errorf("explanation is empty")
	}
	if r.Verdict != "Да" && strings.TrimSpace(r.Recommendation) == "" {
		return nil, fmt.Errorf("recommendation required when verdict=%q", r.Verdict)
	}
	return &r, nil
}

