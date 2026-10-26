package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Чтение для страницы «Транзакции» (ADR-018): список с фильтрами и keyset-пагинацией, одна строка по id и окно
// вокруг строки для ссылки /transactions?focus=<id>. Порядок везде один: дата по убыванию, id по возрастанию.
// Транзакции общие, без chat_id (ADR-009 §8), поэтому область чатов здесь не проверяется.

const txViewSelect = `SELECT t.id, t.type, ROUND(t.amount * 100)::bigint, t.currency, t.category_id, COALESCE(c.name, ''),
	t.description, t.transaction_date, t.updated_at, t.planned_id, t.recurring_id, COALESCE(t.added_by, '')
	FROM budget_transaction t LEFT JOIN budget_category c ON c.id = t.category_id`

func scanTxView(row pgx.Row) (TxRow, error) {
	var x TxRow
	err := row.Scan(&x.ID, &x.Type, &x.AmountMinor, &x.Currency, &x.CategoryID, &x.CategoryName, &x.Description,
		&x.Date, &x.UpdatedAt, &x.PlannedID, &x.RecurringID, &x.AddedBy)
	return x, err
}

func collectTxView(rows pgx.Rows) ([]TxRow, error) {
	defer rows.Close()
	var out []TxRow
	for rows.Next() {
		x, err := scanTxView(rows)
		if err != nil {
			return nil, fmt.Errorf("tx view scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// TxListQuery — фильтры и продолжение списка. Пустые поля не фильтруют.
type TxListQuery struct {
	Text      string // подстрока описания без учёта регистра
	Category  string // имя категории, lower(btrim()) как в plan.CategoryKey
	From, To  *time.Time
	AfterDate *time.Time // продолжение после (дата, id)
	AfterID   *uuid.UUID
	Limit     int
}

// GetTxView читает транзакцию по id с именем категории; ErrTxNotFound — нет такой.
func (s *Store) GetTxView(ctx context.Context, id uuid.UUID) (TxRow, error) {
	x, err := scanTxView(s.pool.QueryRow(ctx, txViewSelect+` WHERE t.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return TxRow{}, ErrTxNotFound
	}
	if err != nil {
		return TxRow{}, fmt.Errorf("get tx view: %w", err)
	}
	return x, nil
}

// ListTx возвращает страницу транзакций: Limit строк после курсора.
func (s *Store) ListTx(ctx context.Context, q TxListQuery) ([]TxRow, error) {
	rows, err := s.pool.Query(ctx, txViewSelect+`
		WHERE ($1 = '' OR position(lower($1) in lower(t.description)) > 0)
		  AND ($2 = '' OR lower(btrim(c.name)) = lower(btrim($2)))
		  AND ($3::date IS NULL OR t.transaction_date >= $3::date)
		  AND ($4::date IS NULL OR t.transaction_date <= $4::date)
		  AND ($5::date IS NULL OR t.transaction_date < $5::date OR (t.transaction_date = $5::date AND t.id > $6::uuid))
		ORDER BY t.transaction_date DESC, t.id ASC
		LIMIT $7`, q.Text, q.Category, q.From, q.To, q.AfterDate, q.AfterID, q.Limit)
	if err != nil {
		return nil, fmt.Errorf("list tx: %w", err)
	}
	return collectTxView(rows)
}

// AroundTx возвращает окно вокруг транзакции id: до before более новых строк, сама строка и до after более старых.
// Фильтры не применяются: ссылка ведёт на конкретную строку. ErrTxNotFound — строки нет.
func (s *Store) AroundTx(ctx context.Context, id uuid.UUID, before, after int) ([]TxRow, error) {
	focus, err := s.GetTxView(ctx, id)
	if err != nil {
		return nil, err
	}
	newer, err := s.pool.Query(ctx, txViewSelect+`
		WHERE t.transaction_date > $1::date OR (t.transaction_date = $1::date AND t.id < $2)
		ORDER BY t.transaction_date ASC, t.id DESC
		LIMIT $3`, focus.Date, id, before)
	if err != nil {
		return nil, fmt.Errorf("around tx: newer: %w", err)
	}
	up, err := collectTxView(newer)
	if err != nil {
		return nil, err
	}
	older, err := s.pool.Query(ctx, txViewSelect+`
		WHERE t.transaction_date < $1::date OR (t.transaction_date = $1::date AND t.id > $2)
		ORDER BY t.transaction_date DESC, t.id ASC
		LIMIT $3`, focus.Date, id, after)
	if err != nil {
		return nil, fmt.Errorf("around tx: older: %w", err)
	}
	down, err := collectTxView(older)
	if err != nil {
		return nil, err
	}
	out := make([]TxRow, 0, len(up)+1+len(down))
	for i := len(up) - 1; i >= 0; i-- { // newer пришли от ближайших к дальним, в списке нужен обратный порядок
		out = append(out, up[i])
	}
	out = append(out, focus)
	return append(out, down...), nil
}
