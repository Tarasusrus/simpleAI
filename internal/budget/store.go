package budget

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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
func (s *Store) FindCategoryByName(ctx context.Context, name, typ string) (*Category, error) {
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
func (s *Store) AddCategory(ctx context.Context, name, typ string) (*Category, error) {
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
// Фильтр должен быть провалидирован вызывающим кодом (f.Validate()).
func (s *Store) ListTransactions(ctx context.Context, f *TransactionFilter) ([]Transaction, error) {
	var conds []string
	var args []any
	n := 1

	if f.DateRange != nil {
		conds = append(conds, fmt.Sprintf("t.transaction_date >= $%d AND t.transaction_date <= $%d", n, n+1))
		args = append(args, f.DateRange.From, f.DateRange.To)
		n += 2
	}

	if len(f.Directions) == 1 {
		conds = append(conds, fmt.Sprintf("t.type = $%d", n))
		args = append(args, string(f.Directions[0]))
		n++
	} else if len(f.Directions) > 1 {
		placeholders := make([]string, len(f.Directions))
		for i, d := range f.Directions {
			placeholders[i] = fmt.Sprintf("$%d", n)
			args = append(args, string(d))
			n++
		}
		conds = append(conds, "t.type IN ("+strings.Join(placeholders, ", ")+")")
	}

	if len(f.CategoryIDs) == 1 {
		conds = append(conds, fmt.Sprintf("t.category_id = $%d", n))
		args = append(args, f.CategoryIDs[0])
		n++
	} else if len(f.CategoryIDs) > 1 {
		placeholders := make([]string, len(f.CategoryIDs))
		for i, id := range f.CategoryIDs {
			placeholders[i] = fmt.Sprintf("$%d", n)
			args = append(args, id)
			n++
		}
		conds = append(conds, "t.category_id IN ("+strings.Join(placeholders, ", ")+")")
	}

	if len(f.Currencies) == 1 {
		conds = append(conds, fmt.Sprintf("t.currency = $%d", n))
		args = append(args, string(f.Currencies[0]))
		n++
	} else if len(f.Currencies) > 1 {
		placeholders := make([]string, len(f.Currencies))
		for i, c := range f.Currencies {
			placeholders[i] = fmt.Sprintf("$%d", n)
			args = append(args, string(c))
			n++
		}
		conds = append(conds, "t.currency IN ("+strings.Join(placeholders, ", ")+")")
	}

	if f.AmountRange != nil {
		conds = append(conds, fmt.Sprintf("t.amount >= $%d AND t.amount <= $%d", n, n+1))
		args = append(args, f.AmountRange.Min, f.AmountRange.Max)
		n += 2
	}

	if f.Search != nil {
		conds = append(conds, fmt.Sprintf("t.description ILIKE $%d", n))
		args = append(args, "%"+*f.Search+"%")
		n++
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	sortBy := SortByDate
	switch f.SortBy {
	case SortByDate, "":
		// default
	case SortByAmount, SortByCreatedAt:
		sortBy = f.SortBy
	}
	sortDir := SortDesc
	if f.SortDir == SortAsc {
		sortDir = SortAsc
	}
	orderBy := fmt.Sprintf("ORDER BY t.%s %s", string(sortBy), string(sortDir))
	if sortBy != SortByDate {
		orderBy += fmt.Sprintf(", t.%s %s", SortByDate, SortDesc)
	}

	args = append(args, f.Pagination.Limit, f.Pagination.Offset)
	limitOffsetClause := fmt.Sprintf("LIMIT $%d OFFSET $%d", n, n+1)

	query := fmt.Sprintf(`
		SELECT t.id, t.recurring_id, t.type, t.amount, t.currency, t.category_id, COALESCE(c.name, ''), t.description, t.transaction_date, t.created_at
		FROM budget_transaction t
		LEFT JOIN budget_category c ON c.id = t.category_id
		%s
		%s
		%s
	`, where, orderBy, limitOffsetClause)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.RecurringID, &t.Type, &t.Amount, &t.Currency, &t.CategoryID, &t.CategoryName, &t.Description, &t.Date, &t.CreatedAt); err != nil {
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

// EarliestTransactionDate возвращает дату первой транзакции ledger'а.
// Второй результат false — транзакций ещё нет (пустой ledger).
// Дайджест использует это как гейт возраста: молодой ledger не даёт
// достоверного 30-дневного бэйзлайна для аномалий.
func (s *Store) EarliestTransactionDate(ctx context.Context) (time.Time, bool, error) {
	var earliest *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT MIN(transaction_date) FROM budget_transaction
	`).Scan(&earliest)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("earliest transaction date: %w", err)
	}
	if earliest == nil {
		return time.Time{}, false, nil
	}
	return *earliest, true, nil
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
	defer tx.Rollback(ctx) //nolint:errcheck // rollback error is irrelevant after commit or on failed begin

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
			INSERT INTO budget_transaction (id, recurring_id, type, amount, currency, category_id, description, transaction_date)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, t.ID, recurringID, t.Type, t.Amount, t.Currency, t.CategoryID, t.Description, t.Date); err != nil {
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

// GetExecutedRecurringIDs возвращает множество recurring_id, для которых уже есть
// транзакция в заданном периоде. Используется для set-difference при расчёте прогноза.
func (s *Store) GetExecutedRecurringIDs(ctx context.Context, recurringIDs []uuid.UUID, p Period) (map[uuid.UUID]struct{}, error) {
	if len(recurringIDs) == 0 {
		return map[uuid.UUID]struct{}{}, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT recurring_id
		FROM budget_transaction
		WHERE recurring_id = ANY($1)
		  AND transaction_date >= $2
		  AND transaction_date <= $3
	`, recurringIDs, p.From, p.To)
	if err != nil {
		return nil, fmt.Errorf("get executed recurring ids: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan recurring id: %w", err)
		}
		result[id] = struct{}{}
	}
	return result, rows.Err()
}

// --- Прогнозирование ---

// MonthlyCategoryExpense — сырая выборка трат по (категория, валюта, месяц).
// Используется как вход для AggregateForecast: конвертация в THB и слияние
// валют одной категории происходит до усреднения, чтобы один и тот же
// платёж, оплаченный частично в RUB и THB в одном месяце, не попадал в
// два бакета и не давал двойной счёт.
type MonthlyCategoryExpense struct {
	CategoryName string
	Icon         string
	Currency     string
	Month        time.Time
	Total        float64
}

// GetForecastData возвращает прогноз трат по категориям, усреднённый по месяцам.
// Все суммы конвертируются в THB до усреднения (см. AggregateForecast).
// Учитываются только расходы (type = 'expense').
// Если months <= 0 — берёт все доступные данные.
func (s *Store) GetForecastData(ctx context.Context, months int, rates map[string]float64) ([]CategoryForecast, error) {
	rows, err := s.getMonthlyExpenses(ctx, months)
	if err != nil {
		return nil, err
	}
	return AggregateForecast(rows, rates), nil
}

// GetMonthlyIncomeAvg возвращает среднемесячный доход в THB по завершённым прошлым месяцам.
// Текущий (неполный) месяц исключается, чтобы не занижать среднее.
// Если данных нет — возвращает 0 без ошибки.
func (s *Store) GetMonthlyIncomeAvg(ctx context.Context, rates map[string]float64) (float64, error) {
	thbRate, ok := rates["THB"]
	if !ok || thbRate == 0 {
		return 0, fmt.Errorf("GetMonthlyIncomeAvg: THB rate missing")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT t.currency, date_trunc('month', t.transaction_date) AS month, SUM(t.amount) AS total
		FROM budget_transaction t
		WHERE t.type = 'income'
		  AND t.transaction_date < date_trunc('month', NOW())
		GROUP BY t.currency, month
	`)
	if err != nil {
		return 0, fmt.Errorf("GetMonthlyIncomeAvg: %w", err)
	}
	defer rows.Close()

	// month → THB sum
	byMonth := map[time.Time]float64{}
	for rows.Next() {
		var currency string
		var month time.Time
		var total float64
		if err := rows.Scan(&currency, &month, &total); err != nil {
			return 0, fmt.Errorf("GetMonthlyIncomeAvg scan: %w", err)
		}
		rubRate, ok := rates[currency]
		if !ok || rubRate == 0 {
			continue
		}
		byMonth[month] += total * rubRate / thbRate
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("GetMonthlyIncomeAvg rows: %w", err)
	}
	if len(byMonth) == 0 {
		return 0, nil
	}
	var sum float64
	for _, v := range byMonth {
		sum += v
	}
	return sum / float64(len(byMonth)), nil
}

func (s *Store) getMonthlyExpenses(ctx context.Context, months int) ([]MonthlyCategoryExpense, error) {
	var fromClause string
	var args []any

	if months > 0 {
		fromClause = `AND t.transaction_date >= date_trunc('month', NOW()) - ($1 * INTERVAL '1 month')`
		args = append(args, months)
	}

	query := fmt.Sprintf(`
		SELECT
			COALESCE(c.name, 'Прочее')                  AS category_name,
			COALESCE(NULLIF(c.icon, ''), '📦')          AS icon,
			t.currency,
			date_trunc('month', t.transaction_date)     AS month,
			SUM(t.amount)                               AS total
		FROM budget_transaction t
		LEFT JOIN budget_category c ON c.id = t.category_id
		WHERE t.type = 'expense'
		%s
		GROUP BY category_name, icon, t.currency, month
	`, fromClause)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get monthly expenses: %w", err)
	}
	defer rows.Close()

	var out []MonthlyCategoryExpense
	for rows.Next() {
		var r MonthlyCategoryExpense
		if err := rows.Scan(&r.CategoryName, &r.Icon, &r.Currency, &r.Month, &r.Total); err != nil {
			return nil, fmt.Errorf("scan monthly expense: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AggregateForecast строит прогноз: для каждой категории сначала суммирует
// траты за каждый месяц в THB (конвертируя из исходной валюты), затем
// усредняет полученные месячные суммы. Тренд — отношение последнего месяца
// к предыдущему (тоже в THB).
//
// rates — map[currency]rate_to_RUB; THB должен присутствовать. Строки с
// отсутствующим курсом валюты пропускаются.
//
// Возвращает CategoryForecast с Currency="THB" и ForecastAmount в THB,
// отсортированный по убыванию ForecastAmount.
func AggregateForecast(rows []MonthlyCategoryExpense, rates map[string]float64) []CategoryForecast {
	thbRate, ok := rates["THB"]
	if !ok || thbRate == 0 {
		return nil
	}

	// (category) -> (month) -> THB sum
	perCat := map[string]map[time.Time]float64{}
	icons := map[string]string{}
	for _, r := range rows {
		rubRate, ok := rates[r.Currency]
		if !ok || rubRate == 0 {
			continue
		}
		thb := r.Total * rubRate / thbRate
		if _, ok := perCat[r.CategoryName]; !ok {
			perCat[r.CategoryName] = map[time.Time]float64{}
		}
		perCat[r.CategoryName][r.Month] += thb
		// Сохраняем первый встретившийся непустой icon.
		if existing, ok := icons[r.CategoryName]; !ok || existing == "📦" {
			if r.Icon != "" {
				icons[r.CategoryName] = r.Icon
			}
		}
	}

	out := make([]CategoryForecast, 0, len(perCat))
	for cat, monthly := range perCat {
		months := make([]time.Time, 0, len(monthly))
		for m := range monthly {
			months = append(months, m)
		}
		sort.Slice(months, func(i, j int) bool { return months[i].After(months[j]) })

		var sum float64
		for _, v := range monthly {
			sum += v
		}
		avg := sum / float64(len(monthly))

		f := CategoryForecast{
			CategoryName:   cat,
			Icon:           icons[cat],
			Currency:       "THB",
			ForecastAmount: avg,
		}
		if len(months) >= 2 {
			last := monthly[months[0]]
			prev := monthly[months[1]]
			if prev > 0 {
				f.TrendPct = (last - prev) / prev * 100
				f.HasTrend = true
			}
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ForecastAmount > out[j].ForecastAmount })
	return out
}

// --- Курсы валют ---

// GetExchangeRates возвращает все курсы из БД как map[currency]rate_to_rub.
func (s *Store) GetExchangeRates(ctx context.Context) (map[string]float64, error) {
	rows, err := s.pool.Query(ctx, `SELECT currency, rate_to_rub FROM exchange_rate`)
	if err != nil {
		return nil, fmt.Errorf("get exchange rates: %w", err)
	}
	defer rows.Close()

	rates := map[string]float64{"RUB": 1.0}
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

// --- Advisor snapshot (ADR-002) ---

// advisorRow — сырая строка из advisor snapshot CTE.
type advisorRow struct {
	Kind     string  // "tx" | "debt" | "recurring"
	Subtype  string  // "income" | "expense" | "" (debt)
	Currency string  // ISO 4217
	Category string  // имя категории (только для tx)
	Total    float64 // сумма в исходной валюте
	Cnt      int     // число агрегированных записей
}

// advisorSnapshotQuery — единственный CTE собирающий MTD-транзакции, активные долги
// с due_date <= конец месяца, и enabled recurring с next_date <= конец месяца.
//
// Асимметрия chat_id (constraints §2):
//   - budget_transaction и budget_debt — глобальные (нет колонки chat_id)
//   - budget_recurring — фильтруется по chat_id = $2
const advisorSnapshotQuery = `
WITH params AS (
    SELECT
        date_trunc('month', $1::date)::date AS month_start,
        (date_trunc('month', $1::date) + interval '1 month - 1 day')::date AS month_end
),
tx_agg AS (
    SELECT
        'tx'::text                          AS kind,
        t.type                              AS subtype,
        t.currency                          AS currency,
        COALESCE(c.name, 'Прочее')          AS category,
        SUM(t.amount)::float8               AS total,
        COUNT(*)::int                       AS cnt
    FROM budget_transaction t
    LEFT JOIN budget_category c ON c.id = t.category_id
    CROSS JOIN params p
    WHERE t.transaction_date >= p.month_start
      AND t.transaction_date <= p.month_end
    GROUP BY t.type, t.currency, COALESCE(c.name, 'Прочее')
),
debt_agg AS (
    SELECT
        'debt'::text                        AS kind,
        ''::text                            AS subtype,
        'RUB'::text                         AS currency,
        ''::text                            AS category,
        COALESCE(SUM(d.total_amount - d.paid_amount), 0)::float8 AS total,
        COUNT(*)::int                       AS cnt
    FROM budget_debt d
    CROSS JOIN params p
    WHERE d.status = 'active'
      AND d.direction = 'owe'
      AND d.due_date IS NOT NULL
      AND d.due_date <= p.month_end
),
recurring_agg AS (
    SELECT
        'recurring'::text                   AS kind,
        r.type                              AS subtype,
        r.currency                          AS currency,
        ''::text                            AS category,
        SUM(r.amount)::float8               AS total,
        COUNT(*)::int                       AS cnt
    FROM budget_recurring r
    CROSS JOIN params p
    WHERE r.chat_id = $2
      AND r.enabled = true
      AND r.next_date <= p.month_end
    GROUP BY r.type, r.currency
)
SELECT kind, subtype, currency, category, total, cnt FROM tx_agg
UNION ALL
SELECT kind, subtype, currency, category, total, cnt FROM debt_agg WHERE cnt > 0
UNION ALL
SELECT kind, subtype, currency, category, total, cnt FROM recurring_agg
`

// GetAdvisorSnapshot собирает финансовый снимок для AdvisorSkill одним SQL CTE.
// Все суммы конвертируются в THB через rates (map[currency]rate_to_rub).
//
//   - today: точка отсчёта (определяет границы месяца через date_trunc)
//   - monthOffset: 0 — текущий месяц, 1 — прошлый, и т.д.
//   - chatID: фильтр для budget_recurring; budget_transaction и budget_debt — глобальные
//   - rates: map currency → rate_to_rub. Должна содержать "THB", иначе ошибка.
//
// ForecastRemaining не заполняется — это делает skill отдельным вызовом GetForecastData.
func (s *Store) GetAdvisorSnapshot(ctx context.Context, chatID int64, today time.Time, monthOffset int, rates map[string]float64) (*AdvisorSnapshot, error) {
	target := today.AddDate(0, -monthOffset, 0)
	rows, err := s.pool.Query(ctx, advisorSnapshotQuery, target, chatID)
	if err != nil {
		return nil, fmt.Errorf("advisor snapshot query: %w", err)
	}
	defer rows.Close()

	var collected []advisorRow
	for rows.Next() {
		var r advisorRow
		if err := rows.Scan(&r.Kind, &r.Subtype, &r.Currency, &r.Category, &r.Total, &r.Cnt); err != nil {
			return nil, fmt.Errorf("scan advisor row: %w", err)
		}
		collected = append(collected, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("advisor rows: %w", err)
	}
	return aggregateAdvisorSnapshot(collected, rates)
}

// topExpenseQuery — топ-N самых дорогих расходов за месяц (определённый через
// date_trunc от $1::date), глобально по всем chat_id (как budget_transaction).
// Сортировка по amount DESC в исходной валюте — не идеально для multi-currency,
// поэтому финальный ORDER в Go-коде после конверсии в THB.
const topExpenseQuery = `
WITH params AS (
    SELECT
        date_trunc('month', $1::date)::date AS month_start,
        (date_trunc('month', $1::date) + interval '1 month - 1 day')::date AS month_end
)
SELECT
    t.transaction_date,
    COALESCE(c.name, 'Прочее') AS category,
    t.amount,
    t.currency
FROM budget_transaction t
LEFT JOIN budget_category c ON c.id = t.category_id
CROSS JOIN params p
WHERE t.type = 'expense'
  AND t.transaction_date >= p.month_start
  AND t.transaction_date <= p.month_end
`

// GetTopExpenseTransactions — топ-N самых дорогих расходов за указанный месяц
// (today, monthOffset аналогично GetAdvisorSnapshot), сконвертированных в THB.
// Сортировка финальная по AmountTHB DESC в Go-коде (после конверсии).
func (s *Store) GetTopExpenseTransactions(ctx context.Context, today time.Time, monthOffset, limit int, rates map[string]float64) ([]TopExpense, error) {
	if limit <= 0 {
		limit = 20
	}
	thbRate, ok := rates["THB"]
	if !ok || thbRate == 0 {
		return nil, fmt.Errorf("top expenses: THB exchange rate missing")
	}
	target := today.AddDate(0, -monthOffset, 0)
	rows, err := s.pool.Query(ctx, topExpenseQuery, target)
	if err != nil {
		return nil, fmt.Errorf("top expenses query: %w", err)
	}
	defer rows.Close()

	var all []TopExpense
	for rows.Next() {
		var e TopExpense
		if err := rows.Scan(&e.Date, &e.Category, &e.OrigAmt, &e.Currency); err != nil {
			return nil, fmt.Errorf("scan top expense: %w", err)
		}
		rubRate, hasRate := rates[e.Currency]
		if !hasRate || rubRate == 0 {
			continue
		}
		e.AmountTHB = e.OrigAmt * rubRate / thbRate
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("top expenses rows: %w", err)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].AmountTHB > all[j].AmountTHB })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// aggregateAdvisorSnapshot — чистая агрегация: rows → AdvisorSnapshot в THB.
//
// Конверсия: amount * rates[currency] / rates["THB"].
// Отсутствие THB-курса — ошибка. Отсутствие курса для отдельной валюты —
// запись пропускается со slog.Warn.
func aggregateAdvisorSnapshot(rows []advisorRow, rates map[string]float64) (*AdvisorSnapshot, error) {
	thbRate, ok := rates["THB"]
	if !ok || thbRate == 0 {
		return nil, fmt.Errorf("advisor snapshot: THB exchange rate missing")
	}

	snap := &AdvisorSnapshot{SpentByCategory: map[string]float64{}}
	var income, expense float64

	for _, r := range rows {
		rubRate, hasRate := rates[r.Currency]
		if !hasRate || rubRate == 0 {
			slog.Warn("advisor snapshot: skipping row, no exchange rate",
				"currency", r.Currency, "kind", r.Kind, "amount", r.Total)
			continue
		}
		thb := r.Total * rubRate / thbRate

		switch r.Kind {
		case "tx":
			if r.Subtype == "income" {
				income += thb
			} else {
				expense += thb
				snap.SpentByCategory[r.Category] += thb
			}
			snap.TxCount += r.Cnt
		case "debt":
			snap.ActiveDebtDue += thb
		case "recurring":
			switch r.Subtype {
			case "expense":
				snap.UpcomingRecurring += thb
			case "income":
				snap.UpcomingRecurringIncome += thb
			}
		}
	}

	snap.BalanceMTD = income - expense
	snap.IncomeMTD = income
	snap.FreeCash = snap.BalanceMTD - snap.UpcomingRecurring - snap.ActiveDebtDue
	snap.LowData = snap.TxCount < MinTxForConfidence

	return snap, nil
}
