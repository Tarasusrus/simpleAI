package appwiring

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"simpleAI/config"
	"simpleAI/internal/adapters/llm/litellm"
	openaiadapter "simpleAI/internal/adapters/llm/openai"
	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
	"simpleAI/internal/receipt"
	"simpleAI/internal/receipt/budgetadapter"
	"simpleAI/internal/receipt/memdraft"
	"simpleAI/internal/telegram"
)

// receiptDraftTTL — сколько черновик чека ждёт нажатия кнопки.
const receiptDraftTTL = 30 * time.Minute

// Receipt — собранный сценарий фото чека. Service и Catalog равны nil, если
// VISION_* не заданы: бот работает, на фото отвечает «не настроено».
type Receipt struct {
	Service *receipt.Service
	Catalog core.ModelCatalog
	Model   string
}

// Flow — сценарий для Telegram; nil, если не настроен. Отдельный метод, а не
// поле: *receipt.Service, равный nil, внутри интерфейса не был бы nil.
func (r Receipt) Flow() telegram.ReceiptService {
	if r.Service == nil {
		return nil
	}
	return r.Service
}

// BuildReceipt собирает сценарий фото чека (ADR-014). Ошибки не фатальны:
// без vision-прокси бот должен стартовать как раньше.
func BuildReceipt(ctx context.Context, cfg config.ReceiptConfig, registry *plugin.Registry,
	categories budgetadapter.CategorySource, seen receipt.SeenStore, logger *slog.Logger) Receipt {
	out := Receipt{Model: cfg.Model}
	if !cfg.Enabled() {
		logger.Info("receipt photos disabled: VISION_LLM_BASE_URL / VISION_LLM_API_KEY not set")
		return out
	}
	budgetSkill, ok := registry.Get("budget")
	if !ok {
		logger.Warn("receipt photos disabled: budget skill not registered")
		return out
	}
	vision, err := openaiadapter.NewVisionClient(cfg.BaseURL, cfg.APIKey, cfg.Timeout)
	if err != nil {
		logger.Warn("receipt photos disabled", "err", err)
		return out
	}
	catalog := litellm.NewCatalog(cfg.BaseURL, cfg.APIKey, nil, cfg.CatalogTTL)
	out.Catalog = catalog
	out.Service = receipt.NewService(receipt.Deps{
		Catalog:    catalog,
		Model:      cfg.Model,
		MaxBytes:   cfg.MaxBytes,
		Classifier: receipt.NewVisionClassifier(vision, cfg.Model),
		Parser:     receipt.NewVisionParser(vision, cfg.Model),
		Categories: budgetadapter.NewCategories(categories),
		Drafts:     memdraft.New(receiptDraftTTL),
		Writer:     budgetadapter.NewSkillWriter(budgetSkill),
		Seen:       seen,
	})
	// Прокси часто поднимается позже бота: лог не должен задерживать старт.
	go func() {
		lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		logVisionModels(lctx, catalog, cfg.Model, logger)
	}()
	return out
}

// logVisionModels пишет при старте, какие модели прокси принимают фото и
// PDF, и есть ли среди них модель для чеков.
func logVisionModels(ctx context.Context, catalog core.ModelCatalog, model string, logger *slog.Logger) {
	models, err := catalog.Models(ctx)
	if err != nil {
		logger.Warn("receipt photos: model catalog unavailable, will retry on demand", "err", err)
		return
	}
	var vision, pdf []string
	found := false
	for _, m := range models {
		if m.Capabilities.Vision {
			vision = append(vision, m.Name)
		}
		if m.Capabilities.PDFInput {
			pdf = append(pdf, m.Name)
		}
		if m.Name == model {
			found = true
		}
	}
	logger.Info("receipt photos: model catalog",
		"model", model, "model_in_catalog", found,
		"vision", strings.Join(vision, ","), "pdf", strings.Join(pdf, ","))
}
