package budgetskill

import (
	"context"
	"fmt"

	"simpleAI/internal/agent"
)

// addPlannedExpense записывает ручную разовую плановую трату (ADR-007 §6).
// chat-scoped. Используется safe_to_spend для точного свободного остатка.
func (s *BudgetSkill) addPlannedExpense(ctx context.Context, req budgetInput) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	if !ok || chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}
	currency := req.Currency
	if currency == "" {
		currency = "RUB"
	}
	if err := s.store.AddPlannedExpense(ctx, chatID, req.Amount, currency, req.Description); err != nil {
		return "", err
	}
	suffix := ""
	if req.Description != "" {
		suffix = " на " + req.Description
	}
	return fmt.Sprintf("📝 Плановая трата записана: %.0f %s%s", req.Amount, currency, suffix), nil
}
