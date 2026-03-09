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

const maxIterations = 10

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

// Ask отвечает на запрос пользователя.
// Если registry содержит skills — запускает agentic loop:
// LLM может вызвать один или несколько инструментов за итерацию,
// результаты передаются обратно до получения финального текстового ответа.
func (s *Service) Ask(ctx context.Context, input string) (string, error) {
	if s.registry == nil || len(s.registry.List()) == 0 {
		return s.client.Ask(ctx, input)
	}

	toolSystemPrompt := buildToolsSystemPrompt(s.registry.List())

	currentPrompt := input
	var accumulatedResults []string

	for range maxIterations {
		resp, err := s.client.AskWithSystem(ctx, toolSystemPrompt, currentPrompt)
		if err != nil {
			return "", err
		}

		calls := parseToolCalls(resp)
		if len(calls) == 0 {
			// Финальный текстовый ответ — выходим из цикла.
			return resp, nil
		}

		// Выполняем все tool calls последовательно.
		for i, call := range calls {
			result, err := s.runSkill(ctx, call)
			if err != nil {
				accumulatedResults = append(accumulatedResults,
					fmt.Sprintf("[%d] %s → Ошибка: %v", i+1, call.Skill, err))
			} else {
				accumulatedResults = append(accumulatedResults,
					fmt.Sprintf("[%d] %s → %s", i+1, call.Skill, result))
			}
		}

		// Передаём накопленный контекст обратно LLM.
		currentPrompt = fmt.Sprintf(
			"[Исходный запрос]: %s\n\n[Результаты инструментов]:\n%s\n\nОтветь пользователю или вызови ещё инструменты если нужно.",
			input, strings.Join(accumulatedResults, "\n"),
		)
	}

	// Превышен лимит итераций — финальный ответ без tool calling.
	return s.client.Ask(ctx, currentPrompt)
}

func (s *Service) runSkill(ctx context.Context, call toolCall) (string, error) {
	skill, ok := s.registry.Get(call.Skill)
	if !ok {
		return "", fmt.Errorf("unknown skill: %s", call.Skill)
	}
	inputJSON, err := json.Marshal(call.Input)
	if err != nil {
		return "", fmt.Errorf("marshal skill input: %w", err)
	}
	return skill.Run(ctx, string(inputJSON))
}

type toolCall struct {
	Skill string         `json:"skill"`
	Input map[string]any `json:"input"`
}

// parseToolCalls извлекает все tool calls из ответа LLM.
// Поддерживает: одиночный JSON-объект, JSON-массив объектов, JSON внутри markdown.
func parseToolCalls(resp string) []toolCall {
	trimmed := strings.TrimSpace(resp)

	// Извлечь из markdown code block.
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
		if calls, ok := unmarshalCalls(strings.TrimSpace(inner[:end])); ok {
			return calls
		}
	}

	// Попробовать как JSON-массив или одиночный объект напрямую.
	if calls, ok := unmarshalCalls(trimmed); ok {
		return calls
	}

	// Найти первый JSON-объект/массив в тексте.
	for _, start := range []string{"[", "{"} {
		idx := strings.Index(trimmed, start)
		if idx == -1 {
			continue
		}
		var endChar string
		if start == "[" {
			endChar = "]"
		} else {
			endChar = "}"
		}
		end := strings.LastIndex(trimmed, endChar)
		if end > idx {
			if calls, ok := unmarshalCalls(trimmed[idx : end+1]); ok {
				return calls
			}
		}
	}

	return nil
}

// unmarshalCalls пробует распарсить s как []toolCall или одиночный toolCall.
func unmarshalCalls(s string) ([]toolCall, bool) {
	// Попробовать как массив.
	var arr []toolCall
	if err := json.Unmarshal([]byte(s), &arr); err == nil {
		var valid []toolCall
		for _, c := range arr {
			if c.Skill != "" {
				valid = append(valid, c)
			}
		}
		if len(valid) > 0 {
			return valid, true
		}
	}

	// Попробовать как одиночный объект.
	var single toolCall
	if err := json.Unmarshal([]byte(s), &single); err == nil && single.Skill != "" {
		return []toolCall{single}, true
	}

	return nil, false
}

// buildToolsSystemPrompt формирует дополнение к system prompt с описанием доступных tools.
func buildToolsSystemPrompt(manifests []plugin.Manifest) string {
	var sb strings.Builder
	sb.WriteString("У тебя есть доступ к инструментам. Если нужно вызвать инструмент — ответь ТОЛЬКО валидным JSON без markdown и пояснений.\n")
	sb.WriteString("Один вызов: {\"skill\": \"<id>\", \"input\": {<параметры>}}\n")
	sb.WriteString("Несколько вызовов за раз: [{\"skill\": \"<id>\", \"input\": {...}}, {\"skill\": \"<id>\", \"input\": {...}}]\n")
	sb.WriteString("\nДоступные инструменты:\n")
	for _, m := range manifests {
		fmt.Fprintf(&sb, "- %s: %s\n", m.ID, m.Description)
		if m.InputSchema != nil {
			b, err := json.Marshal(m.InputSchema.JSON)
			if err == nil {
				fmt.Fprintf(&sb, "  Параметры: %s\n", string(b))
			}
		}
	}
	sb.WriteString("\nЕсли инструменты не нужны — отвечай обычным текстом.")
	return sb.String()
}
