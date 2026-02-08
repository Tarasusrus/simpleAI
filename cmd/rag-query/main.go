// Package main содержит код пакета main и его задачи.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"simpleAI/config"
	llmfactory "simpleAI/internal/adapters/llm"
	"simpleAI/internal/db"
	"simpleAI/internal/rag"
	"simpleAI/internal/tools"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	query := flag.String("q", "", "search query")
	limit := flag.Int("limit", 5, "max results")
	flag.Parse()

	if *query == "" {
		return fmt.Errorf("query is required")
	}

	logger := tools.NewLogger()
	logger.Info("rag query started", "limit", *limit)

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DB)
	if err != nil {
		return fmt.Errorf("init db pool: %w", err)
	}
	defer pool.Close()

	client, err := llmfactory.NewClient(cfg, logger)
	if err != nil {
		return fmt.Errorf("init llm client: %w", err)
	}
	embeddings, err := client.Embed(ctx, []string{*query})
	if err != nil {
		return fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 || embeddings[0] == nil {
		return fmt.Errorf("empty embedding result")
	}
	logger.Info("rag query embedded")

	retriever := rag.NewRetriever(pool)
	results, err := retriever.Search(ctx, embeddings[0], *limit, rag.SearchFilter{})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	logger.Info("rag query results", "count", len(results))

	prompt := rag.BuildPrompt(*query, results)
	fmt.Println(prompt)
	return nil
}
