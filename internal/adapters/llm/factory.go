// Package llm содержит фабрику для выбора LLM-адаптера по конфигурации.
package llm

import (
	"fmt"
	"log/slog"
	"strings"

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
		return openaiadapter.NewClient(cfg, logger)
	case "ollama":
		return ollamaadapter.NewClient(cfg, logger)
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", provider)
	}
}
