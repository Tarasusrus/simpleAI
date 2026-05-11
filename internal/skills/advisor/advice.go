package advisorskill

import (
	"fmt"
	"strings"
	"time"

	"context"

	"simpleAI/internal/agent"
	botformat "simpleAI/internal/bot/format"
	"simpleAI/internal/budget"
	"simpleAI/internal/prompts"
)

// advisorLLMResponse — JSON-структура которую обязана вернуть LLM.
type advisorLLMResponse struct {
	Verdict string `json:"verdict"` // "Да" | "Нет" | "Условно"
	Flow    struct {
		YesFundNow          float64 `json:"yes_fund_now"`
		YesFundAfterPurchase float64 `json:"yes_fund_after_purchase"`
		BalanceNow           float64 `json:"balance_now"`
		ObligationsRemaining float64 `json:"obligations_remaining"`
		AfterObligations     float64 `json:"after_obligations"`
		ExpectedSpend        float64 `json:"expected_spend"`
		ForecastRemaining    float64 `json:"forecast_remaining"`
		AfterPurchase        float64 `json:"after_purchase"`
	} `json:"flow"`
	Assumptions    []string `json:"assumptions"`
	Risk           string   `json:"risk"`
	Recommendation string   `json:"recommendation,omitempty"`
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
	yesFund := computeYesFund(snap)

	prompt := buildAdvisorPrompt(req, snap, yesFund, amountTHB, origAmount, origCurrency)

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
		Flow: botformat.AdvisorFlow{
			YesFundNow:           parsed.Flow.YesFundNow,
			YesFundAfterPurchase: parsed.Flow.YesFundAfterPurchase,
			BalanceNow:           parsed.Flow.BalanceNow,
			ObligationsRemaining: parsed.Flow.ObligationsRemaining,
			AfterObligations:     parsed.Flow.AfterObligations,
			ExpectedSpend:        parsed.Flow.ExpectedSpend,
			ForecastRemaining:    parsed.Flow.ForecastRemaining,
			AfterPurchase:        parsed.Flow.AfterPurchase,
		},
		Assumptions:    parsed.Assumptions,
		Risk:           parsed.Risk,
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
		"forecast_remaining_thb", parsed.Flow.ForecastRemaining,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return reply, nil
}

const murphyFactor = 1.10 // +10% buffer on projected remaining spend

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
	return snap.BalanceMTD - remainingExpected*murphyFactor
}

// computeYesFund returns the amount freely spendable this month after all
// commitments (upcoming recurring expenses and active debts) are covered,
// including income still expected to arrive (upcoming recurring income).
// Goals are not yet subtracted (no monthly contribution data available).
func computeYesFund(snap *budget.AdvisorSnapshot) float64 {
	return snap.BalanceMTD + snap.UpcomingRecurringIncome - snap.UpcomingRecurring - snap.ActiveDebtDue
}

func buildAdvisorPrompt(req advisorInput, snap *budget.AdvisorSnapshot, yesFund, amountTHB, origAmount float64, origCurrency string) string {
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
		yesFund,
		snap.UpcomingRecurringIncome,
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
	if strings.TrimSpace(r.Risk) == "" {
		return nil, fmt.Errorf("risk is empty")
	}
	if r.Verdict != "Да" && strings.TrimSpace(r.Recommendation) == "" {
		return nil, fmt.Errorf("recommendation required when verdict=%q", r.Verdict)
	}
	return &r, nil
}
