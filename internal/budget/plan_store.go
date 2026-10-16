package budget

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Строки для плана окна. Деньги отдаются целыми минимальными единицами
// (amount*100), прямо из NUMERIC в SQL: через float64 копейки терялись бы на
// границе, а домен плана считает только целыми (C1).

// PlanTx — транзакция для плана: факт поступления или расхода.
type PlanTx struct {
	ID          uuid.UUID
	Type        string // "income" | "expense"
	AmountMinor int64
	Currency    string
	Category    string
	Description string
	Date        time.Time
	RecurringID *uuid.UUID
	// ScheduledFor — срок правила, к которому относится факт; у старых строк NULL.
	ScheduledFor *time.Time
}

// PlanRecurring — включённое повторяющееся правило в новом описании (шаг, якорь).
type PlanRecurring struct {
	ID           uuid.UUID
	ChatID       int64
	Name         string
	Type         string // "income" | "expense"
	Category     string
	Currency     string
	AmountMinor  int64
	IntervalN    int
	IntervalUnit string // "day" | "week" | "month" | "year"
	Anchor       time.Time
	DayOfMonth   *int
	Until        *time.Time
	Skipped      []time.Time
}

// PlanPlanned — незакрытая плановая запись (расход или поступление).
type PlanPlanned struct {
	ID          uuid.UUID
	ChatID      int64
	Direction   string // "expense" | "income"
	Description string
	Currency    string
	Category    string
	AmountMinor *int64 // nil — сумма неизвестна («Права»)
	DueDate     *time.Time
	WindowStart *time.Time
}

// PlanTransactions возвращает ВСЕ транзакции: поступления нужны с самого начала
// (цепочка окон зависит от точки отсечки истории), расходы — для «потрачено».
// Транзакции общие, без chat_id (как везде в боте, C16).
func (s *Store) PlanTransactions(ctx context.Context) ([]PlanTx, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.type, ROUND(t.amount * 100)::bigint, t.currency, COALESCE(c.name, ''),
		       t.description, t.transaction_date, t.recurring_id, t.scheduled_for
		FROM budget_transaction t
		LEFT JOIN budget_category c ON c.id = t.category_id
		ORDER BY t.transaction_date, t.created_at, t.id`)
	if err != nil {
		return nil, fmt.Errorf("plan transactions: %w", err)
	}
	defer rows.Close()
	var out []PlanTx
	for rows.Next() {
		var x PlanTx
		if err := rows.Scan(&x.ID, &x.Type, &x.AmountMinor, &x.Currency, &x.Category,
			&x.Description, &x.Date, &x.RecurringID, &x.ScheduledFor); err != nil {
			return nil, fmt.Errorf("plan transactions scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PlanRecurringRules возвращает включённые правила указанных чатов. Строки,
// заведённые до миграции 00021 или откатившимся бинарём, имеют NULL в новых
// колонках — тогда берётся то, что выводится из старых (recurrence_type, next_date).
func (s *Store) PlanRecurringRules(ctx context.Context, chatIDs []int64) ([]PlanRecurring, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.chat_id, r.name, r.type, COALESCE(c.name, ''), r.currency,
		       ROUND(r.amount * 100)::bigint, r.interval_n,
		       COALESCE(r.interval_unit, CASE r.recurrence_type WHEN 'weekly' THEN 'week' WHEN 'daily' THEN 'day' ELSE 'month' END),
		       COALESCE(r.anchor_date, r.next_date), r.day_of_month, r.until, r.skipped_dates
		FROM budget_recurring r
		LEFT JOIN budget_category c ON c.id = r.category_id
		WHERE r.enabled AND r.chat_id = ANY($1)
		ORDER BY r.created_at, r.id`, chatIDs)
	if err != nil {
		return nil, fmt.Errorf("plan recurring: %w", err)
	}
	defer rows.Close()
	var out []PlanRecurring
	for rows.Next() {
		var x PlanRecurring
		if err := rows.Scan(&x.ID, &x.ChatID, &x.Name, &x.Type, &x.Category, &x.Currency,
			&x.AmountMinor, &x.IntervalN, &x.IntervalUnit, &x.Anchor, &x.DayOfMonth, &x.Until, &x.Skipped); err != nil {
			return nil, fmt.Errorf("plan recurring scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PlanPlannedItems возвращает незакрытые плановые записи указанных чатов — в
// отличие от ListPlannedExpenses, и расходы, и поступления, и без суммы.
func (s *Store) PlanPlannedItems(ctx context.Context, chatIDs []int64) ([]PlanPlanned, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.chat_id, p.direction, p.description, p.currency, COALESCE(c.name, ''),
		       ROUND(p.amount * 100)::bigint, p.due_date, p.window_start
		FROM budget_planned_expense p
		LEFT JOIN budget_category c ON c.id = p.category_id
		WHERE NOT p.settled AND p.chat_id = ANY($1)
		ORDER BY p.created_at, p.id`, chatIDs)
	if err != nil {
		return nil, fmt.Errorf("plan planned: %w", err)
	}
	defer rows.Close()
	var out []PlanPlanned
	for rows.Next() {
		var x PlanPlanned
		if err := rows.Scan(&x.ID, &x.ChatID, &x.Direction, &x.Description, &x.Currency, &x.Category,
			&x.AmountMinor, &x.DueDate, &x.WindowStart); err != nil {
			return nil, fmt.Errorf("plan planned scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PlanReservePct — процент резерва из настроек плана (одна строка, по умолчанию 0).
func (s *Store) PlanReservePct(ctx context.Context) (int, error) {
	var pct int
	if err := s.pool.QueryRow(ctx, `SELECT reserve_pct FROM plan_settings WHERE id = 1`).Scan(&pct); err != nil {
		return 0, fmt.Errorf("plan reserve: %w", err)
	}
	return pct, nil
}

// PlanItemInput — новая плановая запись (расход или поступление).
type PlanItemInput struct {
	ChatID      int64
	Direction   string // "expense" | "income"
	Description string
	AmountMinor *int64 // nil — сумма неизвестна
	Currency    string
	Category    string // имя категории расходов; пусто — без категории
	DueDate     *time.Time
	WindowStart *time.Time
}

// AddPlanItem сохраняет плановую запись одной командой (одна транзакция БД).
// Категория подбирается по имени без учёта регистра; нет такой — запись без категории.
func (s *Store) AddPlanItem(ctx context.Context, in PlanItemInput) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO budget_planned_expense
		    (chat_id, direction, description, amount, currency, due_date, window_start, category_id)
		VALUES ($1, $2, $3,
		        CASE WHEN $4::bigint IS NULL THEN NULL ELSE $4::bigint::numeric / 100 END,
		        $5, $6, $7,
		        (SELECT c.id FROM budget_category c WHERE LOWER(c.name) = LOWER($8) AND c.type = 'expense' LIMIT 1))
		RETURNING id`,
		in.ChatID, in.Direction, in.Description, in.AmountMinor, in.Currency, in.DueDate, in.WindowStart, in.Category).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("add plan item: %w", err)
	}
	return id, nil
}

// DeletePlanItem удаляет плановую запись ТОЛЬКО из перечисленных чатов. false —
// такой записи там нет (нет id или чужой чат).
func (s *Store) DeletePlanItem(ctx context.Context, chatIDs []int64, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM budget_planned_expense WHERE id = $1 AND chat_id = ANY($2)`, id, chatIDs)
	if err != nil {
		return false, fmt.Errorf("delete plan item: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ExpenseCategoryNames — названия категорий расходов по порядку сортировки.
func (s *Store) ExpenseCategoryNames(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT name FROM budget_category WHERE type = 'expense' ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("expense categories: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("expense categories scan: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
