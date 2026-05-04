// Package main contains code for main package and its tasks.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"

	"simpleAI/config"
	llmfactory "simpleAI/internal/adapters/llm"
	telegramadapter "simpleAI/internal/adapters/telegram"
	"simpleAI/internal/agent"
	"simpleAI/internal/core"
	"simpleAI/internal/db"
	"simpleAI/internal/plugin"
	"simpleAI/internal/rag"
	"simpleAI/internal/skills"
	"simpleAI/internal/telegram"
	"simpleAI/internal/tools"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Err while load the config: ", err)
	}

	logger := tools.NewLogger()
	if cfg.Telegram.Token == "" {
		logger.Error("telegram bot token is empty")
		return
	}
	if cfg.Telegram.MediaDir != "" {
		if err := os.MkdirAll(cfg.Telegram.MediaDir, 0o755); err != nil {
			logger.Error("failed to create media dir", "err", err, "dir", cfg.Telegram.MediaDir)
			return
		}
	}

	adapter, err := telegramadapter.NewAdapter(cfg.Telegram.Token, cfg.Telegram.PollingTimeout)
	if err != nil {
		log.Fatal("Err while init telegram bot: ", err)
	}
	if err := adapter.SetCommands(context.Background(), []telegramadapter.Command{
		{Command: "start", Description: "Приветствие и краткая справка"},
		{Command: "help", Description: "Список доступных команд"},
		{Command: "budget", Description: "💰 Бюджет: расходы, доходы, цели, долги"},
		{Command: "recurring", Description: "🔄 Регулярные платежи"},
		{Command: "forecast", Description: "🔮 Прогноз трат на следующий месяц"},
		{Command: "reminders", Description: "🔔 Напоминания о внесении трат"},
	}); err != nil {
		logger.Warn("failed to set telegram commands", "err", err)
	}

	llmClient, err := llmfactory.NewClient(cfg, logger)
	if err != nil {
		log.Fatal("Err while init llm client: ", err)
	}

	registry := buildRegistry(cfg, llmClient, logger)
	agentService := agent.NewServiceWithRegistry(llmClient, registry)

	router := telegram.NewRouter()
	router.Use(telegram.DeduplicateUpdates(1000))
	router.Use(telegram.RequireAllowedChats(cfg.Telegram.AllowedChats))
	router.Use(telegram.LogUpdates())
	router.Use(telegram.LogDuration())
	router.Use(telegram.RateLimit(cfg.Telegram.RateLimit))
	router.HandleCommand("start", telegram.HandleStart)
	router.HandleCommand("help", telegram.HandleHelp)
	router.HandleCommand("budget", telegram.HandleBudget)
	router.HandleCommand("recurring", telegram.HandleRecurring)
	router.HandleCommand("forecast", telegram.HandleForecast)
	router.HandleCommand("reminders", telegram.HandleReminders)
	router.HandleDefault(telegram.HandleDefault)

	updates, err := adapter.Updates(context.Background())
	if err != nil {
		log.Fatal("Err while init telegram updates: ", err)
	}
	logger.Info("telegram bot started")

	workers := cfg.Telegram.Workers
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)

	for update := range updates {
		sem <- struct{}{}
		go func(update core.Update) {
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			err := router.HandleUpdate(ctx, &telegram.Context{
				Bot:       adapter,
				Update:    update,
				Logger:    logger,
				Agent:     agentService,
				Fetcher:   adapter,
				Allowed:   cfg.Telegram.AllowedChats,
				RequestID: uuid.NewString(),
				MediaDir:  cfg.Telegram.MediaDir,
				Registry:  registry,
			})
			cancel()
			if err != nil {
				logger.Error("telegram update error", "err", err)
			}
		}(update)
	}
}

// buildRegistry creates a plugin.Registry with all available skills.
// If the database is unavailable the registry is returned empty so the bot
// continues to work without RAG.
func buildRegistry(cfg config.Config, llmClient core.LLMClient, logger *slog.Logger) *plugin.Registry {
	registry := plugin.NewRegistry()

	pool, err := db.NewPool(context.Background(), cfg.DB)
	if err != nil {
		logger.Error("failed to connect to db, RAG skill disabled", "err", err)
		return registry
	}

	retriever := rag.NewRetriever(pool)
	ragSkill := skills.NewRAGSearchSkill(retriever, llmClient)
	if err := registry.Register(ragSkill); err != nil {
		logger.Error("failed to register rag_search skill", "err", err)
		return registry
	}

	logger.Info("registry ready", "skills", len(registry.List()))
	return registry
}
