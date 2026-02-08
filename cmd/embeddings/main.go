// Package main содержит код пакета main и его задачи.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	limit := flag.Int("limit", 100, "max documents per run")
	batch := flag.Int("batch", 20, "batch size for embeddings")
	flag.Parse()

	logger := tools.NewLogger()
	logger.Info("rag embeddings run started", "limit", *limit, "batch", *batch)

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DB)
	if err != nil {
		return fmt.Errorf("init db pool: %w", err)
	}
	defer pool.Close()

	store := rag.NewStore(pool)
	docs, err := store.ListPending(ctx, *limit)
	if err != nil {
		return fmt.Errorf("list pending documents: %w", err)
	}
	logger.Info("rag documents pending", "count", len(docs))
	if len(docs) == 0 {
		logger.Info("no pending rag documents")
		return nil
	}

	client, err := llmfactory.NewClient(cfg, logger)
	if err != nil {
		return fmt.Errorf("init llm client: %w", err)
	}

	for i := 0; i < len(docs); i += *batch {
		end := i + *batch
		if end > len(docs) {
			end = len(docs)
		}
		batchDocs := docs[i:end]
		logger.Info("rag embeddings batch started", "offset", i, "count", len(batchDocs))
		inputs := make([]string, 0, len(batchDocs))
		hashes := make([]string, 0, len(batchDocs))
		for _, doc := range batchDocs {
			inputs = append(inputs, doc.Content)
			hashes = append(hashes, hashContent(doc.Content))
		}

		embeddings, err := client.Embed(ctx, inputs)
		if err != nil {
			return fmt.Errorf("embed documents: %w", err)
		}

		for idx, doc := range batchDocs {
			if idx >= len(embeddings) || embeddings[idx] == nil {
				continue
			}
			if err := store.UpdateEmbedding(ctx, doc.ID, embeddings[idx], hashes[idx]); err != nil {
				return fmt.Errorf("update embedding: %w", err)
			}
		}
		logger.Info("rag embeddings batch completed", "offset", i, "count", len(batchDocs))
	}

	logger.Info("embeddings updated", "count", len(docs))
	return nil
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
