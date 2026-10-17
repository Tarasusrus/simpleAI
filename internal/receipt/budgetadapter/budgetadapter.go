// Package budgetadapter связывает сценарий чека с бюджетом: запись расхода
// через budget skill и список категорий расхода из хранилища.
package budgetadapter

import (
	"context"
	"encoding/json"
	"fmt"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/plugin"
	"simpleAI/internal/receipt"
)

// SkillWriter пишет расход вызовом budget skill — тем же путём, которым
// агент вызывает навык, но без LLM: подтверждённые цифры не пересказываются
// моделью, а предупреждение о пробитом конверте (ADR-008) остаётся в ответе.
// Чат навык берёт из контекста, как при вызове агентом: без него
// предупреждение о конверте молча не считается.
type SkillWriter struct {
	skill plugin.Skill
}

func NewSkillWriter(skill plugin.Skill) *SkillWriter {
	return &SkillWriter{skill: skill}
}

type addExpenseInput struct {
	Action      string  `json:"action"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Category    string  `json:"category,omitempty"`
	Description string  `json:"description,omitempty"`
	Date        string  `json:"date,omitempty"`
}

func (w *SkillWriter) WriteExpense(ctx context.Context, chatID int64, r receipt.Receipt) (string, error) {
	in, err := json.Marshal(addExpenseInput{
		Action:      "add_expense",
		Amount:      r.Total,
		Currency:    r.Currency,
		Category:    r.Category,
		Description: r.Merchant,
		Date:        r.Date,
	})
	if err != nil {
		return "", fmt.Errorf("budgetadapter: %w", err)
	}
	return w.skill.Run(context.WithValue(ctx, agent.ChatIDKey{}, chatID), string(in))
}

// CategorySource — часть budget.Store, которая нужна для списка категорий.
type CategorySource interface {
	ListCategories(ctx context.Context) ([]budget.Category, error)
}

// Categories — receipt.CategoryLister: только категории расхода.
type Categories struct {
	src CategorySource
}

func NewCategories(src CategorySource) *Categories {
	return &Categories{src: src}
}

func (c *Categories) ExpenseCategories(ctx context.Context) ([]string, error) {
	all, err := c.src.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, cat := range all {
		if cat.Type == "expense" {
			out = append(out, cat.Name)
		}
	}
	return out, nil
}
