// Package mail реализует сбор почты из провайдеров, нормализацию и запись в БД.
// Здесь же формируется LLM-дайджест и оркестрация запусков; основные точки входа Runner.RunOnce и Store.
package mail

import (
	"context"
	"time"
)

type Provider interface {
	Fetch(ctx context.Context, account Account, checkpoint Checkpoint) (FetchResult, error)
}

type FetchResult struct {
	Messages  []FetchedMessage
	Cursor    string
	LastSeen  *time.Time
	HasMore   bool
	SourceUID string
}

type FetchedMessage struct {
	MessageID   string
	ProviderUID string
	FromEmail   string
	Subject     string
	ReceivedAt  *time.Time
	Preview     string
	Metadata    map[string]any
}
