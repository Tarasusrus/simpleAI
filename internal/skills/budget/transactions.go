package budgetskill

import (
	"context"
	"fmt"
	"strings"
	"time"

	"simpleAI/internal/budget"

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

	if req.Category != "" {
		cat, err := s.store.FindCategoryByName(ctx, req.Category, typ)
		if err != nil {
			cat, err = s.store.AddCategory(ctx, req.Category, typ)
			if err != nil {
				return "", fmt.Errorf("create category: %w", err)
			}
		}
		t.CategoryID = &cat.ID
		t.CategoryName = cat.Name
	}

	if err := s.store.AddTransaction(ctx, t); err != nil {
		return "", fmt.Errorf("add transaction: %w", err)
	}

	return formatTransaction(t, typ), nil
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
		cat, err := s.store.FindCategoryByName(ctx, req.Category, tx.Type)
		if err != nil {
			cat, err = s.store.AddCategory(ctx, req.Category, tx.Type)
			if err != nil {
				return "", fmt.Errorf("resolve category: %w", err)
			}
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

func (s *BudgetSkill) listTransactions(ctx context.Context, req budgetInput) (string, error) {
	var p budget.Period
	var periodLabel string

	// LLM иногда кладёт конкретную дату в period вместо date — исправляем.
	if req.Date == "" && req.Period != "" {
		for _, layout := range []string{"2006-01-02", "02.01.2006"} {
			if _, err := time.Parse(layout, req.Period); err == nil {
				req.Date = req.Period
				req.Period = ""
				break
			}
		}
	}

	rangeMode := false
	switch {
	case req.Date != "":
		day := parseFlexibleDate(req.Date)
		if day.IsZero() {
			return "", fmt.Errorf("неверный формат даты %q, используй YYYY-MM-DD или DD.MM.YYYY", req.Date)
		}
		p = budget.Period{
			From: time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC),
			To:   time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, time.UTC),
		}
		periodLabel = day.Format("02.01.2006")
	case req.DateFrom != "" || req.DateTo != "":
		from := parseFlexibleDate(req.DateFrom)
		to := parseFlexibleDate(req.DateTo)
		if from.IsZero() || to.IsZero() {
			return "", fmt.Errorf("неверный формат date_from/date_to, используй YYYY-MM-DD или DD.MM.YYYY")
		}
		if to.Before(from) {
			from, to = to, from
		}
		p = budget.Period{
			From: time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC),
			To:   time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 0, time.UTC),
		}
		periodLabel = fmt.Sprintf("%s — %s", from.Format("02.01.2006"), to.Format("02.01.2006"))
		rangeMode = true
	default:
		p = parsePeriod(req.Period)
		periodLabel = formatPeriodName(p)
	}

	limit := 20
	if rangeMode {
		limit = 200
	}
	f := budget.TransactionFilter{
		Period:  &p,
		Keyword: req.Keyword,
		Type:    strings.ToLower(strings.TrimSpace(req.TransactionType)),
		Limit:   limit,
	}
	if f.Type != "income" && f.Type != "expense" {
		f.Type = ""
	}

	txs, err := s.store.ListTransactions(ctx, f)
	if err != nil {
		return "", fmt.Errorf("list transactions: %w", err)
	}

	if len(txs) == 0 {
		return fmt.Sprintf("Транзакций за %s нет.", periodLabel), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Транзакции за %s:\n\n", periodLabel)
	for _, t := range txs {
		icon := "🔴"
		sign := "-"
		if t.Type == "income" {
			icon = "🟢"
			sign = "+"
		}
		cat := t.CategoryName
		if cat == "" {
			cat = "без категории"
		}
		desc := t.Description
		if desc != "" {
			desc = " — " + desc
		}
		fmt.Fprintf(&sb, "[%s] %s %s%.0f %s | %s%s | %s\n",
			t.ID.String()[:8], icon, sign, t.Amount, currencySymbol(t.Currency), cat, desc, t.Date.Format("02.01"))
	}
	return sb.String(), nil
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
