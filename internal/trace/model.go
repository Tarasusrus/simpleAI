// Package trace хранит историю вызовов agentic loop в Postgres.
package trace

import (
	"time"

	"github.com/google/uuid"
)

// Entry описывает одну строку истории агентского цикла.
type Entry struct {
	ID          uuid.UUID
	SessionID   uuid.UUID
	ChatID      *int64
	UserInput   string
	Iteration   int
	Skill       *string
	SkillInput  []byte // JSONB
	SkillResult *string
	LLMResponse string
	IsFinal     bool
	CreatedAt   time.Time
}
