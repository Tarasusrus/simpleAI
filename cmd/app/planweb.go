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
	"simpleAI/internal/categories"
	"simpleAI/internal/plan"
	"simpleAI/internal/planrepo"
	"simpleAI/internal/txedit"
	"simpleAI/internal/web"
)

const defaultPlanTZ = "Asia/Bangkok"

// newPlanService собирает plan.Service из окружения: часовой пояс (PLAN_TZ) и область чатов (PLAN_CHAT_IDS,
// иначе TELEGRAM_ALLOWED_CHATS). Один сервис для экрана и бота (ADR-016). ok=false — причина уже в логе.
func newPlanService(cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) (svc *plan.Service, store *budget.Store, chats []int64, ok bool) {
	tz := strings.TrimSpace(os.Getenv("PLAN_TZ"))
	if tz == "" {
		tz = defaultPlanTZ
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		logger.Error("plan not started: bad PLAN_TZ", "tz", tz, "err", err)
		return nil, nil, nil, false
	}
	chats = config.ParseChatIDs(os.Getenv("PLAN_CHAT_IDS"))
	if len(chats) == 0 {
		chats = cfg.Telegram.AllowedChats
	}
	if len(chats) == 0 {
		logger.Error("plan not started: no chats (PLAN_CHAT_IDS / TELEGRAM_ALLOWED_CHATS)")
		return nil, nil, nil, false
	}
	store = budget.NewStore(pool)
	reader := planrepo.NewReader(store, logger).WithLimits(store).WithDismissed(store)
	svc = plan.NewService(reader, planrepo.NewWriter(store), func() plan.Date { return plan.DateOf(time.Now(), loc) }, plan.Scope{ChatIDs: chats},
		plan.WithFactWriter(planrepo.NewFactWriter(store))).
		WithLimitWriter(planrepo.NewLimitWriter(store)).
		WithCategoryHider(planrepo.NewCategoryHider(store)).
		WithTxLister(planrepo.NewTxLister(store))
	return svc, store, chats, true
}

// runPlanWeb поднимает экран плана окна. Выключен по умолчанию (PLAN_WEB_ENABLED);
// при слабом токене или неверном адресе не стартует (fail-closed) и пишет причину
// без значения токена. Падение веба бота не трогает.
func runPlanWeb(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) {
	wc := web.ConfigFromEnv()
	if !wc.Enabled {
		logger.Info("plan web disabled (PLAN_WEB_ENABLED != true)")
		return
	}
	svc, store, _, ok := newPlanService(cfg, logger, pool)
	if !ok {
		return
	}
	srv, err := web.New(wc, svc, logger)
	if err != nil {
		logger.Warn("plan web not started: invalid config", "reason", err.Error())
		return
	}
	srv.WithLimits(svc, planrepo.NewLimitSuggester(store)).WithCategoryHider(svc).WithCategoryTransactions(svc).
		WithTransactions(txedit.NewService(store, categories.NewStore(pool)))
	if err := srv.Run(ctx); err != nil {
		logger.Error("plan web stopped", "err", err)
	}
}
