// Package mcp provides an MCP (Model Context Protocol) server adapter
// that exposes the plugin.Registry as MCP tools over HTTP/SSE.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"simpleAI/internal/plugin"
)

// Build creates an MCPServer with one tool per registered skill.
// Each skill is exposed as an MCP tool with a single "input" JSON string
// parameter that maps directly to plugin.Skill.Run.
func Build(registry *plugin.Registry, name, version string) *server.MCPServer {
	s := server.NewMCPServer(name, version, server.WithToolCapabilities(false))

	for _, manifest := range registry.List() {
		m := manifest // capture loop variable

		tool := mcp.NewTool(
			m.ID,
			mcp.WithDescription(m.Description),
			mcp.WithString("input",
				mcp.Required(),
				mcp.Description(buildInputDescription(m)),
			),
		)

		skillID := m.ID
		s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			skill, ok := registry.Get(skillID)
			if !ok {
				return mcp.NewToolResultError(fmt.Sprintf("skill not found: %s", skillID)), nil
			}

			// Accept either a raw JSON string or a map from the "input" argument.
			rawInput, err := extractInput(req)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			result, err := skill.Run(ctx, rawInput)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
		})
	}

	return s
}

// extractInput reads the "input" argument from the request.
// It supports both string and object values for ergonomic usage from
// Claude Code (which may pass the arguments as a JSON object directly).
func extractInput(req mcp.CallToolRequest) (string, error) {
	args := req.GetArguments()
	raw, ok := args["input"]
	if !ok {
		return "{}", nil
	}

	switch v := raw.(type) {
	case string:
		return v, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal input: %w", err)
		}
		return string(b), nil
	}
}

func buildInputDescription(m plugin.Manifest) string {
	if m.InputSchema == nil {
		return "JSON input for the skill"
	}
	b, err := json.Marshal(m.InputSchema.JSON)
	if err != nil {
		return "JSON input for the skill"
	}
	return fmt.Sprintf("JSON input matching schema: %s", string(b))
}
