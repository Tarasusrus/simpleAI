// Package agent contains agent package code and its tasks.
package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"simpleAI/internal/core"
	"simpleAI/internal/observability"
	"simpleAI/internal/plugin"
	"simpleAI/internal/trace"
)

const maxIterations = 10

// Service wraps an LLM client with optional skill registry for tool calling.
type Service struct {
	client   core.LLM
	registry *plugin.Registry
	tracer   *trace.Store
	obs      *observability.Tracer
	logger   *slog.Logger
	llmModel string
}

func NewService(client core.LLM) *Service {
	return &Service{client: client, logger: slog.Default()}
}

func NewServiceWithRegistry(client core.LLM, registry *plugin.Registry) *Service {
	return &Service{client: client, registry: registry, logger: slog.Default()}
}

// WithLogger добавляет logger в сервис.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
}

// WithTracer добавляет трейс-стор в сервис (опционально).
func (s *Service) WithTracer(t *trace.Store) *Service {
	s.tracer = t
	return s
}

// WithObservability добавляет Langfuse-трейсер (опционально). nil безопасен.
func (s *Service) WithObservability(o *observability.Tracer) *Service {
	s.obs = o
	return s
}

// WithLLMModel задаёт имя модели для Langfuse generation.
func (s *Service) WithLLMModel(model string) *Service {
	s.llmModel = model
	return s
}

// Ask отвечает на запрос пользователя.
func (s *Service) Ask(ctx context.Context, input string) (string, error) {
	return s.AskWithMeta(ctx, input, nil)
}

// ChatIDKey — ключ контекста для передачи chat_id в skills.
type ChatIDKey struct{}

// AskWithMeta аналогичен Ask, но принимает chatID для трейсинга.
func (s *Service) AskWithMeta(ctx context.Context, input string, chatID *int64) (string, error) {
	if chatID != nil {
		ctx = context.WithValue(ctx, ChatIDKey{}, *chatID)
	}
	if s.registry == nil || len(s.registry.List()) == 0 {
		userID := ""
		if chatID != nil {
			userID = fmt.Sprintf("%d", *chatID)
		}
		obsTrace := s.obs.StartTrace("agent.ask", map[string]any{"input": input}, "", userID)
		gen := obsTrace.StartGeneration("llm.ask", s.llmModel, input)
		resp, err := s.client.Ask(ctx, input)
		gen.End(resp, err)
		obsTrace.End(map[string]any{"answer": resp})
		return resp, err
	}

	sessionID := uuid.New()
	userID := ""
	if chatID != nil {
		userID = fmt.Sprintf("%d", *chatID)
	}
	obsTrace := s.obs.StartTrace("agent.run", map[string]any{"input": input}, sessionID.String(), userID)

	var finalAnswer string
	defer func() {
		obsTrace.End(map[string]any{"answer": finalAnswer})
	}()

	finalAnswer, err := s.runAgentLoop(ctx, input, sessionID, obsTrace, chatID)
	return finalAnswer, err
}
