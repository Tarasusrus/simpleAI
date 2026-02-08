// Package main содержит код пакета main и его задачи.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"simpleAI/config"
	"simpleAI/internal/db"
	"simpleAI/internal/ingest"
	"simpleAI/internal/tools"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	logger := tools.NewLogger()
	var filePath string
	flag.StringVar(&filePath, "file", "", "path to receipt JSON (defaults to stdin)")
	flag.Parse()

	logger.Info("ingest run started", "file", filePath)
	payload, err := readPayload(filePath)
	if err != nil {
		return fmt.Errorf("failed to read payload: %w", err)
	}
	logger.Info("ingest payload loaded", "bytes", len(payload))

	var input ingest.ReceiptInput
	if err := json.Unmarshal(payload, &input); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}
	logger.Info("ingest payload parsed", "source", input.Source, "source_ref", input.SourceRef, "items", len(input.Items))

	cfg, err := config.LoadDBConfig()
	if err != nil {
		return fmt.Errorf("failed to load db config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to connect db: %w", err)
	}
	defer pool.Close()
	logger.Info("ingest db connected")

	store := ingest.NewStore(pool)
	id, err := store.IngestReceipt(ctx, input)
	if err != nil {
		return fmt.Errorf("ingest failed: %w", err)
	}

	logger.Info("ingest run completed", "receipt_id", id.String())
	fmt.Println(id.String())
	return nil
}

func readPayload(filePath string) ([]byte, error) {
	if filePath != "" {
		return os.ReadFile(filePath)
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return nil, io.EOF
	}
	return io.ReadAll(os.Stdin)
}
