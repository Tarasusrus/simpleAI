package main

import (
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/config"
	"simpleAI/internal/appwiring"
	"simpleAI/internal/botlimits"
	"simpleAI/internal/envelopepush"
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

// botLimits — план для бота: читатель, режим флагов. Пустой читатель — план недоступен, старый путь.
type botLimits struct {
	reader botlimits.Reader
	mode   safetospendskill.LimitsMode
}

// newBotLimits подключает план к скиллам бота, если он нужен (флаг или shadow) и собирается из окружения.
// Нет области чатов или плохой PLAN_TZ — бот работает по старому пути.
func newBotLimits(cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool, getenv func(string) string) botLimits {
	mode := botLimitsMode(getenv)
	if !mode.FromPlan && !mode.Shadow {
		return botLimits{mode: mode}
	}
	svc, _, chats, ok := newPlanService(cfg, logger, pool)
	if !ok {
		logger.Warn("bot limits: план недоступен, бот работает по старому пути")
		return botLimits{mode: mode}
	}
	logger.Info("bot limits wired", "from_plan", mode.FromPlan, "shadow", mode.Shadow && !mode.FromPlan, "chats", len(chats))
	return botLimits{reader: botlimits.NewPlan(svc, botlimits.ScopeOfChats(chats)), mode: mode}
}

// options — опция реестра скиллов (пусто, если плана нет).
func (b botLimits) options() []appwiring.RegistryOption {
	if b.reader == nil {
		return nil
	}
	return []appwiring.RegistryOption{appwiring.WithBotLimits(b.reader, b.mode)}
}

// pushSource переключает утренний пуш на план при BOT_LIMITS_FROM_PLAN; иначе источник остаётся прежним.
func (b botLimits) pushSource(src *envelopepush.Source) *envelopepush.Source {
	if b.reader != nil && b.mode.FromPlan {
		return src.WithPlan(b.reader)
	}
	return src
}
