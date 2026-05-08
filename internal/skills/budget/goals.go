package budgetskill

import (
	"context"
	"fmt"
	"time"

	"simpleAI/internal/budget"

	"github.com/google/uuid"
)

func (s *BudgetSkill) addGoal(ctx context.Context, req budgetInput) (string, error) {
	if req.Name == "" {
		return "", fmt.Errorf("name is required for goal")
	}
	if req.TargetAmount <= 0 {
		return "", fmt.Errorf("target_amount must be positive")
	}

	g := budget.Goal{
		ID:           uuid.New(),
		Name:         req.Name,
		TargetAmount: req.TargetAmount,
	}

	if req.Deadline != "" {
		if d, err := time.Parse("2006-01-02", req.Deadline); err == nil {
			g.Deadline = &d
		}
	}

	if err := s.store.AddGoal(ctx, g); err != nil {
		return "", fmt.Errorf("add goal: %w", err)
	}

	result := fmt.Sprintf("🎯 Цель создана: %s — %.0f ₽", g.Name, g.TargetAmount)
	if g.Deadline != nil {
		months := monthsUntil(*g.Deadline)
		if months > 0 {
			monthly := g.TargetAmount / float64(months)
			result += fmt.Sprintf("\nДедлайн: %s (нужно откладывать ~%.0f ₽/мес)",
				g.Deadline.Format("02.01.2006"), monthly)
		}
	}
	return result, nil
}

func (s *BudgetSkill) updateGoal(ctx context.Context, req budgetInput) (string, error) {
	if req.GoalID == "" {
		return "", fmt.Errorf("goal_id is required")
	}
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	id, err := uuid.Parse(req.GoalID)
	if err != nil {
		return "", fmt.Errorf("invalid goal_id: %w", err)
	}

	if err := s.store.UpdateGoalProgress(ctx, id, req.Amount); err != nil {
		return "", fmt.Errorf("update goal: %w", err)
	}

	return fmt.Sprintf("✅ Пополнение цели: +%.0f ₽", req.Amount), nil
}

func (s *BudgetSkill) goalStatus(ctx context.Context) (string, error) {
	goals, err := s.store.ListGoals(ctx)
	if err != nil {
		return "", fmt.Errorf("list goals: %w", err)
	}
	return renderGoalList(goals), nil
}
