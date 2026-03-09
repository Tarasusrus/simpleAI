// Package llm содержит фабрику для выбора LLM-адаптера по конфигурации.
package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
// Если настроены оба ключа — создаёт составной клиент:
// primary (DeepSeek) для чата, fallback (Gemini) для эмбеддингов.
func buildOpenAIClient(cfg config.Config, logger *slog.Logger) (core.LLMClient, error) {
	fallback, fallbackErr := openaiadapter.NewFallbackClient(cfg, logger)
	if fallbackErr != nil && logger != nil {
		logger.Warn("fallback llm not available", "err", fallbackErr)
	}

	primary, primaryErr := openaiadapter.NewClient(cfg, logger)
	if primaryErr != nil {
		if logger != nil {
			logger.Warn("primary llm not available, trying fallback", "err", primaryErr)
		}
		if fallback != nil {
			if logger != nil {
				logger.Info("llm provider active", "provider", "fallback", "model", cfg.LLM.FallbackChatModel)
			}
			return fallback, nil
		}
		return fallbackToOllama(cfg, logger, primaryErr)
	}

	if err := testLLM(primary); err != nil {
		if logger != nil {
			logger.Warn("primary llm test failed, trying fallback", "err", err)
		}
		if fallback != nil {
			if logger != nil {
				logger.Info("llm provider active", "provider", "fallback", "model", cfg.LLM.FallbackChatModel)
			}
			return fallback, nil
		}
		return fallbackToOllama(cfg, logger, err)
	}

	if logger != nil {
		logger.Info("llm provider active", "provider", "primary", "model", cfg.LLM.ChatModel)
	}

	// Составной клиент: primary для чата, fallback для эмбеддингов.
	if fallback != nil {
		return newCompositeClient(primary, fallback), nil
	}
	return primary, nil
}

func testLLM(client core.LLM) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Ask(ctx, "ping")
	return err
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

// compositeClient использует разных провайдеров для чата и эмбеддингов.
type compositeClient struct {
	chat    core.LLM
	embedder core.Embedder
}

func newCompositeClient(chat core.LLM, embedder core.Embedder) core.LLMClient {
	return &compositeClient{chat: chat, embedder: embedder}
}

func (c *compositeClient) Ask(ctx context.Context, prompt string) (string, error) {
	return c.chat.Ask(ctx, prompt)
}

func (c *compositeClient) AskWithSystem(ctx context.Context, systemAddition, userPrompt string) (string, error) {
	return c.chat.AskWithSystem(ctx, systemAddition, userPrompt)
}

func (c *compositeClient) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	return c.embedder.Embed(ctx, inputs)
}
