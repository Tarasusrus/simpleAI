package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Лимиты категорий на экране плана (ADR-011). Все команды ограничены областью чатов
// прямо в WHERE; деньги — целые минимальные единицы, как в остальном plan_*.

var (
	// ErrPlanLimitDuplicate — лимит с таким ключом уникальности уже есть.
	ErrPlanLimitDuplicate = errors.New("plan limit: такой лимит уже есть")
	// ErrPlanLimitCategory — нет категории расходов с таким именем.
	ErrPlanLimitCategory = errors.New("plan limit: нет такой категории расходов")
)

// PlanLimit — строка plan_category_limit с именем категории.
type PlanLimit struct {
	ID          uuid.UUID
	ChatID      int64
	Category    string
	AmountMinor int64
	Currency    string
	Repeat      bool
	WindowStart time.Time
}

// PlanLimitInput — новый лимит; категория задаётся именем (без учёта регистра).
type PlanLimitInput struct {
	ChatID      int64
	Category    string
	AmountMinor int64
	Currency    string
	Repeat      bool
	WindowStart time.Time
}

// PlanLimits возвращает лимиты указанных чатов.
func (s *Store) PlanLimits(ctx context.Context, chatIDs []int64) ([]PlanLimit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.chat_id, c.name, l.amount_minor, l.currency, l.repeat, l.window_start
		FROM plan_category_limit l
		JOIN budget_category c ON c.id = l.category_id
		WHERE l.chat_id = ANY($1)
		ORDER BY l.created_at, l.id`, chatIDs)
	if err != nil {
		return nil, fmt.Errorf("plan limits: %w", err)
	}
	defer rows.Close()
	var out []PlanLimit
	for rows.Next() {
		var x PlanLimit
		if err := rows.Scan(&x.ID, &x.ChatID, &x.Category, &x.AmountMinor, &x.Currency, &x.Repeat, &x.WindowStart); err != nil {
			return nil, fmt.Errorf("plan limits scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// AddPlanLimit сохраняет лимит. Нет категории расходов с таким именем —
// ErrPlanLimitCategory; ключ уникальности занят — ErrPlanLimitDuplicate.
func (s *Store) AddPlanLimit(ctx context.Context, in PlanLimitInput) (uuid.UUID, error) {
	catID, err := s.expenseCategoryID(ctx, in.Category)
	if errors.Is(err, ErrUnknownCategory) || (err == nil && !catID.Valid) {
		return uuid.Nil, ErrPlanLimitCategory
	}
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO plan_category_limit (chat_id, category_id, amount_minor, currency, repeat, window_start)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		in.ChatID, catID.UUID, in.AmountMinor, in.Currency, in.Repeat, in.WindowStart).Scan(&id)
	switch {
	case err == nil:
		return id, nil
	case isUniqueViolation(err):
		return uuid.Nil, ErrPlanLimitDuplicate
	}
	return uuid.Nil, fmt.Errorf("add plan limit: %w", err)
}

// UpdatePlanLimit меняет сумму, валюту, повтор и (если задано) окно лимита из указанных чатов.
// false — такого лимита в этих чатах нет.
func (s *Store) UpdatePlanLimit(ctx context.Context, chatIDs []int64, id uuid.UUID, amountMinor int64, currency string, repeat bool, windowStart *time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE plan_category_limit SET amount_minor = $3, currency = $4, repeat = $5,
		       window_start = COALESCE($6::date, window_start)
		WHERE id = $1 AND chat_id = ANY($2)`, id, chatIDs, amountMinor, currency, repeat, windowStart)
	if isUniqueViolation(err) {
		return false, ErrPlanLimitDuplicate
	}
	if err != nil {
		return false, fmt.Errorf("update plan limit: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeletePlanLimit удаляет лимит ТОЛЬКО из указанных чатов. false — такого лимита там нет.
func (s *Store) DeletePlanLimit(ctx context.Context, chatIDs []int64, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM plan_category_limit WHERE id = $1 AND chat_id = ANY($2)`, id, chatIDs)
	if err != nil {
		return false, fmt.Errorf("delete plan limit: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// PlanLimitHistory — траты расходов по категориям и ПОЛНЫМ календарным месяцам за всю
// историю, без повторяющихся (recurring_id IS NULL): аренда и подписки лимитом не
// являются. Текущий неполный месяц исключён, иначе он занижал бы среднее. Из этих
// строк считается подсказка суммы лимита (ADR-011).
func (s *Store) PlanLimitHistory(ctx context.Context) ([]MonthlyCategoryExpense, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(c.name, 'Прочее'), COALESCE(NULLIF(c.icon, ''), '📦'), t.currency,
		       date_trunc('month', t.transaction_date), SUM(t.amount)
		FROM budget_transaction t
		LEFT JOIN budget_category c ON c.id = t.category_id
		WHERE t.type = 'expense'
		  AND t.recurring_id IS NULL
		  AND t.transaction_date < date_trunc('month', NOW())
		GROUP BY 1, 2, 3, 4`)
	if err != nil {
		return nil, fmt.Errorf("plan limit history: %w", err)
	}
	defer rows.Close()
	var out []MonthlyCategoryExpense
	for rows.Next() {
		var r MonthlyCategoryExpense
		if err := rows.Scan(&r.CategoryName, &r.Icon, &r.Currency, &r.Month, &r.Total); err != nil {
			return nil, fmt.Errorf("plan limit history scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
