package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"simpleAI/internal/plugin"
)

type toolCall struct {
	Skill string         `json:"skill"`
	Input map[string]any `json:"input"`
}

// buildToolsSystemPrompt формирует дополнение к system prompt с описанием доступных tools.
func buildToolsSystemPrompt(manifests []plugin.Manifest) string {
	var sb strings.Builder
	now := time.Now()
	fmt.Fprintf(&sb,
		"Сегодня: %s (год %d, месяц %d). При указании месяца без года: если этот месяц уже прошёл или идёт сейчас — используй %d; если месяц ещё не наступил в этом году (например декабрь когда сейчас март) — используй %d.\n\n",
		now.Format("02.01.2006"), now.Year(), int(now.Month()), now.Year(), now.Year()-1,
	)
	sb.WriteString("У тебя есть доступ к инструментам. Если нужно вызвать инструмент — ответь ТОЛЬКО валидным JSON без markdown и пояснений.\n")
	sb.WriteString("Один вызов: {\"skill\": \"<id>\", \"input\": {<параметры>}}\n")
	sb.WriteString("Несколько вызовов за раз: [{\"skill\": \"<id>\", \"input\": {...}}, {\"skill\": \"<id>\", \"input\": {...}}]\n")
	sb.WriteString("\nROUTING RULES (приоритет выше описаний инструментов):\n")
	sb.WriteString("1. Будущая покупка / совет о покупке («хочу купить», «планирую купить», «думаю купить», «стоит ли купить», «можем ли позволить», «хватит ли денег на», «потянем ли», «что приоритетнее») — ВСЕГДА skill=advisor, action=advice. НИКОГДА не вызывай budget.add_expense для будущих/гипотетических покупок.\n")
	sb.WriteString("2. Запись СОВЕРШЁННОЙ траты (прошедшее время: «купил», «купила», «потратил», «заплатил», «оплатил») — skill=budget, action=add_expense.\n")
	sb.WriteString("3. Если в сообщении нет суммы и нет глагола в прошедшем времени — это НЕ add_expense.\n")
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
