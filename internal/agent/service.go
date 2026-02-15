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

// Ask answers the query, optionally routing through a skill if the registry
// has registered skills and the LLM decides to invoke one.
func (s *Service) Ask(ctx context.Context, input string) (string, error) {
	if s.registry == nil || len(s.registry.List()) == 0 {
		return s.client.Ask(ctx, input)
	}

	// Build prompt that lists available tools and instructs LLM on format.
	fullPrompt := buildToolsPrompt(s.registry.List()) + "\n\nUser: " + input

	resp, err := s.client.Ask(ctx, fullPrompt)
	if err != nil {
		return "", err
	}

	// Try to detect a tool call in the response.
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

	// Feed the skill result back to the LLM to produce a natural language answer.
	return s.client.Ask(ctx, result)
}

type toolCall struct {
	Skill string         `json:"skill"`
	Input map[string]any `json:"input"`
}

func parseToolCall(resp string) (toolCall, bool) {
	trimmed := strings.TrimSpace(resp)
	var call toolCall
	if err := json.Unmarshal([]byte(trimmed), &call); err != nil {
		return toolCall{}, false
	}
	return call, call.Skill != ""
}

func buildToolsPrompt(manifests []plugin.Manifest) string {
	var sb strings.Builder
	sb.WriteString("You have access to the following tools. If the user's request requires")
	sb.WriteString(" information retrieval or data lookup, respond ONLY with a JSON tool call")
	sb.WriteString(" in this exact format (no other text):\n")
	sb.WriteString(`{"skill": "<tool_id>", "input": {<tool_input>}}`)
	sb.WriteString("\n\nAvailable tools:\n")
	for _, m := range manifests {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", m.ID, m.Description))
		if m.InputSchema != nil {
			schemaBytes, _ := json.Marshal(m.InputSchema.JSON)
			sb.WriteString(fmt.Sprintf("  Input schema: %s\n", string(schemaBytes)))
		}
	}
	sb.WriteString("\nIf you can answer directly without a tool, respond normally.")
	return sb.String()
}
