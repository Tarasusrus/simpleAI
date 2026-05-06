package agent

import "simpleAI/internal/plugin"

// ParsedCall — один tool call, извлечённый из ответа LLM.
// Action отдельно вытащен из Input для удобства (skills используют поле "action" в payload).
type ParsedCall struct {
	Skill  string
	Action string
	Input  map[string]any
}

// ParseCalls — публичная обёртка над парсером tool calls.
// Используется eval-харнесом для оценки routing-решений LLM
// без выполнения skills и без agent loop.
func ParseCalls(resp string) []ParsedCall {
	raw := parseToolCalls(resp)
	out := make([]ParsedCall, 0, len(raw))
	for _, c := range raw {
		action, ok := c.Input["action"].(string)
		if !ok {
			action = ""
		}
		out = append(out, ParsedCall{Skill: c.Skill, Action: action, Input: c.Input})
	}
	return out
}

// BuildToolsSystemPrompt — публичная обёртка над генератором system prompt.
// Eval-харнес обязан использовать ИМЕННО этот промпт, иначе routing-метрики
// не отражают реального поведения agent loop.
func BuildToolsSystemPrompt(manifests []plugin.Manifest) string {
	return buildToolsSystemPrompt(manifests)
}
