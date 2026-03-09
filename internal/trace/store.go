package trace

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store сохраняет трейсы agentic loop в Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore создаёт Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Append записывает одну строку трейса. Возвращает ошибку — вызывающий решает как её обработать.
func (s *Store) Append(ctx context.Context, e Entry) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_trace
			(id, session_id, chat_id, user_input, iteration, skill, skill_input, skill_result, llm_response, is_final)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, e.ID, e.SessionID, e.ChatID, e.UserInput, e.Iteration, e.Skill, e.SkillInput, e.SkillResult, e.LLMResponse, e.IsFinal)
	if err != nil {
		return fmt.Errorf("trace append: %w", err)
	}
	return nil
}
