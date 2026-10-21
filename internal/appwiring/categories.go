package appwiring

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/config"
	"simpleAI/internal/adapters/llm/openai"
	"simpleAI/internal/categories"
	budgetskill "simpleAI/internal/skills/budget"
)

// AttachCategoryChooser подключает к навыку бюджета выбор категории (ADR-012): имя пользователя
// как есть, память описаний, классификатор на маршруте standard, деградация в «Прочее».
// Классификатор включается только режимом shadow/on И ключами прокси (config.CategoryClassifierConfig);
// иначе навык работает как раньше, а Chooser лишь уточняет легаси-источник разделения («Еда»,
// ADR-013), когда он появится в таблице разделения. Сбой создания клиента не мешает запуску:
// классификатор выключается, бот продолжает записывать траты.
func AttachCategoryChooser(bs *budgetskill.BudgetSkill, pool *pgxpool.Pool, cfg config.CategoryClassifierConfig, logger *slog.Logger) {
	if bs == nil || pool == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	store := categories.NewStore(pool)
	ch := &categories.Chooser{Dir: store, Memory: store, Mode: categories.ModeOff, Hints: categories.DefaultHints, Log: logger}
	if cfg.Enabled() {
		client, err := openai.NewClassifierClient(cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.Timeout)
		if err != nil {
			logger.Error("category classifier disabled", "err", err)
		} else {
			ch.Classifier = categories.NewGuardedClassifier(client, categories.GuardConfig{
				MaxCallsPerDay: cfg.MaxCallsPerDay, MaxCreatesPerDay: cfg.MaxCreatesPerDay, BreakerPause: 15 * time.Minute,
			})
			ch.Mode = cfg.Mode
		}
	}
	logger.Info("category chooser ready", "mode", ch.Mode)
	bs.WithCategoryChooser(ch, store).WithCategoryNames(func(ctx context.Context) ([]string, error) {
		cats, err := store.ListAssignable(ctx, "expense")
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(cats))
		for _, c := range cats {
			names = append(names, c.Name)
		}
		return names, nil
	})
}
