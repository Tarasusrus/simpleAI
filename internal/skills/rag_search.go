// Package skills contains concrete Skill implementations for the agent registry.
package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
	"simpleAI/internal/rag"
)

// RAGSearchSkill searches the knowledge base using semantic similarity.
type RAGSearchSkill struct {
	retriever *rag.Retriever
	embedder  core.Embedder
}

// NewRAGSearchSkill creates a new RAGSearchSkill.
func NewRAGSearchSkill(retriever *rag.Retriever, embedder core.Embedder) *RAGSearchSkill {
	return &RAGSearchSkill{retriever: retriever, embedder: embedder}
}

// Manifest returns the skill descriptor.
func (s *RAGSearchSkill) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "rag_search",
		Name:        "RAG Search",
		Description: "Search the knowledge base using semantic similarity. Use this to find information from stored documents, receipts, notes, and other data.",
		Version:     "1.0.0",
		InputSchema: &plugin.Schema{
			Name:    "RAGSearchInput",
			Version: "1.0.0",
			JSON: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results (default 5)",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

type ragSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// Run executes the RAG search: embed → search → build prompt.
func (s *RAGSearchSkill) Run(ctx context.Context, input string) (string, error) {
	var req ragSearchInput
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if req.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}

	embeddings, err := s.embedder.Embed(ctx, []string{req.Query})
	if err != nil {
		return "", fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return "", fmt.Errorf("no embeddings returned")
	}

	results, err := s.retriever.Search(ctx, embeddings[0], limit, rag.SearchFilter{})
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}

	return rag.BuildPrompt(req.Query, results), nil
}
