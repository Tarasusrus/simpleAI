package main

import (
	"context"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"

	"simpleAI/config"
	"simpleAI/internal/agent"
	"simpleAI/internal/telegram"
	"simpleAI/internal/tools"
	"simpleAI/pkg/llm"
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

	bot, err := tgbotapi.NewBotAPI(cfg.Telegram.Token)
	if err != nil {
		log.Fatal("Err while init telegram bot: ", err)
	}

	llmClient := llm.NewClient(cfg.APIKey, logger, cfg)
	agentService := agent.NewService(llmClient)

	router := telegram.NewRouter()
	router.Use(telegram.RequireAllowedChats(cfg.Telegram.AllowedChats))
	router.Use(telegram.LogUpdates())
	router.Use(telegram.LogDuration())
	router.Use(telegram.RateLimit(cfg.Telegram.RateLimit))
	router.HandleCommand("start", telegram.HandleStart)
	router.HandleCommand("help", telegram.HandleHelp)
	router.HandleDefault(telegram.HandleDefault)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = int(cfg.Telegram.PollingTimeout / time.Second)

	updates := bot.GetUpdatesChan(u)
	logger.Info("telegram bot started")

	workers := cfg.Telegram.Workers
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)

	for update := range updates {
		sem <- struct{}{}
		go func(update tgbotapi.Update) {
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			err := router.HandleUpdate(ctx, &telegram.Context{
				Bot:       bot,
				Update:    update,
				Logger:    logger,
				Agent:     agentService,
				Allowed:   cfg.Telegram.AllowedChats,
				RequestID: uuid.NewString(),
			})
			cancel()
			if err != nil {
				logger.Error("telegram update error", "err", err)
			}
		}(update)
	}
}
