// Package main is the entry point for the MCP (Model Context Protocol) server.
// It exposes the plugin registry as MCP tools over HTTP/SSE on :8080.
package main

import (
	"context"
	"log"

	"simpleAI/config"
	llmfactory "simpleAI/internal/adapters/llm"
	"simpleAI/internal/db"
	mcpserver "simpleAI/internal/mcp"
	"simpleAI/internal/plugin"
	"simpleAI/internal/rag"
	"simpleAI/internal/skills"
	"simpleAI/internal/tools"

	"github.com/mark3labs/mcp-go/server"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("failed to load config: ", err)
	}

	logger := tools.NewLogger()

	pool, err := db.NewPool(context.Background(), cfg.DB)
	if err != nil {
		log.Fatal("failed to connect to db: ", err)
	}

	llmClient, err := llmfactory.NewClient(cfg, logger)
	if err != nil {
		log.Fatal("failed to init llm client: ", err)
	}

	registry := plugin.NewRegistry()

	retriever := rag.NewRetriever(pool)
	ragSkill := skills.NewRAGSearchSkill(retriever, llmClient)
	if err := registry.Register(ragSkill); err != nil {
		log.Fatal("failed to register rag_search skill: ", err)
	}

	mcpSrv := mcpserver.Build(registry, "simpleai", "1.0.0")

	sseSrv := server.NewSSEServer(mcpSrv,
		server.WithBaseURL("http://localhost:8080"),
	)

	logger.Info("MCP SSE server starting", "addr", ":8080", "endpoint", "/sse")
	if err := sseSrv.Start(":8080"); err != nil {
		log.Fatal("MCP server error: ", err)
	}
}
