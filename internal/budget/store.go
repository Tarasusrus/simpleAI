package budget

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store предоставляет CRUD-операции для бюджетных данных.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore создаёт Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// --- Категории ---

// ListCategories возвращает все категории, отсортированные по типу и порядку.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, type, icon, sort_order
		FROM budget_category
		ORDER BY type, sort_order
	`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Icon, &c.SortOrder); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FindCategoryByName ищет категорию по имени (case-insensitive) и типу.
func (s *Store) FindCategoryByName(ctx context.Context, name string, typ string) (*Category, error) {
	var c Category
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, type, icon, sort_order
		FROM budget_category
		WHERE LOWER(name) = LOWER($1) AND type = $2
	`, strings.TrimSpace(name), typ).Scan(&c.ID, &c.Name, &c.Type, &c.Icon, &c.SortOrder)
	if err != nil {
		return nil, fmt.Errorf("find category %q: %w", name, err)
	}
	return &c, nil
}

// AddCategory создаёт пользовательскую категорию.
func (s *Store) AddCategory(ctx context.Context, name string, typ string) (*Category, error) {
	c := Category{
		ID:   uuid.New(),
		Name: strings.TrimSpace(name),
		Type: typ,
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO budget_category (id, name, type, icon, sort_order)
		VALUES ($1, $2, $3, '', 100)
	`, c.ID, c.Name, c.Type)
	if err != nil {
		return nil, fmt.Errorf("add category: %w", err)
	}
	return &c, nil
}

// --- Транзакции ---

// AddTransaction записывает доход или расход.
// Защита от дублей: если за последние 60 секунд уже существует запись
// с теми же type, amount, currency, category_id, description и transaction_date — пропускаем INSERT.
func (s *Store) AddTransaction(ctx context.Context, t Transaction) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.Currency == "" {
		t.Currency = "RUB"
	}

	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM budget_transaction
			WHERE type = $1
			  AND amount = $2
			  AND currency = $3
			  AND category_id IS NOT DISTINCT FROM $4
			  AND description IS NOT DISTINCT FROM $5
			  AND transaction_date = $6
			  AND created_at >= NOW() - INTERVAL '60 seconds'
		)
	`, t.Type, t.Amount, t.Currency, t.CategoryID, t.Description, t.Date).Scan(&exists)
	if err != nil {
		return fmt.Errorf("dedup check: %w", err)
	}
	if exists {
		return nil
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO budget_transaction (id, type, amount, currency, category_id, description, transaction_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, t.ID, t.Type, t.Amount, t.Currency, t.CategoryID, t.Description, t.Date)
	if err != nil {
		return fmt.Errorf("add transaction: %w", err)
	}
	return nil
}

// ListTransactions возвращает транзакции по фильтру.
func (s *Store) ListTransactions(ctx context.Context, f TransactionFilter) ([]Transaction, error) {
	var conditions []string
	var args []any
	argN := 1

	if f.Period != nil {
		conditions = append(conditions, fmt.Sprintf("t.transaction_date >= $%d AND t.transaction_date <= $%d", argN, argN+1))
		args = append(args, f.Period.From, f.Period.To)
		argN += 2
	}
	if f.CategoryID != nil {
		conditions = append(conditions, fmt.Sprintf("t.category_id = $%d", argN))
		args = append(args, *f.CategoryID)
		argN++
	}
	if f.Type != "" {
		conditions = append(conditions, fmt.Sprintf("t.type = $%d", argN))
		args = append(args, f.Type)
		argN++
	}
	if f.Keyword != "" {
		conditions = append(conditions, fmt.Sprintf("t.description ILIKE $%d", argN))
		args = append(args, "%"+f.Keyword+"%")
		argN++
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	limitClause := fmt.Sprintf("LIMIT $%d", argN)
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT t.id, t.type, t.amount, t.currency, t.category_id, COALESCE(c.name, ''), t.description, t.transaction_date, t.created_at
		FROM budget_transaction t
		LEFT JOIN budget_category c ON c.id = t.category_id
		%s
		ORDER BY t.transaction_date DESC, t.created_at DESC
		%s
	`, where, limitClause)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Type, &t.Amount, &t.Currency, &t.CategoryID, &t.CategoryName, &t.Description, &t.Date, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTransaction удаляет транзакцию по ID.
func (s *Store) DeleteTransaction(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM budget_transaction WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("transaction %s not found", id)
	}
	return nil
}

// --- Сводка ---

// GetSummary возвращает агрегированную сводку за период.
func (s *Store) GetSummary(ctx context.Context, p Period) (*Summary, error) {
	summary := &Summary{Period: p}

	// Общие суммы.
	err := s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN type = 'income' THEN amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type = 'expense' THEN amount ELSE 0 END), 0)
		FROM budget_transaction
		WHERE transaction_date >= $1 AND transaction_date <= $2
	`, p.From, p.To).Scan(&summary.TotalIncome, &summary.TotalExpense)
	if err != nil {
		return nil, fmt.Errorf("get summary totals: %w", err)
	}
	summary.Balance = summary.TotalIncome - summary.TotalExpense

	// По категориям (только расходы).
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.icon, COALESCE(SUM(t.amount), 0)
		FROM budget_transaction t
		JOIN budget_category c ON c.id = t.category_id
		WHERE t.type = 'expense' AND t.transaction_date >= $1 AND t.transaction_date <= $2
		GROUP BY c.id, c.name, c.icon
		ORDER BY SUM(t.amount) DESC
	`, p.From, p.To)
	if err != nil {
		return nil, fmt.Errorf("get summary by category: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ct CategoryTotal
		if err := rows.Scan(&ct.CategoryID, &ct.CategoryName, &ct.Icon, &ct.Total); err != nil {
			return nil, fmt.Errorf("scan category total: %w", err)
		}
		summary.ByCategory = append(summary.ByCategory, ct)
	}
	return summary, rows.Err()
}

// --- Цели ---

// AddGoal создаёт цель накопления.
func (s *Store) AddGoal(ctx context.Context, g Goal) error {
	if g.ID == uuid.Nil {
		g.ID = uuid.New()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO budget_goal (id, name, target_amount, current_amount, deadline, status)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, g.ID, g.Name, g.TargetAmount, g.CurrentAmount, g.Deadline, "active")
	if err != nil {
		return fmt.Errorf("add goal: %w", err)
	}
	return nil
}

// UpdateGoalProgress пополняет цель на указанную сумму.
func (s *Store) UpdateGoalProgress(ctx context.Context, id uuid.UUID, amount float64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE budget_goal
		SET current_amount = LEAST(current_amount + $2, target_amount),
		    status = CASE WHEN current_amount + $2 >= target_amount THEN 'completed' ELSE status END,
		    updated_at = $3
		WHERE id = $1 AND status = 'active'
	`, id, amount, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("update goal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("goal %s not found or not active", id)
	}
	return nil
}

// ListGoals возвращает все цели.
func (s *Store) ListGoals(ctx context.Context) ([]Goal, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, target_amount, current_amount, deadline, status, created_at, updated_at
		FROM budget_goal
		ORDER BY status, created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	defer rows.Close()

	var out []Goal
	for rows.Next() {
		var g Goal
		if err := rows.Scan(&g.ID, &g.Name, &g.TargetAmount, &g.CurrentAmount, &g.Deadline, &g.Status, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan goal: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// --- Долги ---

// AddDebt создаёт запись о долге.
func (s *Store) AddDebt(ctx context.Context, d Debt) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO budget_debt (id, name, total_amount, paid_amount, monthly_payment, direction, counterparty, status, due_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, d.ID, d.Name, d.TotalAmount, d.PaidAmount, d.MonthlyPayment, d.Direction, d.Counterparty, "active", d.DueDate)
	if err != nil {
		return fmt.Errorf("add debt: %w", err)
	}
	return nil
}

// PayDebt вносит платёж по долгу.
func (s *Store) PayDebt(ctx context.Context, id uuid.UUID, amount float64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE budget_debt
		SET paid_amount = LEAST(paid_amount + $2, total_amount),
		    status = CASE WHEN paid_amount + $2 >= total_amount THEN 'paid' ELSE status END,
		    updated_at = $3
		WHERE id = $1 AND status = 'active'
	`, id, amount, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("pay debt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("debt %s not found or already paid", id)
	}
	return nil
}

// ListDebts возвращает все долги.
func (s *Store) ListDebts(ctx context.Context) ([]Debt, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, total_amount, paid_amount, monthly_payment, direction, counterparty, status, due_date, created_at, updated_at
		FROM budget_debt
		ORDER BY status, created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list debts: %w", err)
	}
	defer rows.Close()

	var out []Debt
	for rows.Next() {
		var d Debt
		if err := rows.Scan(&d.ID, &d.Name, &d.TotalAmount, &d.PaidAmount, &d.MonthlyPayment, &d.Direction, &d.Counterparty, &d.Status, &d.DueDate, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan debt: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- Редактирование транзакций ---

// GetTransactionByPrefix находит транзакцию по первым символам UUID.
func (s *Store) GetTransactionByPrefix(ctx context.Context, prefix string) (*Transaction, error) {
	var t Transaction
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.type, t.amount, t.currency, t.category_id, COALESCE(c.name, ''), t.description, t.transaction_date, t.created_at
		FROM budget_transaction t
		LEFT JOIN budget_category c ON c.id = t.category_id
		WHERE CAST(t.id AS TEXT) LIKE $1
		LIMIT 1
	`, prefix+"%").Scan(&t.ID, &t.Type, &t.Amount, &t.Currency, &t.CategoryID, &t.CategoryName, &t.Description, &t.Date, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("find transaction by prefix %q: %w", prefix, err)
	}
	return &t, nil
}

// PatchTransaction обновляет только переданные поля транзакции и возвращает обновлённую запись.
func (s *Store) PatchTransaction(ctx context.Context, id uuid.UUID, p TransactionPatch) (*Transaction, error) {
	var setClauses []string
	var args []any
	argN := 1

	if p.Amount > 0 {
		setClauses = append(setClauses, fmt.Sprintf("amount = $%d", argN))
		args = append(args, p.Amount)
		argN++
	}
	if p.Currency != "" {
		setClauses = append(setClauses, fmt.Sprintf("currency = $%d", argN))
		args = append(args, p.Currency)
		argN++
	}
	if p.CategoryID != nil {
		if *p.CategoryID == uuid.Nil {
			setClauses = append(setClauses, "category_id = NULL")
		} else {
			setClauses = append(setClauses, fmt.Sprintf("category_id = $%d", argN))
			args = append(args, *p.CategoryID)
			argN++
		}
	}
	if p.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argN))
		args = append(args, *p.Description)
		argN++
	}
	if p.Date != nil {
		setClauses = append(setClauses, fmt.Sprintf("transaction_date = $%d", argN))
		args = append(args, *p.Date)
		argN++
	}

	if len(setClauses) == 0 {
		return nil, fmt.Errorf("patch transaction: no fields to update")
	}

	args = append(args, id)
	query := fmt.Sprintf(
		`UPDATE budget_transaction SET %s WHERE id = $%d`,
		strings.Join(setClauses, ", "), argN,
	)
	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("patch transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("transaction %s not found", id)
	}

	return s.GetTransactionByPrefix(ctx, id.String()[:8])
}
