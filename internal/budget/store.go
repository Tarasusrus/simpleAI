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

// GetSummary возвращает агрегированную сводку за период, сгруппированную по валютам.
func (s *Store) GetSummary(ctx context.Context, p Period) (*Summary, error) {
	summary := &Summary{Period: p}

	// Общие суммы по валютам.
	totalsRows, err := s.pool.Query(ctx, `
		SELECT
			currency,
			COALESCE(SUM(CASE WHEN type = 'income'  THEN amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type = 'expense' THEN amount ELSE 0 END), 0)
		FROM budget_transaction
		WHERE transaction_date >= $1 AND transaction_date <= $2
		GROUP BY currency
		ORDER BY currency
	`, p.From, p.To)
	if err != nil {
		return nil, fmt.Errorf("get summary totals: %w", err)
	}
	defer totalsRows.Close()

	currencyIndex := map[string]int{}
	for totalsRows.Next() {
		var cg CurrencyGroup
		if err := totalsRows.Scan(&cg.Currency, &cg.TotalIncome, &cg.TotalExpense); err != nil {
			return nil, fmt.Errorf("scan summary totals: %w", err)
		}
		cg.Balance = cg.TotalIncome - cg.TotalExpense
		currencyIndex[cg.Currency] = len(summary.Currencies)
		summary.Currencies = append(summary.Currencies, cg)
	}
	if err := totalsRows.Err(); err != nil {
		return nil, err
	}

	// По категориям (только расходы), с разбивкой по валюте.
	catRows, err := s.pool.Query(ctx, `
		SELECT t.currency, c.id, c.name, c.icon, COALESCE(SUM(t.amount), 0)
		FROM budget_transaction t
		JOIN budget_category c ON c.id = t.category_id
		WHERE t.type = 'expense' AND t.transaction_date >= $1 AND t.transaction_date <= $2
		GROUP BY t.currency, c.id, c.name, c.icon
		ORDER BY t.currency, SUM(t.amount) DESC
	`, p.From, p.To)
	if err != nil {
		return nil, fmt.Errorf("get summary by category: %w", err)
	}
	defer catRows.Close()

	for catRows.Next() {
		var currency string
		var ct CategoryTotal
		if err := catRows.Scan(&currency, &ct.CategoryID, &ct.CategoryName, &ct.Icon, &ct.Total); err != nil {
			return nil, fmt.Errorf("scan category total: %w", err)
		}
		if idx, ok := currencyIndex[currency]; ok {
			summary.Currencies[idx].ByCategory = append(summary.Currencies[idx].ByCategory, ct)
		}
	}
	return summary, catRows.Err()
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

// --- Напоминания ---

// SetReminder сохраняет или обновляет настройки напоминания для пользователя.
func (s *Store) SetReminder(ctx context.Context, r Reminder) error {
	if r.Timezone == "" {
		r.Timezone = "UTC"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO budget_reminder (chat_id, enabled, notify_hour, notify_minute, timezone, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (chat_id) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    notify_hour = EXCLUDED.notify_hour,
		    notify_minute = EXCLUDED.notify_minute,
		    timezone = EXCLUDED.timezone,
		    updated_at = NOW()
	`, r.ChatID, r.Enabled, r.NotifyHour, r.NotifyMinute, r.Timezone)
	if err != nil {
		return fmt.Errorf("set reminder: %w", err)
	}
	return nil
}

// GetReminder возвращает настройки напоминания для пользователя. Ошибка если не найден.
func (s *Store) GetReminder(ctx context.Context, chatID int64) (*Reminder, error) {
	var r Reminder
	err := s.pool.QueryRow(ctx, `
		SELECT chat_id, enabled, notify_hour, notify_minute, timezone
		FROM budget_reminder WHERE chat_id = $1
	`, chatID).Scan(&r.ChatID, &r.Enabled, &r.NotifyHour, &r.NotifyMinute, &r.Timezone)
	if err != nil {
		return nil, fmt.Errorf("get reminder: %w", err)
	}
	return &r, nil
}

// ListActiveReminders возвращает все включённые напоминания.
func (s *Store) ListActiveReminders(ctx context.Context) ([]Reminder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT chat_id, enabled, notify_hour, notify_minute, timezone
		FROM budget_reminder WHERE enabled = true
	`)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()

	var out []Reminder
	for rows.Next() {
		var r Reminder
		if err := rows.Scan(&r.ChatID, &r.Enabled, &r.NotifyHour, &r.NotifyMinute, &r.Timezone); err != nil {
			return nil, fmt.Errorf("scan reminder: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- Повторяющиеся платежи ---

// AddRecurring создаёт повторяющийся платёж.
func (s *Store) AddRecurring(ctx context.Context, r RecurringPayment) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.Currency == "" {
		r.Currency = "RUB"
	}
	if r.Type == "" {
		r.Type = "expense"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO budget_recurring (id, chat_id, name, type, amount, category_id, currency, recurrence_type, day_of_month, next_date, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true)
	`, r.ID, r.ChatID, r.Name, r.Type, r.Amount, r.CategoryID, r.Currency, r.RecurrenceType, r.DayOfMonth, r.NextDate)
	if err != nil {
		return fmt.Errorf("add recurring: %w", err)
	}
	return nil
}

// ListRecurring возвращает все повторяющиеся платежи для чата (включая отключённые).
func (s *Store) ListRecurring(ctx context.Context, chatID int64) ([]RecurringPayment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.chat_id, r.name, r.type, r.amount, r.category_id, COALESCE(c.name, ''), r.currency,
		       r.recurrence_type, r.day_of_month, r.next_date, r.enabled, r.created_at, r.updated_at
		FROM budget_recurring r
		LEFT JOIN budget_category c ON c.id = r.category_id
		WHERE r.chat_id = $1
		ORDER BY r.enabled DESC, r.next_date
	`, chatID)
	if err != nil {
		return nil, fmt.Errorf("list recurring: %w", err)
	}
	defer rows.Close()

	var out []RecurringPayment
	for rows.Next() {
		var r RecurringPayment
		if err := rows.Scan(&r.ID, &r.ChatID, &r.Name, &r.Type, &r.Amount, &r.CategoryID, &r.CategoryName,
			&r.Currency, &r.RecurrenceType, &r.DayOfMonth, &r.NextDate, &r.Enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan recurring: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetDueRecurring возвращает включённые платежи, чья next_date <= date.
func (s *Store) GetDueRecurring(ctx context.Context, date time.Time) ([]RecurringPayment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.chat_id, r.name, r.type, r.amount, r.category_id, COALESCE(c.name, ''), r.currency,
		       r.recurrence_type, r.day_of_month, r.next_date, r.enabled, r.created_at, r.updated_at
		FROM budget_recurring r
		LEFT JOIN budget_category c ON c.id = r.category_id
		WHERE r.enabled = true AND r.next_date <= $1
	`, date)
	if err != nil {
		return nil, fmt.Errorf("get due recurring: %w", err)
	}
	defer rows.Close()

	var out []RecurringPayment
	for rows.Next() {
		var r RecurringPayment
		if err := rows.Scan(&r.ID, &r.ChatID, &r.Name, &r.Type, &r.Amount, &r.CategoryID, &r.CategoryName,
			&r.Currency, &r.RecurrenceType, &r.DayOfMonth, &r.NextDate, &r.Enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan due recurring: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateRecurringTransaction атомарно создаёт транзакцию и сдвигает next_date в одной DB-транзакции.
func (s *Store) CreateRecurringTransaction(ctx context.Context, t Transaction, recurringID uuid.UUID, nextDate time.Time) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.Currency == "" {
		t.Currency = "RUB"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Dedup-guard: не создаём дубль если уже есть такая же транзакция за последние 60 секунд.
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM budget_transaction
			WHERE type = $1 AND amount = $2 AND currency = $3
			  AND category_id IS NOT DISTINCT FROM $4
			  AND description IS NOT DISTINCT FROM $5
			  AND transaction_date = $6
			  AND created_at >= NOW() - INTERVAL '60 seconds'
		)
	`, t.Type, t.Amount, t.Currency, t.CategoryID, t.Description, t.Date).Scan(&exists); err != nil {
		return fmt.Errorf("dedup check: %w", err)
	}
	if !exists {
		if _, err := tx.Exec(ctx, `
			INSERT INTO budget_transaction (id, type, amount, currency, category_id, description, transaction_date)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, t.ID, t.Type, t.Amount, t.Currency, t.CategoryID, t.Description, t.Date); err != nil {
			return fmt.Errorf("insert transaction: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE budget_recurring SET next_date = $2, updated_at = NOW() WHERE id = $1
	`, recurringID, nextDate); err != nil {
		return fmt.Errorf("advance next_date: %w", err)
	}

	return tx.Commit(ctx)
}

// AdvanceRecurringNextDate сдвигает next_date на следующий период и обновляет updated_at.
func (s *Store) AdvanceRecurringNextDate(ctx context.Context, id uuid.UUID, nextDate time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE budget_recurring SET next_date = $2, updated_at = NOW() WHERE id = $1
	`, id, nextDate)
	if err != nil {
		return fmt.Errorf("advance recurring next_date: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("recurring %s not found", id)
	}
	return nil
}

// DisableRecurringByPrefix отключает повторяющийся платёж по префиксу UUID (enabled = false).
func (s *Store) DisableRecurringByPrefix(ctx context.Context, prefix string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE budget_recurring SET enabled = false, updated_at = NOW()
		WHERE CAST(id AS TEXT) LIKE $1
	`, prefix+"%")
	if err != nil {
		return fmt.Errorf("disable recurring: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("recurring with prefix %q not found", prefix)
	}
	return nil
}

// DeleteRecurring удаляет повторяющийся платёж полностью.
func (s *Store) DeleteRecurring(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM budget_recurring WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete recurring: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("recurring %s not found", id)
	}
	return nil
}

// --- Прогнозирование ---

// GetForecastData возвращает прогноз трат по категориям на основе последних months месяцев.
// Учитываются только расходы (type = 'expense').
// Если months <= 0 — берёт все доступные данные.
func (s *Store) GetForecastData(ctx context.Context, months int) ([]CategoryForecast, error) {
	var fromClause string
	var args []any

	if months > 0 {
		fromClause = `AND t.transaction_date >= date_trunc('month', NOW()) - ($1 * INTERVAL '1 month')`
		args = append(args, months)
	}

	// Агрегируем траты по (категория, валюта, месяц).
	// Из этого считаем: среднее, last_month, prev_month.
	query := fmt.Sprintf(`
		WITH monthly AS (
			SELECT
				COALESCE(c.name, 'Прочее')                          AS category_name,
				COALESCE(c.icon, '📦')                              AS icon,
				t.currency,
				date_trunc('month', t.transaction_date)             AS month,
				SUM(t.amount)                                       AS total
			FROM budget_transaction t
			LEFT JOIN budget_category c ON c.id = t.category_id
			WHERE t.type = 'expense'
			%s
			GROUP BY category_name, icon, t.currency, month
		),
		ranked AS (
			SELECT *,
				ROW_NUMBER() OVER (PARTITION BY category_name, currency ORDER BY month DESC) AS rn,
				AVG(total)   OVER (PARTITION BY category_name, currency)                     AS avg_total,
				COUNT(*)     OVER (PARTITION BY category_name, currency)                     AS month_count
			FROM monthly
		)
		SELECT
			category_name,
			icon,
			currency,
			avg_total,
			MAX(CASE WHEN rn = 1 THEN total END) AS last_month,
			MAX(CASE WHEN rn = 2 THEN total END) AS prev_month,
			MAX(month_count)                      AS month_count
		FROM ranked
		GROUP BY category_name, icon, currency, avg_total
		ORDER BY avg_total DESC
	`, fromClause)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get forecast data: %w", err)
	}
	defer rows.Close()

	var out []CategoryForecast
	for rows.Next() {
		var f CategoryForecast
		var lastMonth, prevMonth *float64
		var monthCount int
		if err := rows.Scan(&f.CategoryName, &f.Icon, &f.Currency, &f.ForecastAmount,
			&lastMonth, &prevMonth, &monthCount); err != nil {
			return nil, fmt.Errorf("scan forecast: %w", err)
		}
		if monthCount >= 2 && lastMonth != nil && prevMonth != nil && *prevMonth > 0 {
			f.TrendPct = (*lastMonth - *prevMonth) / *prevMonth * 100
			f.HasTrend = true
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// --- Курсы валют ---

// GetExchangeRates возвращает все курсы из БД как map[currency]rate_to_rub.
func (s *Store) GetExchangeRates(ctx context.Context) (map[string]float64, error) {
	rows, err := s.pool.Query(ctx, `SELECT currency, rate_to_rub FROM exchange_rate`)
	if err != nil {
		return nil, fmt.Errorf("get exchange rates: %w", err)
	}
	defer rows.Close()

	rates := map[string]float64{}
	for rows.Next() {
		var currency string
		var rate float64
		if err := rows.Scan(&currency, &rate); err != nil {
			return nil, fmt.Errorf("scan exchange rate: %w", err)
		}
		rates[currency] = rate
	}
	return rates, rows.Err()
}

// SaveExchangeRate сохраняет или обновляет курс валюты.
func (s *Store) SaveExchangeRate(ctx context.Context, currency string, rateToRUB float64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO exchange_rate (currency, rate_to_rub, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (currency) DO UPDATE
		SET rate_to_rub = EXCLUDED.rate_to_rub, updated_at = NOW()
	`, currency, rateToRUB)
	if err != nil {
		return fmt.Errorf("save exchange rate: %w", err)
	}
	return nil
}
