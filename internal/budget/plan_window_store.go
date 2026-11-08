package budget

import (
	"context"
	"fmt"
	"time"
)

// Правка длины окна плана (ADR-020): исключения из стандартных 14 дней.

// PlanWindowLength — поправленная длина окна, начинающегося в WindowStart (UTC-полночь).
type PlanWindowLength struct {
	WindowStart time.Time
	Days        int
}

// SetPlanWindowLength сохраняет длину окна для чата; повтор перезаписывает.
func (s *Store) SetPlanWindowLength(ctx context.Context, chatID int64, windowStart time.Time, days int) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO plan_window_length (chat_id, window_start, days) VALUES ($1, $2::date, $3)
		ON CONFLICT (chat_id, window_start) DO UPDATE SET days = EXCLUDED.days, updated_at = now()`,
		chatID, windowStart, days); err != nil {
		return fmt.Errorf("set plan window length: %w", err)
	}
	return nil
}

// ResetPlanWindowLength снимает правку окна во всех чатах области; правки не было — не ошибка.
func (s *Store) ResetPlanWindowLength(ctx context.Context, chatIDs []int64, windowStart time.Time) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM plan_window_length WHERE chat_id = ANY($1) AND window_start = $2::date`, chatIDs, windowStart); err != nil {
		return fmt.Errorf("reset plan window length: %w", err)
	}
	return nil
}

// PlanWindowLengths возвращает правки окон чатов области, по одной на дату начала. Если правки одного окна есть
// у нескольких чатов, берётся самая свежая: «последнее сохранение побеждает», как и в остальном плане.
func (s *Store) PlanWindowLengths(ctx context.Context, chatIDs []int64) ([]PlanWindowLength, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (window_start) window_start, days FROM plan_window_length
		WHERE chat_id = ANY($1) ORDER BY window_start, updated_at DESC, chat_id`, chatIDs)
	if err != nil {
		return nil, fmt.Errorf("plan window lengths: %w", err)
	}
	defer rows.Close()
	var out []PlanWindowLength
	for rows.Next() {
		var x PlanWindowLength
		if err := rows.Scan(&x.WindowStart, &x.Days); err != nil {
			return nil, fmt.Errorf("plan window lengths scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
