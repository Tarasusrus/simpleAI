// Package agent содержит код пакета agent и его задачи.
package agent

import (
	"context"

	"simpleAI/internal/core"
)

type Service struct {
	client core.LLM
}

func NewService(client core.LLM) *Service {
	return &Service{client: client}
}

func (s *Service) Ask(ctx context.Context, input string) (string, error) {
	return s.client.Ask(ctx, input)
}
