package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Окна плана (ADR-021): бизнес-сущность со своей таблицей.

// ErrPlanWindowNotFound — окна нет в области (или оно удалено).
var ErrPlanWindowNotFound = errors.New("budget: окно плана не найдено")

// PlanWindow — окно плана; даты — UTC-полночь.
type PlanWindow struct {
	ID         uuid.UUID
	Start, End time.Time
	Deleted    bool
}

// CreatePlanWindow сохраняет окно в чат и возвращает id. Действующее окно с тем же началом в чате уже есть —
// возвращает его id: повтор безопасен.
func (s *Store) CreatePlanWindow(ctx context.Context, chatID int64, start, end time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO plan_window (chat_id, start_date, end_date) VALUES ($1, $2::date, $3::date)
		ON CONFLICT (chat_id, start_date) WHERE NOT deleted DO NOTHING
		RETURNING id`, chatID, start, end).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.pool.QueryRow(ctx, `
			SELECT id FROM plan_window WHERE chat_id = $1 AND start_date = $2::date AND NOT deleted`, chatID, start).Scan(&id)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("create plan window: %w", err)
	}
	return id, nil
}

// UpdatePlanWindow меняет даты действующего окна области. Нет окна — ErrPlanWindowNotFound.
func (s *Store) UpdatePlanWindow(ctx context.Context, chatIDs []int64, id uuid.UUID, start, end time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE plan_window SET start_date = $3::date, end_date = $4::date, updated_at = now()
		WHERE id = $2 AND chat_id = ANY($1) AND NOT deleted`, chatIDs, id, start, end)
	if err != nil {
		return fmt.Errorf("update plan window: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPlanWindowNotFound
	}
	return nil
}

// DeletePlanWindow помечает действующее окно области удалённым. Нет окна — ErrPlanWindowNotFound.
func (s *Store) DeletePlanWindow(ctx context.Context, chatIDs []int64, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE plan_window SET deleted = true, updated_at = now()
		WHERE id = $2 AND chat_id = ANY($1) AND NOT deleted`, chatIDs, id)
	if err != nil {
		return fmt.Errorf("delete plan window: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPlanWindowNotFound
	}
	return nil
}

// PlanWindows возвращает окна области, включая удалённые, по дате начала.
func (s *Store) PlanWindows(ctx context.Context, chatIDs []int64) ([]PlanWindow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, start_date, end_date, deleted FROM plan_window
		WHERE chat_id = ANY($1) ORDER BY start_date, created_at, id`, chatIDs)
	if err != nil {
		return nil, fmt.Errorf("plan windows: %w", err)
	}
	defer rows.Close()
	var out []PlanWindow
	for rows.Next() {
		var x PlanWindow
		if err := rows.Scan(&x.ID, &x.Start, &x.End, &x.Deleted); err != nil {
			return nil, fmt.Errorf("plan windows scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
