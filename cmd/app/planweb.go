package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/config"
	"simpleAI/internal/budget"
	"simpleAI/internal/plan"
	"simpleAI/internal/planrepo"
	"simpleAI/internal/web"
)

const defaultPlanTZ = "Asia/Bangkok"

// runPlanWeb поднимает экран плана окна. Выключен по умолчанию (PLAN_WEB_ENABLED);
// при слабом токене или неверном адресе не стартует (fail-closed) и пишет причину
// без значения токена. Падение веба бота не трогает.
func runPlanWeb(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) {
	wc := web.ConfigFromEnv()
	if !wc.Enabled {
		logger.Info("plan web disabled (PLAN_WEB_ENABLED != true)")
		return
	}
	tz := strings.TrimSpace(os.Getenv("PLAN_TZ"))
	if tz == "" {
		tz = defaultPlanTZ
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		logger.Error("plan web not started: bad PLAN_TZ", "tz", tz, "err", err)
		return
	}
	chats := config.ParseChatIDs(os.Getenv("PLAN_CHAT_IDS"))
	if len(chats) == 0 {
		chats = cfg.Telegram.AllowedChats
	}
	if len(chats) == 0 {
		logger.Error("plan web not started: no chats (PLAN_CHAT_IDS / TELEGRAM_ALLOWED_CHATS)")
		return
	}

	store := budget.NewStore(pool)
	reader := planrepo.NewReader(store, logger).WithLimits(store).WithDismissed(store)
	svc := plan.NewService(reader, planrepo.NewWriter(store), func() plan.Date { return plan.DateOf(time.Now(), loc) }, plan.Scope{ChatIDs: chats},
		plan.WithFactWriter(planrepo.NewFactWriter(store))).
		WithLimitWriter(planrepo.NewLimitWriter(store)).
		WithCategoryHider(planrepo.NewCategoryHider(store))
	srv, err := web.New(wc, svc, logger)
	if err != nil {
		logger.Warn("plan web not started: invalid config", "reason", err.Error())
		return
	}
	srv.WithLimits(svc, planrepo.NewLimitSuggester(store)).WithCategoryHider(svc)
	if err := srv.Run(ctx); err != nil {
		logger.Error("plan web stopped", "err", err)
	}
}
