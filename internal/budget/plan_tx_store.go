package budget

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PlanCategoryTxQuery — запрос трат категории окна (ADR-015 §6).
type PlanCategoryTxQuery struct {
	// Category — имя категории расходов; сравнение lower(btrim()) на стороне базы, как plan.CategoryKey.
	Category string
	From, To time.Time
	// After — продолжение после (дата, id) в порядке «дата DESC, id ASC»; nil — первая страница.
	AfterDate *time.Time
	AfterID   *uuid.UUID
	Limit     int
}

// PlanCategoryTransactions возвращает оплаченные расходы категории за [From, To] — те же факты, из которых
// собран Spent строки категории: без повторяющихся (recurring_id IS NULL), по category_id категорий расходов
// с этим именем. Транзакции общие, без chat_id. Порядок: дата по убыванию, id по возрастанию (keyset).
func (s *Store) PlanCategoryTransactions(ctx context.Context, q PlanCategoryTxQuery) ([]PlanTx, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.type, ROUND(t.amount * 100)::bigint, t.currency, c.name,
		       t.description, t.transaction_date, t.recurring_id, t.scheduled_for,
		       COALESCE(t.added_by, ''), t.planned_id
		FROM budget_transaction t
		JOIN budget_category c ON c.id = t.category_id
		WHERE t.category_id IN (SELECT id FROM budget_category WHERE type = 'expense' AND lower(btrim(name)) = lower(btrim($1)))
		  AND t.type = 'expense'
		  AND t.currency IN ('RUB', 'THB')
		  AND t.recurring_id IS NULL
		  AND t.transaction_date BETWEEN $2::date AND $3::date
		  AND ($4::date IS NULL OR t.transaction_date < $4::date OR (t.transaction_date = $4::date AND t.id > $5::uuid))
		ORDER BY t.transaction_date DESC, t.id ASC
		LIMIT $6`, q.Category, q.From, q.To, q.AfterDate, q.AfterID, q.Limit)
	if err != nil {
		return nil, fmt.Errorf("plan category transactions: %w", err)
	}
	defer rows.Close()
	var out []PlanTx
	for rows.Next() {
		var x PlanTx
		if err := rows.Scan(&x.ID, &x.Type, &x.AmountMinor, &x.Currency, &x.Category,
			&x.Description, &x.Date, &x.RecurringID, &x.ScheduledFor, &x.AddedBy, &x.PlannedID); err != nil {
			return nil, fmt.Errorf("plan category transactions scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
