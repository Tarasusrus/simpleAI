package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"simpleAI/internal/observability"
	"simpleAI/internal/trace"
)

// runAgentLoop executes the agentic iteration loop and returns the final answer.
func (s *Service) runAgentLoop(
	ctx context.Context,
	input string,
	sessionID uuid.UUID,
	obsTrace *observability.Trace,
	chatID *int64,
) (string, error) {
	toolSystemPrompt := buildToolsSystemPrompt(s.registry.List())
	currentPrompt := input
	var accumulatedResults []string
	iteration := 0

	for range maxIterations {
		iteration++
		gen := obsTrace.StartGeneration(fmt.Sprintf("llm.iter%d", iteration), s.llmModel, map[string]any{
			"system": toolSystemPrompt,
			"prompt": currentPrompt,
		})
		resp, err := s.client.AskWithSystem(ctx, toolSystemPrompt, currentPrompt)
		gen.End(resp, err)
		if err != nil {
			return "", err
		}

		calls := parseToolCalls(resp)
		if len(calls) == 0 {
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

		for i, call := range calls {
			inputJSON, err := json.Marshal(call.Input)
			if err != nil {
				inputJSON = []byte("{}")
			}

			skillStart := time.Now()
			sp := obsTrace.StartSpan("tool."+call.Skill, call.Input)
			result, err := s.runSkill(ctx, call)
			sp.End(result, err)
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

		currentPrompt = fmt.Sprintf(
			"Инструменты уже выполнены. Результаты:\n%s\n\nВерни результат инструмента пользователю дословно, без изменений и пересказа. НЕ вызывай инструменты повторно.",
			strings.Join(accumulatedResults, "\n"),
		)
	}

	// Превышен лимит итераций — финальный ответ без tool calling.
	gen := obsTrace.StartGeneration("llm.final", s.llmModel, currentPrompt)
	resp, err := s.client.Ask(ctx, currentPrompt)
	gen.End(resp, err)
	return resp, err
}

// runSkill выполняет skill по toolCall.
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

// appendTrace записывает трейс, если tracer настроен. Ошибки не прерывают работу агента.
func (s *Service) appendTrace(ctx context.Context, e trace.Entry) {
	if s.tracer == nil {
		return
	}
	if err := s.tracer.Append(ctx, e); err != nil {
		s.logger.WarnContext(ctx, "trace append failed", "err", err, "session_id", e.SessionID)
	}
}
