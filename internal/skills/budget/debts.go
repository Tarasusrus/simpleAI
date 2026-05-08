package budgetskill

import (
	"context"
	"fmt"

	"simpleAI/internal/budget"

	"github.com/google/uuid"
)

func (s *BudgetSkill) addDebt(ctx context.Context, req budgetInput) (string, error) {
	if req.Name == "" {
		return "", fmt.Errorf("name is required for debt")
	}
	if req.Total <= 0 {
		return "", fmt.Errorf("total must be positive")
	}

	d := budget.Debt{
		ID:           uuid.New(),
		Name:         req.Name,
		TotalAmount:  req.Total,
		Direction:    req.Direction,
		Counterparty: req.Counterparty,
	}
	if d.Direction == "" {
		d.Direction = "owe"
	}
	if req.Monthly > 0 {
		d.MonthlyPayment = &req.Monthly
	}

	if err := s.store.AddDebt(ctx, d); err != nil {
		return "", fmt.Errorf("add debt: %w", err)
	}

	dir := "Я должен"
	if d.Direction == "owed" {
		dir = "Мне должны"
	}
	result := fmt.Sprintf("📋 Долг записан: %s — %.0f ₽ (%s)", d.Name, d.TotalAmount, dir)
	if d.Counterparty != "" {
		result += fmt.Sprintf("\nКонтрагент: %s", d.Counterparty)
	}
	return result, nil
}

func (s *BudgetSkill) payDebt(ctx context.Context, req budgetInput) (string, error) {
	if req.DebtID == "" {
		return "", fmt.Errorf("debt_id is required")
	}
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	id, err := uuid.Parse(req.DebtID)
	if err != nil {
		return "", fmt.Errorf("invalid debt_id: %w", err)
	}

	if err := s.store.PayDebt(ctx, id, req.Amount); err != nil {
		return "", fmt.Errorf("pay debt: %w", err)
	}

	return fmt.Sprintf("✅ Платёж по долгу: %.0f ₽", req.Amount), nil
}

func (s *BudgetSkill) debtStatus(ctx context.Context) (string, error) {
	debts, err := s.store.ListDebts(ctx)
	if err != nil {
		return "", fmt.Errorf("list debts: %w", err)
	}
	return renderDebtList(debts), nil
}
