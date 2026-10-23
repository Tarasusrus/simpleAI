package main

import (
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/config"
	"simpleAI/internal/appwiring"
	"simpleAI/internal/botlimits"
	safetospendskill "simpleAI/internal/skills/safetospend"
)

// botLimitsMode читает флаги бота (ADR-016 §8): BOT_LIMITS_FROM_PLAN — отвечать числами плана (по умолчанию
// выключен); BOT_LIMITS_SHADOW — при выключенном первом сравнивать с планом и писать расхождения в лог
// (по умолчанию включён). Независимо от PLAN_WEB_ENABLED: включение веба бота не переключает.
func botLimitsMode(getenv func(string) string) safetospendskill.LimitsMode {
	return safetospendskill.LimitsMode{
		FromPlan: envBool(getenv("BOT_LIMITS_FROM_PLAN"), false),
		Shadow:   envBool(getenv("BOT_LIMITS_SHADOW"), true),
	}
}

func envBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// botLimitsOptions подключает план к скиллам бота, если он нужен (флаг или shadow) и собирается из окружения.
// Нет области чатов или плохой PLAN_TZ — бот работает по старому пути.
func botLimitsOptions(cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool, getenv func(string) string) []appwiring.RegistryOption {
	mode := botLimitsMode(getenv)
	if !mode.FromPlan && !mode.Shadow {
		return nil
	}
	svc, _, chats, ok := newPlanService(cfg, logger, pool)
	if !ok {
		logger.Warn("bot limits: план недоступен, бот работает по старому пути")
		return nil
	}
	logger.Info("bot limits wired", "from_plan", mode.FromPlan, "shadow", mode.Shadow && !mode.FromPlan, "chats", len(chats))
	return []appwiring.RegistryOption{appwiring.WithBotLimits(botlimits.NewPlan(svc, botlimits.ScopeOfChats(chats)), mode)}
}
