package budgetskill

import (
	"context"
	"fmt"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"

	"github.com/google/uuid"
)

func (s *BudgetSkill) addRecurring(ctx context.Context, req budgetInput) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	_ = ok
	if chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}
	if req.Name == "" {
		return "", fmt.Errorf("name is required for recurring payment")
	}
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	currency := req.Currency
	if currency == "" {
		currency = "RUB"
	}

	txType := req.TransactionType
	if txType != "income" {
		txType = "expense"
	}

	var catID *uuid.UUID
	if req.Category != "" {
		cat, err := s.store.FindCategoryByName(ctx, req.Category, txType)
		if err != nil {
			cat, err = s.store.AddCategory(ctx, req.Category, txType)
		}
		if err == nil {
			catID = &cat.ID
		}
	}

	now := time.Now().UTC()
	var nextDate time.Time
	dayOfMonth := 1
	if req.DayOfMonth != nil {
		dayOfMonth = *req.DayOfMonth
	}
	candidate := time.Date(now.Year(), now.Month(), dayOfMonth, 0, 0, 0, 0, time.UTC)
	if !candidate.Before(now.Truncate(24 * time.Hour)) {
		nextDate = candidate
	} else {
		nextDate = candidate.AddDate(0, 1, 0)
	}

	r := budget.RecurringPayment{
		ChatID:         chatID,
		Name:           req.Name,
		Type:           txType,
		Amount:         req.Amount,
		CategoryID:     catID,
		Currency:       currency,
		RecurrenceType: "monthly",
		DayOfMonth:     &dayOfMonth,
		NextDate:       nextDate,
		Enabled:        true,
	}

	if err := s.store.AddRecurring(ctx, r); err != nil {
		return fmt.Sprintf("Не удалось сохранить повторяющийся платёж: %v", err), nil
	}

	return fmt.Sprintf("🔄 Повторяющийся платёж добавлен: *%s* — %.0f %s каждое %d-е число месяца. Первое списание: %s.",
		req.Name, req.Amount, currency, dayOfMonth, nextDate.Format("02.01.2006")), nil
}

func (s *BudgetSkill) listRecurring(ctx context.Context) (string, error) {
	chatID, ok := ctx.Value(agent.ChatIDKey{}).(int64)
	_ = ok
	if chatID == 0 {
		return "Не удалось определить чат — попробуй ещё раз.", nil
	}

	list, err := s.store.ListRecurring(ctx, chatID)
	if err != nil {
		return fmt.Sprintf("Не удалось загрузить список: %v", err), nil
	}
	return renderRecurringList(list), nil
}

func (s *BudgetSkill) disableRecurring(ctx context.Context, req budgetInput) (string, error) {
	if req.RecurringID == "" {
		return "", fmt.Errorf("recurring_id is required")
	}

	if err := s.store.DisableRecurringByPrefix(ctx, req.RecurringID); err != nil {
		return fmt.Sprintf("Не удалось отключить: %v", err), nil
	}
	return "⏸ Повторяющийся платёж отключён.", nil
}
