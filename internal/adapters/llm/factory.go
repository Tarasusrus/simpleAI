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
func NewClient(cfg config.Config, logger *slog.Logger) (core.LLMClient, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.LLM.Provider))
	if provider == "" {
		provider = "openai"
	}

	switch provider {
	case "openai":
		client, err := openaiadapter.NewClient(cfg, logger)
		if err != nil {
			return fallbackToOllama(cfg, logger, err)
		}
		if err := testLLM(client); err != nil {
			return fallbackToOllama(cfg, logger, err)
		}
		return client, nil
	case "ollama":
		return ollamaadapter.NewClient(cfg, logger)
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", provider)
	}
}

func testLLM(client core.LLM) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Ask(ctx, "ping")
	return err
}

func fallbackToOllama(cfg config.Config, logger *slog.Logger, cause error) (core.LLMClient, error) {
	if logger != nil && cause != nil {
		logger.Warn("LLM provider failed, falling back to Ollama", "err", cause)
	}
	return ollamaadapter.NewClient(cfg, logger)
}
