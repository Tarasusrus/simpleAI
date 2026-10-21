package budgetskill

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/categories"

	"github.com/google/uuid"
)

func (s *BudgetSkill) addTransaction(ctx context.Context, req budgetInput, typ string) (string, error) {
	if req.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	date := time.Now()
	if req.Date != "" {
		for _, layout := range []string{"2006-01-02", "02.01.2006", "2006-01-02T15:04:05Z07:00"} {
			if d, err := time.Parse(layout, req.Date); err == nil {
				date = d
				break
			}
		}
	}

	t := budget.Transaction{
		ID:          uuid.New(),
		Type:        typ,
		Amount:      req.Amount,
		Currency:    req.Currency,
		Description: req.Description,
		Date:        date,
	}

	var choice categories.Choice
	if s.chooser != nil && typ == "expense" {
		var err error
		choice, err = s.chooser.Choose(ctx, categories.ChooseInput{Name: req.Category, Description: req.Description, Type: typ})
		if err != nil {
			return "", fmt.Errorf("choose category: %w", err)
		}
		if choice.Category.ID != uuid.Nil {
			t.CategoryID = &choice.Category.ID
			t.CategoryName = choice.Category.Name
		}
	} else if req.Category != "" {
		cat, err := s.store.GetOrCreateCategory(ctx, req.Category, typ)
		if err != nil {
			return "", fmt.Errorf("create category: %w", err)
		}
		t.CategoryID = &cat.ID
		t.CategoryName = cat.Name
	}

	if err := s.store.AddTransaction(ctx, t); err != nil {
		return "", fmt.Errorf("add transaction: %w", err)
	}
	s.saveProposal(ctx, choice, t.ID)

	reply := formatTransaction(t, typ)
	if choice.Note != "" {
		reply += "\nℹ️ " + choice.Note
	}
	// Предупреждение о пробитом конверте — ПОВЕРХ обычного ответа и только для
	// расхода: транзакция уже записана и не откатывается (ADR-008, задача 7/8).
	if typ == "expense" {
		reply += s.shareOverspendWarning(ctx, t, req)
	}
	return reply, nil
}

func (s *BudgetSkill) editTransaction(ctx context.Context, req budgetInput) (string, error) {
	if req.TransactionID == "" {
		return "", fmt.Errorf("transaction_id is required")
	}

	tx, err := s.store.GetTransactionByPrefix(ctx, req.TransactionID)
	if err != nil {
		return "", fmt.Errorf("find transaction: %w", err)
	}

	patch := budget.TransactionPatch{}

	if req.Amount > 0 {
		patch.Amount = req.Amount
	}
	if req.Currency != "" {
		patch.Currency = strings.ToUpper(req.Currency)
	}
	if req.Category != "" {
		cat, err := s.resolveNamedCategory(ctx, req.Category, tx.Type, firstNonEmpty(req.Description, tx.Description))
		if err != nil {
			return "", fmt.Errorf("resolve category: %w", err)
		}
		patch.CategoryID = &cat.ID
	}
	if req.Description != "" {
		patch.Description = &req.Description
	}
	if req.Date != "" {
		d, err := time.Parse("2006-01-02", req.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format, use YYYY-MM-DD: %w", err)
		}
		patch.Date = &d
	}

	updated, err := s.store.PatchTransaction(ctx, tx.ID, patch)
	if err != nil {
		return "", fmt.Errorf("edit transaction: %w", err)
	}

	cat := updated.CategoryName
	if cat == "" {
		cat = "без категории"
	}
	icon := "🔴"
	if updated.Type == "income" {
		icon = "🟢"
	}
	return fmt.Sprintf("✏️ Транзакция обновлена:\n%s %.0f %s | %s | %s\n(ID: %s)",
		icon, updated.Amount, currencySymbol(updated.Currency),
		cat, updated.Date.Format("02.01.2006"),
		updated.ID.String()[:8],
	), nil
}

// resolveNamedCategory — категория по имени, названному пользователем. С chooser легаси-источник
// разделения («Еда») уточняется по описанию (ADR-013); без него — прежний GetOrCreate.
func (s *BudgetSkill) resolveNamedCategory(ctx context.Context, name, typ, description string) (categories.Category, error) {
	if s.chooser != nil && typ == "expense" {
		choice, err := s.chooser.Choose(ctx, categories.ChooseInput{Name: name, Description: description, Type: typ})
		return choice.Category, err
	}
	cat, err := s.store.GetOrCreateCategory(ctx, name, typ)
	if err != nil {
		return categories.Category{}, err
	}
	return categories.Category{ID: cat.ID, Name: cat.Name, Type: cat.Type}, nil
}

// saveProposal кладёт в очередь разбора то, что не удалось определить. Сбой записи не ломает
// запись траты: она уже сохранена, а предложение можно потерять без последствий.
func (s *BudgetSkill) saveProposal(ctx context.Context, choice categories.Choice, txID uuid.UUID) {
	if choice.Proposal == nil || s.proposals == nil {
		return
	}
	if err := s.proposals.AddProposal(ctx, *choice.Proposal, txID); err != nil {
		slog.Warn("category proposal not saved", "err", err)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (s *BudgetSkill) listTransactions(ctx context.Context, req budgetInput) (string, error) {
	chatID, _ := ctx.Value(agent.ChatIDKey{}).(int64) //nolint:errcheck // type assertion, not error
	q, err := NormalizeBudgetInput(req, chatID)
	if err != nil {
		return "", err
	}

	txs, err := s.store.ListTransactions(ctx, &q.Filter)
	if err != nil {
		return "", fmt.Errorf("list transactions: %w", err)
	}

	return renderTransactionList(q.Display.PeriodLabel, txs), nil
}

func formatTransaction(t budget.Transaction, typ string) string {
	icon := "🔴"
	label := "Расход"
	if typ == "income" {
		icon = "🟢"
		label = "Доход"
	}
	cat := t.CategoryName
	if cat == "" {
		cat = "без категории"
	}

	cur := currencySymbol(t.Currency)
	result := fmt.Sprintf("%s %s записан: %.0f %s → %s | %s", icon, label, t.Amount, cur, cat, t.Date.Format("02.01.2006"))
	if t.Description != "" {
		result += fmt.Sprintf(" (%s)", t.Description)
	}
	return result
}
