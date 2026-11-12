// Package main — единая точка входа приложения.
// Запускает Telegram-бот и MCP SSE-сервер в одном процессе (mail-worker отключён, ADR-024).
// Завершение по Ctrl+C / SIGTERM с graceful shutdown.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/server"

	"simpleAI/config"
	llmfactory "simpleAI/internal/adapters/llm"
	telegramadapter "simpleAI/internal/adapters/telegram"
	"simpleAI/internal/appwiring"
	"simpleAI/internal/budget"
	"simpleAI/internal/core"
	"simpleAI/internal/db"
	"simpleAI/internal/envelopepush"
	mcpserver "simpleAI/internal/mcp"
	"simpleAI/internal/notify"
	"simpleAI/internal/observability"
	"simpleAI/internal/plugin"
	"simpleAI/internal/rates"
	"simpleAI/internal/receipt/seenstore"
	budgetskill "simpleAI/internal/skills/budget"
	"simpleAI/internal/telegram"
	"simpleAI/internal/tools"
	"simpleAI/internal/trace"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("failed to load config: ", err)
	}
	chatAccessMW, err := chatAccess(cfg.Telegram.AllowedChats, cfg.Telegram.AllowAll)
	if err != nil {
		log.Fatal(err)
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

	// Лимиты из плана для бота и утреннего пуша (ADR-016/017); по умолчанию выключены.
	bl := newBotLimits(cfg, logger, pool, os.Getenv)

	// Единый реестр skills для всех компонентов.
	registry, budgetSkill := appwiring.BuildRegistry(llmClient, logger, pool, cfg.LLM.AdvisorLLMTimeout, bl.options()...)
	appwiring.AttachCategoryChooser(budgetSkill, pool, cfg.Classifier, logger)

	// Контекст с graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup

	tracer := trace.NewStore(pool)

	obsTracer := observability.NewTracer(
		os.Getenv("LANGFUSE_HOST"),
		os.Getenv("LANGFUSE_PUBLIC_KEY"),
		os.Getenv("LANGFUSE_SECRET_KEY"),
		logger,
	)
	defer obsTracer.Close()

	receiptApp := appwiring.BuildReceipt(ctx, cfg.Receipt, registry, budget.NewStore(pool), seenstore.New(pool), logger)

	// --- Telegram бот ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := runTelegram(ctx, cfg, chatAccessMW, logger, llmClient, registry, tracer, obsTracer, budgetSkill, budget.NewStore(pool), receiptApp); err != nil {
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

	// --- Экран плана окна (выключен по умолчанию: PLAN_WEB_ENABLED) ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		runPlanWeb(ctx, cfg, logger, pool)
	}()

	// Mail worker отключён навсегда: инструмент недоработан (ADR-024).

	// --- Reminder worker ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		budgetStore := budget.NewStore(pool)
		tg := notify.NewTelegram(cfg.Telegram.Token, "")
		digest := budgetskill.NewDigestProvider(budgetStore)
		worker := notify.NewReminderWorker(budgetStore, tg, digest, logger)
		// Утренние конверты: тело печатает тот же safe_to_spend, что отвечает на
		// «сколько в конвертах» — формат живёт в скилле, воркер его не дублирует.
		if skill, ok := registry.Get("safe_to_spend"); ok {
			worker = worker.WithEnvelopes(bl.pushSource(envelopepush.New(budgetStore, skill)))
		} else {
			logger.Warn("safe_to_spend skill not registered: morning envelope push disabled")
		}
		logger.Info("reminder worker started")
		worker.Run(ctx)
	}()

	// --- Recurring worker ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		budgetStore := budget.NewStore(pool)
		tg := notify.NewTelegram(cfg.Telegram.Token, "")
		worker := notify.NewRecurringWorker(budgetStore, tg, logger)
		logger.Info("recurring worker started")
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

func runTelegram(ctx context.Context, cfg config.Config, chatAccessMW telegram.Middleware, logger *slog.Logger, llmClient core.LLMClient, registry *plugin.Registry, tracer *trace.Store, obsTracer *observability.Tracer, budgetSkill telegram.BudgetCallbackSkill, envelopeReminders notify.EnvelopeReminderStore, receiptApp appwiring.Receipt) error {
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
	botCommands := []telegramadapter.Command{
		{Command: "start", Description: "Приветствие и краткая справка"},
		{Command: "help", Description: "Список доступных команд"},
		{Command: "budget", Description: "💰 Бюджет: расходы, доходы, цели, долги"},
		{Command: "recurring", Description: "🔄 Регулярные платежи"},
		{Command: "forecast", Description: "🔮 Прогноз трат на следующий месяц"},
		{Command: "reminders", Description: "🔔 Напоминания о внесении трат"},
		{Command: "models", Description: "🧾 Модели для фото чеков"},
	}
	if err := adapter.SetCommands(ctx, botCommands); err != nil {
		logger.Warn("failed to set telegram commands", "err", err)
	}

	agentService := appwiring.BuildAgentService(llmClient, registry, tracer, obsTracer, logger, cfg.LLM.ChatModel)

	router := telegram.NewRouter()
	router.Use(telegram.DeduplicateUpdates(1000))
	router.Use(chatAccessMW)
	router.Use(telegram.LogUpdates())
	router.Use(telegram.LogDuration())
	router.Use(telegram.RateLimit(cfg.Telegram.RateLimit))
	router.HandleCommand("start", telegram.HandleStart)
	router.HandleCommand("help", telegram.NewHandleHelp(buildHelpText(botCommands)))
	router.HandleCommand("budget", telegram.HandleBudget)
	router.HandleCommand("recurring", telegram.HandleRecurring)
	router.HandleCommand("forecast", telegram.HandleForecast)
	router.HandleCommand("reminders", telegram.HandleReminders)
	router.HandleCommand("models", telegram.NewHandleModels(receiptApp.Catalog, receiptApp.Model))
	router.HandleDefault(telegram.WithReceiptPhotos(receiptApp.Flow(), telegram.HandleDefault))
	router.HandleCallback(budgetskill.CallbackPrefix, telegram.NewHandleBudgetCallback(budgetSkill))
	router.HandleCallback(telegram.ReceiptCallbackPrefix, telegram.NewHandleReceiptCallback(receiptApp.Flow()))

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
				hctx, hcancel := context.WithTimeout(context.Background(), 150*time.Second)
				defer hcancel()
				err := router.HandleUpdate(hctx, &telegram.Context{
					Bot:       adapter,
					Update:    u,
					Logger:    logger,
					Agent:     agentService,
					Fetcher:   adapter,
					RequestID: uuid.NewString(),
					MediaDir:  cfg.Telegram.MediaDir,
					Registry:  registry,

					EnvelopeReminders: envelopeReminders,
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

	addr := tools.MCPAddr()
	errCh := make(chan error, 1)
	go func() {
		logger.Info("MCP SSE server started", "addr", addr)
		errCh <- sseSrv.Start(addr)
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

func buildHelpText(commands []telegramadapter.Command) string {
	var b strings.Builder
	b.WriteString("Что умею:\n")
	for _, cmd := range commands {
		if cmd.Command == "start" || cmd.Command == "help" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(cmd.Description)
		b.WriteString(" — /")
		b.WriteString(cmd.Command)
	}
	b.WriteString("\n\nИли просто пиши своими словами.")
	return b.String()
}
