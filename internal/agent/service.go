// Package agent contains agent package code and its tasks.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
	"simpleAI/internal/trace"
)

const maxIterations = 10

// Service wraps an LLM client with optional skill registry for tool calling.
type Service struct {
	client   core.LLM
	registry *plugin.Registry
	tracer   *trace.Store
	logger   *slog.Logger
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

// Ask отвечает на запрос пользователя.
// Если registry содержит skills — запускает agentic loop:
// LLM может вызвать один или несколько инструментов за итерацию,
// результаты передаются обратно до получения финального текстового ответа.
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
		return s.client.Ask(ctx, input)
	}

	sessionID := uuid.New()
	toolSystemPrompt := buildToolsSystemPrompt(s.registry.List())

	currentPrompt := input
	var accumulatedResults []string
	iteration := 0

	for range maxIterations {
		iteration++
		resp, err := s.client.AskWithSystem(ctx, toolSystemPrompt, currentPrompt)
		if err != nil {
			return "", err
		}

		calls := parseToolCalls(resp)
		if len(calls) == 0 {
			// Финальный текстовый ответ — выходим из цикла.
			s.appendTrace(ctx, trace.Entry{
				SessionID:   sessionID,
				ChatID:      chatID,
				UserInput:   input,
				Iteration:   iteration,
				LLMResponse: resp,
				IsFinal:     true,
			})
			return resp, nil
		}

		// Выполняем все tool calls последовательно.
		for i, call := range calls {
			inputJSON, err := json.Marshal(call.Input)
			if err != nil {
				inputJSON = []byte("{}")
			}

			skillStart := time.Now()
			result, err := s.runSkill(ctx, call)
			skillDuration := time.Since(skillStart).Milliseconds()

			skillName := call.Skill
			var skillResult *string
			if err != nil {
				msg := fmt.Sprintf("Ошибка: %v", err)
				skillResult = &msg
				s.logger.ErrorContext(ctx, "skill error",
					"skill", call.Skill,
					"input", string(inputJSON),
					"duration_ms", skillDuration,
					"iteration", iteration,
					"err", err,
				)
				accumulatedResults = append(accumulatedResults,
					fmt.Sprintf("[%d] %s → %s", i+1, call.Skill, msg))
			} else {
				skillResult = &result
				s.logger.InfoContext(ctx, "skill called",
					"skill", call.Skill,
					"input", string(inputJSON),
					"duration_ms", skillDuration,
					"iteration", iteration,
				)
				accumulatedResults = append(accumulatedResults,
					fmt.Sprintf("[%d] %s → %s", i+1, call.Skill, result))
			}

			s.appendTrace(ctx, trace.Entry{
				SessionID:   sessionID,
				ChatID:      chatID,
				UserInput:   input,
				Iteration:   iteration,
				Skill:       &skillName,
				SkillInput:  inputJSON,
				SkillResult: skillResult,
				LLMResponse: resp,
				IsFinal:     false,
			})
		}

		// Передаём накопленный контекст обратно LLM.
		// ВАЖНО: не повторяем исходный запрос дословно, чтобы LLM не вызвала инструменты повторно.
		currentPrompt = fmt.Sprintf(
			"Инструменты уже выполнены. Результаты:\n%s\n\nВерни результат инструмента пользователю дословно, без изменений и пересказа. НЕ вызывай инструменты повторно.",
			strings.Join(accumulatedResults, "\n"),
		)
	}

	// Превышен лимит итераций — финальный ответ без tool calling.
	return s.client.Ask(ctx, currentPrompt)
}

// appendTrace записывает трейс, если tracer настроен. Ошибки не прерывают работу агента.
func (s *Service) appendTrace(ctx context.Context, e trace.Entry) {
	if s.tracer == nil {
		return
	}
	if err := s.tracer.Append(ctx, e); err != nil {
		s.logger.WarnContext(ctx, "trace append failed", "err", err, "session_id", e.SessionID)
	}
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
