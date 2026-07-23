// Command botclient — прямой тест-клиент к ядру бота (agent.Service),
// в обход Telegram-транспорта. Шлёт текст в AskWithMeta и печатает ответ.
//
// По умолчанию read-only: подключается к реплике ролью botclient_ro (запись
// отклоняется БД). Запись — только с --allow-writes и BOTCLIENT_DATABASE_URL_RW.
//
// Пример:
//
//	set -a; . ~/.simpleai-replica/botclient.env; set +a
//	go run ./cmd/botclient "пришло 127000₽, сколько свободных осталось?"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/config"
	llmfactory "simpleAI/internal/adapters/llm"
	"simpleAI/internal/appwiring"
	"simpleAI/internal/botclient"
	"simpleAI/internal/tools"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var (
		allowWrites bool
		chatID      int64
	)
	flag.BoolVar(&allowWrites, "allow-writes", false, "разрешить мутации БД (по умолчанию read-only реплика)")
	flag.Int64Var(&chatID, "chat-id", 420229961, "telegram chat_id для chat-scoped skills")
	flag.Parse()

	prompt := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("укажи текст запроса: botclient [флаги] \"<текст>\"")
	}

	dbURL, err := botclient.ResolveDatabaseURL(botclient.Options{
		AllowWrites: allowWrites,
		ReadOnlyURL: os.Getenv("BOTCLIENT_DATABASE_URL"),
		WriteURL:    os.Getenv("BOTCLIENT_DATABASE_URL_RW"),
	})
	if err != nil {
		return err
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger := tools.NewLogger()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("db pool: %w", err)
	}
	defer pool.Close()

	llmClient, err := llmfactory.NewClient(cfg, logger)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	registry, _ := appwiring.BuildRegistry(llmClient, logger, pool, cfg.LLM.AdvisorLLMTimeout)
	svc := appwiring.BuildAgentService(llmClient, registry, nil, nil, logger, cfg.LLM.ChatModel)

	mode := "read-only"
	if allowWrites {
		mode = "READ-WRITE"
	}
	logger.Info("botclient", "mode", mode, "chat_id", chatID)

	answer, err := botclient.Ask(ctx, svc, prompt, chatID)
	if err != nil {
		return fmt.Errorf("ask: %w", err)
	}
	fmt.Println(answer)
	return nil
}
