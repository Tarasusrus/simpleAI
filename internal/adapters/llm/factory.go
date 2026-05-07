// Package llm содержит фабрику для выбора LLM-адаптера по конфигурации.
package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"simpleAI/config"
	ollamaadapter "simpleAI/internal/adapters/llm/ollama"
	openaiadapter "simpleAI/internal/adapters/llm/openai"
	"simpleAI/internal/core"
)

// NewClient возвращает LLM-клиент выбранного провайдера.
// Для провайдера "openai" создаёт составной клиент:
// DeepSeek для чата, Gemini для эмбеддингов и резервного чата.
func NewClient(cfg config.Config, logger *slog.Logger) (core.LLMClient, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.LLM.Provider))
	if provider == "" {
		provider = "openai"
	}
	if logger != nil {
		logger.Debug("llm provider selected", "provider", provider)
	}

	switch provider {
	case "openai":
		return buildOpenAIClient(cfg, logger)
	case "ollama":
		client, err := ollamaadapter.NewClient(cfg, logger)
		if err != nil {
			return nil, err
		}
		if logger != nil {
			logger.Debug("llm provider active", "provider", "ollama", "model", cfg.LLM.OllamaModel)
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", provider)
	}
}

// buildOpenAIClient строит клиент для OpenAI-совместимых провайдеров.
// При наличии обоих ключей возвращает составной клиент с per-request fallback:
// primary (DeepSeek) для чата с автоматическим переключением на fallback (Gemini)
// при ошибке отдельного запроса. Embed всегда идёт на fallback (Gemini).
func buildOpenAIClient(cfg config.Config, logger *slog.Logger) (core.LLMClient, error) {
	fallback, fallbackErr := openaiadapter.NewFallbackClient(cfg, logger)
	if fallbackErr != nil && logger != nil {
		logger.Warn("fallback llm not available", "err", fallbackErr)
	}

	primary, primaryErr := openaiadapter.NewClient(cfg, logger)
	if primaryErr != nil {
		if logger != nil {
			logger.Warn("primary llm not available", "err", primaryErr)
		}
		if fallback != nil {
			if logger != nil {
				logger.Info("llm provider active", "provider", "fallback-only", "model", cfg.LLM.FallbackChatModel)
			}
			return fallback, nil
		}
		return fallbackToOllama(cfg, logger, primaryErr)
	}

	if fallback != nil {
		if logger != nil {
			logger.Info("llm provider active", "provider", "composite", "primary_model", cfg.LLM.ChatModel, "fallback_model", cfg.LLM.FallbackChatModel)
		}
		return newCompositeClient(primary, fallback, logger), nil
	}

	if logger != nil {
		logger.Info("llm provider active", "provider", "primary-only", "model", cfg.LLM.ChatModel)
	}
	return primary, nil
}

func fallbackToOllama(cfg config.Config, logger *slog.Logger, cause error) (core.LLMClient, error) {
	if logger != nil && cause != nil {
		logger.Warn("all cloud LLM providers failed, falling back to Ollama", "err", cause)
	}
	client, err := ollamaadapter.NewClient(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("all LLM providers unavailable: %w", cause)
	}
	if logger != nil {
		logger.Debug("llm provider active", "provider", "ollama", "model", cfg.LLM.OllamaModel)
	}
	return client, nil
}

// compositeClient выбирает провайдера per-request: primary для чата с
// автоматическим fallback при ошибке, fallback (Gemini) для эмбеддингов.
type compositeClient struct {
	primary  core.LLM
	fallback core.LLM
	embedder core.Embedder
	logger   *slog.Logger
}

func newCompositeClient(primary core.LLM, fb core.LLMClient, logger *slog.Logger) core.LLMClient {
	return &compositeClient{primary: primary, fallback: fb, embedder: fb, logger: logger}
}

func (c *compositeClient) Ask(ctx context.Context, prompt string) (string, error) {
	out, err := c.primary.Ask(ctx, prompt)
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return "", err
	}
	if c.logger != nil {
		c.logger.Warn("primary chat failed, retrying on fallback", "err", err)
	}
	return c.fallback.Ask(ctx, prompt)
}

func (c *compositeClient) AskWithSystem(ctx context.Context, systemAddition, userPrompt string) (string, error) {
	out, err := c.primary.AskWithSystem(ctx, systemAddition, userPrompt)
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return "", err
	}
	if c.logger != nil {
		c.logger.Warn("primary chat failed, retrying on fallback", "err", err)
	}
	return c.fallback.AskWithSystem(ctx, systemAddition, userPrompt)
}

func (c *compositeClient) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	return c.embedder.Embed(ctx, inputs)
}
