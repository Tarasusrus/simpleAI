package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Правка и удаление любой транзакции (ADR-018, simpleAI-dvtx). Один путь для всех: бот, экран, оплата по плану,
// правило повтора. Границу владения здесь никто не держит — страница «Транзакции» правит всё (решение владельца).
//
// Каждая правка и удаление пишутся в tx_edit_log В ТОЙ ЖЕ транзакции БД (снимок «до» целиком), версия строки —
// updated_at (триггер из миграции 00028 двигает её при любом UPDATE): устаревшая версия — ErrTxConflict.

var (
	// ErrTxNotFound — транзакции нет.
	ErrTxNotFound = errors.New("budget: транзакция не найдена")
	// ErrTxConflict — строку изменили после того, как её прочитали.
	ErrTxConflict = errors.New("budget: транзакция изменилась, обновите страницу")
)

// Источники записи в журнал.
const (
	TxSourceWeb = "web"
)

// TxRow — транзакция целиком, как её видит страница «Транзакции».
type TxRow struct {
	ID           uuid.UUID
	Type         string
	AmountMinor  int64
	Currency     string
	CategoryID   *uuid.UUID
	CategoryName string // только в выборках с JOIN (GetTxView, ListTx, AroundTx)
	Description  string
	Date         time.Time
	UpdatedAt    time.Time
	PlannedID    *uuid.UUID
	RecurringID  *uuid.UUID
	AddedBy      string
	OpensWindow  bool // приход открывает окно (ADR-019)
}

// TxChange — новые значения. Все поля обязательны, кроме CategoryID: nil оставляет категорию как есть.
type TxChange struct {
	Description string
	AmountMinor int64
	Currency    string
	Date        time.Time
	CategoryID  *uuid.UUID
	// OpensWindow — вид прихода (ADR-019): true — зарплата/аванс, открывает окно; false — обычный приход; nil —
	// не менять. У расходов значение не используется, но хранится как есть.
	OpensWindow *bool
}

const txColumns = `id, type, ROUND(amount * 100)::bigint, currency, category_id, description, transaction_date,
	updated_at, planned_id, recurring_id, COALESCE(added_by, ''), opens_window`

func scanTx(row pgx.Row) (TxRow, error) {
	var x TxRow
	err := row.Scan(&x.ID, &x.Type, &x.AmountMinor, &x.Currency, &x.CategoryID, &x.Description, &x.Date,
		&x.UpdatedAt, &x.PlannedID, &x.RecurringID, &x.AddedBy, &x.OpensWindow)
	return x, err
}

// GetTx читает транзакцию по id; ErrTxNotFound — нет такой.
func (s *Store) GetTx(ctx context.Context, id uuid.UUID) (TxRow, error) {
	x, err := scanTx(s.pool.QueryRow(ctx, `SELECT `+txColumns+` FROM budget_transaction WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return TxRow{}, ErrTxNotFound
	}
	if err != nil {
		return TxRow{}, fmt.Errorf("get tx: %w", err)
	}
	return x, nil
}

// lockTx берёт строку под блокировку и сверяет версию. Возвращает снимок строки (jsonb) для журнала и саму строку.
func lockTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, version time.Time) (TxRow, []byte, error) {
	var snapshot []byte
	row := tx.QueryRow(ctx, `SELECT to_jsonb(t) FROM budget_transaction t WHERE id = $1 FOR UPDATE`, id)
	if err := row.Scan(&snapshot); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TxRow{}, nil, ErrTxNotFound
		}
		return TxRow{}, nil, fmt.Errorf("lock tx: %w", err)
	}
	x, err := scanTx(tx.QueryRow(ctx, `SELECT `+txColumns+` FROM budget_transaction WHERE id = $1`, id))
	if err != nil {
		return TxRow{}, nil, fmt.Errorf("lock tx: read: %w", err)
	}
	if !x.UpdatedAt.Equal(version) {
		return TxRow{}, nil, ErrTxConflict
	}
	return x, snapshot, nil
}

// EditTx меняет транзакцию и пишет снимок «до» и значения «после» в tx_edit_log одной транзакцией БД.
func (s *Store) EditTx(ctx context.Context, id uuid.UUID, version time.Time, source string, c TxChange) (TxRow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TxRow{}, fmt.Errorf("edit tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // после Commit откат — no-op

	_, snapshot, err := lockTx(ctx, tx, id, version)
	if err != nil {
		return TxRow{}, err
	}
	if err := applyEdit(ctx, tx, id, source, snapshot, c); err != nil {
		return TxRow{}, err
	}
	out, err := scanTx(tx.QueryRow(ctx, `SELECT `+txColumns+` FROM budget_transaction WHERE id = $1`, id))
	if err != nil {
		return TxRow{}, fmt.Errorf("edit tx: read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TxRow{}, fmt.Errorf("edit tx: commit: %w", err)
	}
	return out, nil
}

func applyEdit(ctx context.Context, tx pgx.Tx, id uuid.UUID, source string, before []byte, c TxChange) error {
	var after []byte
	err := tx.QueryRow(ctx, `
		WITH upd AS (
			UPDATE budget_transaction SET
			    description      = $2,
			    amount           = $3::bigint::numeric / 100,
			    currency         = $4,
			    transaction_date = $5,
			    category_id      = COALESCE($6, category_id),
			    opens_window     = COALESCE($7, opens_window)
			WHERE id = $1
			RETURNING *
		)
		SELECT to_jsonb(upd) FROM upd`,
		id, c.Description, c.AmountMinor, c.Currency, c.Date, c.CategoryID, c.OpensWindow).Scan(&after)
	if err != nil {
		return fmt.Errorf("edit tx: update: %w", err)
	}
	return writeTxLog(ctx, tx, id, "edit", source, before, after)
}

// DeleteTx удаляет транзакцию (жёстко) со снимком в tx_edit_log. Если она закрывала плановую запись, та открывается
// заново в той же транзакции БД: правило общее для любой транзакции со связью planned_id, ветки по типу нет.
func (s *Store) DeleteTx(ctx context.Context, id uuid.UUID, version time.Time, source string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // после Commit откат — no-op

	x, snapshot, err := lockTx(ctx, tx, id, version)
	if err != nil {
		return err
	}
	if x.PlannedID != nil {
		if _, err := tx.Exec(ctx, `UPDATE budget_planned_expense SET settled = false WHERE id = $1`, *x.PlannedID); err != nil {
			return fmt.Errorf("delete tx: reopen planned: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM budget_transaction WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete tx: %w", err)
	}
	if err := writeTxLog(ctx, tx, id, "delete", source, snapshot, nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete tx: commit: %w", err)
	}
	return nil
}

func writeTxLog(ctx context.Context, tx pgx.Tx, id uuid.UUID, action, source string, before, after []byte) error {
	var afterArg any
	if after != nil {
		afterArg = after
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO tx_edit_log (id, tx_id, action, before, after, source)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6)`,
		uuid.New(), id, action, before, afterArg, source); err != nil {
		return fmt.Errorf("tx edit log: %w", err)
	}
	return nil
}
