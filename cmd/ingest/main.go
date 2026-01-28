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
)

func main() {
	var filePath string
	flag.StringVar(&filePath, "file", "", "path to receipt JSON (defaults to stdin)")
	flag.Parse()

	payload, err := readPayload(filePath)
	if err != nil {
		log.Fatal("failed to read payload: ", err)
	}

	var input ingest.ReceiptInput
	if err := json.Unmarshal(payload, &input); err != nil {
		log.Fatal("failed to parse JSON: ", err)
	}

	cfg, err := config.LoadDBConfig()
	if err != nil {
		log.Fatal("failed to load db config: ", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg)
	if err != nil {
		log.Fatal("failed to connect db: ", err)
	}
	defer pool.Close()

	store := ingest.NewStore(pool)
	id, err := store.IngestReceipt(ctx, input)
	if err != nil {
		log.Fatal("ingest failed: ", err)
	}

	fmt.Println(id.String())
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
