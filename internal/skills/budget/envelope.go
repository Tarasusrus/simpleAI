package budgetskill

import (
	"context"
	"fmt"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

// startEnvelope сохраняет «живой конверт» (ADR-007 H3): приход + горизонт.
// Остаток не хранится — считается позже из фактических трат (safe_to_spend).
// Границы горизонта — единый budget.ResolveHorizon (без дублирования резолвера).
func (s *BudgetSkill) startEnvelope(ctx context.Context, req budgetInput) (string, error) {
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

	h := budget.ResolveHorizon(req.Period, time.Now(), budget.DefaultHorizonDays)
	if _, err := s.store.CreateEnvelope(ctx, chatID, req.Amount, currency, h.From, h.To); err != nil {
		return "", err
	}
	return fmt.Sprintf("🧧 Конверт создан: приход %.0f %s на горизонт %s.\nСпроси «сколько свободно осталось?» в любой момент — пересчитаю по фактическим тратам.",
		req.Amount, currency, h.Label), nil
}
