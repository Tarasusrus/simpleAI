// Package seenstore — receipt.SeenStore в Postgres: номера записанных чеков по чату (миграция 00029).
package seenstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/internal/receipt"
)

// Store хранит отметки в таблице receipt_seen.
type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ receipt.SeenStore = (*Store)(nil)

// Seen — номер уже записан в этом чате.
func (s *Store) Seen(ctx context.Context, chatID int64, reference string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM receipt_seen WHERE chat_id = $1 AND reference = $2)`,
		chatID, reference).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("receipt seen: %w", err)
	}
	return ok, nil
}

// Claim занимает номер; false — он уже занят (чек записан или записывается).
func (s *Store) Claim(ctx context.Context, chatID int64, reference string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO receipt_seen (chat_id, reference) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		chatID, reference)
	if err != nil {
		return false, fmt.Errorf("receipt claim: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Release снимает отметку: запись расхода не удалась, чек можно прислать снова.
func (s *Store) Release(ctx context.Context, chatID int64, reference string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM receipt_seen WHERE chat_id = $1 AND reference = $2`, chatID, reference); err != nil {
		return fmt.Errorf("receipt release: %w", err)
	}
	return nil
}
