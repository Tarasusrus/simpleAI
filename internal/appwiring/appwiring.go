// Package appwiring собирает переиспользуемую обвязку приложения — единый
// реестр skills и agent.Service — чтобы её могли разделять разные точки входа
// (cmd/app, cmd/botclient) без дублирования конструирования.
//
// Логика перенесена из cmd/app без смены поведения (задача simpleAI-fm3g).
package appwiring

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/internal/agent"
	"simpleAI/internal/budget"
	"simpleAI/internal/core"
	"simpleAI/internal/observability"
	"simpleAI/internal/plugin"
	"simpleAI/internal/rag"
	"simpleAI/internal/skills"
	advisorskill "simpleAI/internal/skills/advisor"
	budgetskill "simpleAI/internal/skills/budget"
	safetospendskill "simpleAI/internal/skills/safetospend"
	"simpleAI/internal/trace"
)

// BuildRegistry собирает единый реестр skills (rag_search, budget, advisor)
// поверх пула БД. Возвращает реестр и BudgetSkill (нужен отдельно для
// Telegram callback-хендлера).
func BuildRegistry(llmClient core.LLMClient, logger *slog.Logger, pool *pgxpool.Pool, advisorLLMTimeout time.Duration) (*plugin.Registry, *budgetskill.BudgetSkill) {
	if logger == nil {
		logger = slog.Default()
	}
	registry := plugin.NewRegistry()

	retriever := rag.NewRetriever(pool)
	ragSkill := skills.NewRAGSearchSkill(retriever, llmClient)
	if err := registry.Register(ragSkill); err != nil {
		logger.Error("failed to register rag_search skill", "err", err)
	}

	budgetStore := budget.NewStore(pool)
	bs := budgetskill.NewBudgetSkill(budgetStore)
	if err := registry.Register(bs); err != nil {
		logger.Error("failed to register budget skill", "err", err)
	}

	advisorSkill := advisorskill.NewAdvisorSkill(budgetStore, llmClient, logger).
		WithLLMTimeout(advisorLLMTimeout)
	if err := registry.Register(advisorSkill); err != nil {
		logger.Error("failed to register advisor skill", "err", err)
	}

	safeToSpendSkill := safetospendskill.NewSafeToSpendSkill(budgetStore, llmClient, logger).
		WithLLMTimeout(advisorLLMTimeout)
	if err := registry.Register(safeToSpendSkill); err != nil {
		logger.Error("failed to register safe_to_spend skill", "err", err)
	}

	logger.Info("registry ready", "skills", len(registry.List()))
	return registry, bs
}

// BuildAgentService собирает agent.Service с реестром и опциональными
// трейсерами. Перенесено из cmd/app runTelegram без смены поведения.
func BuildAgentService(llmClient core.LLM, registry *plugin.Registry, tracer *trace.Store, obsTracer *observability.Tracer, logger *slog.Logger, chatModel string) *agent.Service {
	if logger == nil {
		logger = slog.Default()
	}
	return agent.NewServiceWithRegistry(llmClient, registry).
		WithTracer(tracer).
		WithObservability(obsTracer).
		WithLogger(logger).
		WithLLMModel(chatModel)
}
