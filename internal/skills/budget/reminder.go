package budgetskill

import (
	"context"
	"fmt"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
)

func (s *BudgetSkill) setReminder(ctx context.Context, req budgetInput) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	_ = ok
	if chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}

	r := budget.Reminder{
		ChatID:       chatID,
		Enabled:      true,
		NotifyHour:   21,
		NotifyMinute: 0,
		Timezone:     "UTC",
	}

	if req.ReminderEnabled != nil {
		r.Enabled = *req.ReminderEnabled
	}
	if req.ReminderHour != nil {
		r.NotifyHour = *req.ReminderHour
	}
	if req.ReminderMinute != nil {
		r.NotifyMinute = *req.ReminderMinute
	}
	if req.ReminderTimezone != "" {
		r.Timezone = req.ReminderTimezone
	}

	if err := s.store.SetReminder(ctx, r); err != nil {
		return fmt.Sprintf("Не удалось сохранить напоминание (%v). Попробуй ещё раз.", err), nil
	}

	if !r.Enabled {
		return "🔕 Ежедневные напоминания отключены.", nil
	}
	return fmt.Sprintf("🔔 Напоминание настроено: каждый день в %02d:%02d (%s) бот напомнит внести покупки.", r.NotifyHour, r.NotifyMinute, r.Timezone), nil
}

func (s *BudgetSkill) getReminder(ctx context.Context) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	_ = ok
	if chatID == 0 {
		return "Напоминания не настроены. Скажи «включи напоминания в 21:00» чтобы настроить.", nil
	}

	r, err := s.store.GetReminder(ctx, chatID)
	if err != nil {
		return "Напоминания не настроены. Скажи «включи напоминания в 21:00» чтобы настроить.", nil
	}

	if !r.Enabled {
		return "🔕 Напоминания отключены.", nil
	}
	return fmt.Sprintf("🔔 Напоминание активно: каждый день в %02d:%02d (%s).", r.NotifyHour, r.NotifyMinute, r.Timezone), nil
}
