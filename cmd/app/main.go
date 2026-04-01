// Package main — единая точка входа приложения.
// Запускает Telegram-бот, MCP SSE-сервер и mail-worker в одном процессе.
// Завершение по Ctrl+C / SIGTERM с graceful shutdown.
package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/server"

	"simpleAI/config"
	llmfactory "simpleAI/internal/adapters/llm"
	telegramadapter "simpleAI/internal/adapters/telegram"
	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/core"
	"simpleAI/internal/db"
	"simpleAI/internal/mail"
	mcpserver "simpleAI/internal/mcp"
	"simpleAI/internal/notify"
	"simpleAI/internal/plugin"
	"simpleAI/internal/rag"
	"simpleAI/internal/rates"
	"simpleAI/internal/skills"
	"simpleAI/internal/telegram"
	"simpleAI/internal/tools"
	"simpleAI/internal/trace"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("failed to load config: ", err)
	}

	logger := tools.NewLogger()

	if err := db.Migrate(context.Background(), cfg.DB); err != nil {
		log.Fatal("failed to run migrations: ", err)
	}

	pool, err := db.NewPool(context.Background(), cfg.DB)
	if err != nil {
		log.Fatal("failed to connect to db: ", err)
	}

	llmClient, err := llmfactory.NewClient(cfg, logger)
	if err != nil {
		pool.Close()
		log.Fatal("failed to init llm client: ", err)
	}

	// Единый реестр skills для всех компонентов.
	registry := buildRegistry(llmClient, logger, pool)

	// Контекст с graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup

	tracer := trace.NewStore(pool)

	// --- Telegram бот ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := runTelegram(ctx, cfg, logger, llmClient, registry, tracer); err != nil {
			logger.Error("telegram stopped", "err", err)
		}
	}()

	// --- MCP SSE сервер ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := runMCP(ctx, logger, registry); err != nil {
			logger.Error("mcp stopped", "err", err)
		}
	}()

	// --- Mail worker (только если настроены аккаунты) ---
	if len(cfg.Mail.Accounts) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runWorker(ctx, cfg, logger, pool, llmClient)
		}()
	} else {
		logger.Info("mail worker skipped: no accounts configured")
	}

	// --- Reminder worker ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		budgetStore := budget.NewStore(pool)
		tg := notify.NewTelegram(cfg.Telegram.Token, "")
		worker := notify.NewReminderWorker(budgetStore, tg, logger)
		logger.Info("reminder worker started")
		worker.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		budgetStore := budget.NewStore(pool)
		ratesWorker := rates.New(budgetStore)
		logger.Info("rates worker started")
		ratesWorker.Run(ctx)
	}()

	logger.Info("all services started")
	wg.Wait()
	pool.Close()
	logger.Info("shutdown complete")
}

func runTelegram(ctx context.Context, cfg config.Config, logger *slog.Logger, llmClient core.LLMClient, registry *plugin.Registry, tracer *trace.Store) error {
	if cfg.Telegram.Token == "" {
		logger.Error("telegram bot token is empty, skipping")
		return nil
	}
	if cfg.Telegram.MediaDir != "" {
		if err := os.MkdirAll(cfg.Telegram.MediaDir, 0o755); err != nil {
			return err
		}
	}

	adapter, err := telegramadapter.NewAdapter(cfg.Telegram.Token, cfg.Telegram.PollingTimeout)
	if err != nil {
		return err
	}
	if err := adapter.SetCommands(ctx, []telegramadapter.Command{
		{Command: "start", Description: "Приветствие"},
		{Command: "help", Description: "Что умею и доступные возможности"},
	}); err != nil {
		logger.Warn("failed to set telegram commands", "err", err)
	}

	agentService := agent.NewServiceWithRegistry(llmClient, registry).WithTracer(tracer)

	router := telegram.NewRouter()
	router.Use(telegram.DeduplicateUpdates(1000))
	router.Use(telegram.RequireAllowedChats(cfg.Telegram.AllowedChats))
	router.Use(telegram.LogUpdates())
	router.Use(telegram.LogDuration())
	router.Use(telegram.RateLimit(cfg.Telegram.RateLimit))
	router.HandleCommand("start", telegram.HandleStart)
	router.HandleCommand("help", telegram.HandleHelp)
	router.HandleDefault(telegram.HandleDefault)

	updates, err := adapter.Updates(ctx)
	if err != nil {
		return err
	}
	logger.Info("telegram bot started")

	workers := cfg.Telegram.Workers
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)

	for {
		select {
		case <-ctx.Done():
			return nil
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			sem <- struct{}{}
			go func(u core.Update) {
				defer func() { <-sem }()
				hctx, hcancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer hcancel()
				err := router.HandleUpdate(hctx, &telegram.Context{
					Bot:       adapter,
					Update:    u,
					Logger:    logger,
					Agent:     agentService,
					Fetcher:   adapter,
					Allowed:   cfg.Telegram.AllowedChats,
					RequestID: uuid.NewString(),
					MediaDir:  cfg.Telegram.MediaDir,
					Registry:  registry,
				})
				if err != nil {
					logger.Error("telegram update error", "err", err)
				}
			}(update)
		}
	}
}

func runMCP(ctx context.Context, logger *slog.Logger, registry *plugin.Registry) error {
	mcpSrv := mcpserver.Build(registry, "simpleai", "1.0.0")
	sseSrv := server.NewSSEServer(mcpSrv, server.WithBaseURL("http://localhost:8080"))

	errCh := make(chan error, 1)
	go func() {
		logger.Info("MCP SSE server started", "addr", ":8080")
		errCh <- sseSrv.Start(":8080")
	}()

	select {
	case <-ctx.Done():
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		return sseSrv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

func runWorker(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool, llmClient core.LLM) {
	store := mail.NewStore(pool)
	gmailProvider := mail.NewGmailProvider(&http.Client{Timeout: 15 * time.Second})
	imapProvider := mail.NewIMAPProvider()
	tg := notify.NewTelegram(cfg.Mail.Telegram.Token, cfg.Mail.Telegram.ChatID)
	runner := mail.NewRunner(store, gmailProvider, imapProvider, tg, llmClient, logger)
	accounts := mapAccounts(cfg.Mail.Accounts)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	logger.Info("mail worker started")
	for {
		if err := runner.RunOnce(ctx, accounts); err != nil {
			logger.Error("mail worker run failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func buildRegistry(llmClient core.LLMClient, logger *slog.Logger, pool *pgxpool.Pool) *plugin.Registry {
	registry := plugin.NewRegistry()

	retriever := rag.NewRetriever(pool)
	ragSkill := skills.NewRAGSearchSkill(retriever, llmClient)
	if err := registry.Register(ragSkill); err != nil {
		logger.Error("failed to register rag_search skill", "err", err)
	}

	budgetStore := budget.NewStore(pool)
	budgetSkill := skills.NewBudgetSkill(budgetStore)
	if err := registry.Register(budgetSkill); err != nil {
		logger.Error("failed to register budget skill", "err", err)
	}

	logger.Info("registry ready", "skills", len(registry.List()))
	return registry
}

func mapAccounts(accounts []config.MailAccount) []mail.Account {
	out := make([]mail.Account, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, mail.Account{
			Provider:     a.Provider,
			Email:        a.Email,
			AccessToken:  a.AccessToken,
			RefreshToken: a.RefreshToken,
			ClientID:     a.ClientID,
			ClientSecret: a.ClientSecret,
			Host:         a.Host,
			Port:         a.Port,
			UseTLS:       a.UseTLS,
			Labels:       a.Labels,
			Folders:      a.Folders,
		})
	}
	return out
}
