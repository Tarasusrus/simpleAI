// Package agent описывает слой агента поверх LLM: сервис Ask и расширяемый Agent с плагинами.
// Пакет связывает бизнес-логику с LLM, не завязываясь на конкретных провайдеров.
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
