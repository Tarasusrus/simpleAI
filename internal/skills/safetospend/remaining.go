package safetospend

import (
	"context"
	"fmt"
	"strings"
	"time"

	"simpleAI/internal/budget"
)

// runRemaining — режим «сколько свободно осталось» по активному конверту.
// Остаток вычисляется из фактических транзакций (ADR-007 §4, недвойной учёт),
// не хранится.
func (s *SafeToSpendSkill) runRemaining(ctx context.Context, chatID int64, rates map[string]float64) (string, error) {
	env, ok, err := s.store.GetActiveEnvelope(ctx, chatID)
	if err != nil {
		s.logger.WarnContext(ctx, "safe_to_spend: get active envelope", "err", err, "chat_id", chatID)
		return "Не удалось получить конверт — попробуй позже.", nil
	}
	if !ok {
		return "Активного конверта нет. Скажи «пришло X, сколько свободно?» — посчитаю, или «запомни приход X», чтобы отслеживать остаток.", nil
	}

	incomeTHB, ok := budget.ToTHB(env.IncomeAmount, env.IncomeCurrency, rates)
	if !ok {
		return fmt.Sprintf("Не знаю курс валюты конверта %s — обнови /rates.", env.IncomeCurrency), nil
	}

	// Обязательства — за весь горизонт конверта.
	snapObl, err := s.store.GetPeriodSnapshot(ctx, chatID, env.PeriodStart, env.PeriodEnd, rates)
	if err != nil {
		s.logger.ErrorContext(ctx, "safe_to_spend: remaining obligations snapshot", "err", err)
		return "Временная ошибка — попробуй позже.", nil
	}

	// Фактически потрачено — с начала конверта по сегодня (не позже конца).
	spentTo := time.Now()
	if spentTo.After(env.PeriodEnd) {
		spentTo = env.PeriodEnd
	}
	snapSpent, err := s.store.GetPeriodSnapshot(ctx, chatID, env.PeriodStart, spentTo, rates)
	if err != nil {
		s.logger.ErrorContext(ctx, "safe_to_spend: remaining spent snapshot", "err", err)
		return "Временная ошибка — попробуй позже.", nil
	}
	actualSpentTHB := consumptionSpentTHB(snapSpent.SpentByCategory)

	plannedTHB, _, err := s.store.PlannedExpensesTHB(ctx, chatID, rates)
	if err != nil {
		s.logger.WarnContext(ctx, "safe_to_spend: remaining planned (continuing with 0)", "err", err)
		plannedTHB = 0
	}

	res := computeRemaining(incomeTHB, snapObl, plannedTHB, actualSpentTHB)
	reply := formatRemaining(res, rates["THB"], env)
	s.logger.InfoContext(ctx, "safe_to_spend.remaining",
		"chat_id", chatID, "remaining_thb", res.RemainingTHB, "spent_thb", actualSpentTHB)
	return reply, nil
}

func formatRemaining(r RemainingResult, rubPerTHB float64, env *budget.Envelope) string {
	rub := func(thb float64) float64 { return thb * rubPerTHB }
	var b strings.Builder

	fmt.Fprintf(&b, "🧧 Конверт: приход ~%.0f ₽ (%s — %s)\n\n",
		rub(r.IncomeTHB), env.PeriodStart.Format("02.01"), env.PeriodEnd.Format("02.01"))
	fmt.Fprintf(&b, "  🔁 Регулярные: ~%.0f ₽\n", rub(r.RecurringTHB))
	fmt.Fprintf(&b, "  💳 Долги: ~%.0f ₽\n", rub(r.DebtTHB))
	if r.PlannedTHB > 0 {
		fmt.Fprintf(&b, "  📝 Плановые: ~%.0f ₽\n", rub(r.PlannedTHB))
	}
	fmt.Fprintf(&b, "  🛒 Уже потрачено: ~%.0f ₽\n", rub(r.ActualSpentTHB))
	b.WriteString("  ──────────────────────\n")
	fmt.Fprintf(&b, "  💚 Свободно осталось: ~%.0f ₽\n", rub(r.RemainingTHB))
	return b.String()
}
