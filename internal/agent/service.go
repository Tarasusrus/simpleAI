// Package agent contains agent package code and its tasks.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
)

// Service wraps an LLM client with optional skill registry for tool calling.
type Service struct {
	client   core.LLM
	registry *plugin.Registry
}

func NewService(client core.LLM) *Service {
	return &Service{client: client}
}

func NewServiceWithRegistry(client core.LLM, registry *plugin.Registry) *Service {
	return &Service{client: client, registry: registry}
}

// Ask отвечает на запрос пользователя. Если registry содержит skills,
// LLM получает инструкции по tool calling в system message (через AskWithSystem).
// При обнаружении tool call — выполняет skill и возвращает финальный ответ LLM.
func (s *Service) Ask(ctx context.Context, input string) (string, error) {
	if s.registry == nil || len(s.registry.List()) == 0 {
		return s.client.Ask(ctx, input)
	}

	// Tool calling инструкции идут в system message, а не в user message.
	toolSystemPrompt := buildToolsSystemPrompt(s.registry.List())
	resp, err := s.client.AskWithSystem(ctx, toolSystemPrompt, input)
	if err != nil {
		return "", err
	}

	// Пытаемся извлечь tool call JSON из ответа.
	call, ok := parseToolCall(resp)
	if !ok {
		return resp, nil
	}

	skill, ok := s.registry.Get(call.Skill)
	if !ok {
		return "", fmt.Errorf("unknown skill: %s", call.Skill)
	}

	inputJSON, err := json.Marshal(call.Input)
	if err != nil {
		return "", fmt.Errorf("marshal skill input: %w", err)
	}

	result, err := skill.Run(ctx, string(inputJSON))
	if err != nil {
		return "", fmt.Errorf("skill %s: %w", call.Skill, err)
	}

	// Финальный ответ: LLM синтезирует ответ из результата skill.
	return s.client.Ask(ctx, result)
}

type toolCall struct {
	Skill string         `json:"skill"`
	Input map[string]any `json:"input"`
}

// parseToolCall пытается извлечь JSON tool call из ответа LLM.
// Поддерживает: чистый JSON, JSON в markdown ```json``` блоке, JSON внутри текста.
func parseToolCall(resp string) (toolCall, bool) {
	for _, candidate := range extractJSONCandidates(resp) {
		var call toolCall
		if err := json.Unmarshal([]byte(candidate), &call); err == nil && call.Skill != "" {
			return call, true
		}
	}
	return toolCall{}, false
}

// extractJSONCandidates возвращает потенциальные JSON-строки из ответа LLM.
func extractJSONCandidates(resp string) []string {
	trimmed := strings.TrimSpace(resp)
	candidates := []string{trimmed}

	// Извлечь из markdown code block: ```json ... ``` или ``` ... ```
	for _, fence := range []string{"```json", "```"} {
		start := strings.Index(trimmed, fence)
		if start == -1 {
			continue
		}
		inner := trimmed[start+len(fence):]
		end := strings.Index(inner, "```")
		if end == -1 {
			continue
		}
		candidates = append(candidates, strings.TrimSpace(inner[:end]))
	}

	// Найти первый JSON-объект в тексте по { ... }
	start := strings.Index(trimmed, "{")
	if start != -1 {
		end := strings.LastIndex(trimmed, "}")
		if end > start {
			candidates = append(candidates, trimmed[start:end+1])
		}
	}

	return candidates
}

// buildToolsSystemPrompt формирует дополнение к system prompt с описанием доступных tools.
func buildToolsSystemPrompt(manifests []plugin.Manifest) string {
	var sb strings.Builder
	sb.WriteString("У тебя есть доступ к следующим инструментам для поиска информации.\n")
	sb.WriteString("Если запрос пользователя требует поиска данных — ответь ТОЛЬКО валидным JSON (без markdown, без пояснений):\n")
	sb.WriteString(`{"skill": "<id>", "input": {<параметры>}}`)
	sb.WriteString("\n\nДоступные инструменты:\n")
	for _, m := range manifests {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", m.ID, m.Description))
		if m.InputSchema != nil {
			b, err := json.Marshal(m.InputSchema.JSON)
			if err == nil {
				sb.WriteString(fmt.Sprintf("  Параметры: %s\n", string(b)))
			}
		}
	}
	sb.WriteString("\nЕсли инструмент не нужен — отвечай обычным текстом.")
	return sb.String()
}
