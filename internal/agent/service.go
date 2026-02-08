// Package agent содержит код пакета agent и его задачи.
package agent

import (
	"context"

	"simpleAI/pkg/llm"
)

type Service struct {
	client llm.Client
}

func NewService(client llm.Client) *Service {
	return &Service{client: client}
}

func (s *Service) Ask(ctx context.Context, input string) (string, error) {
	_ = ctx
	return s.client.Ask(input)
}
