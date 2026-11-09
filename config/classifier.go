package config

import (
	"os"
	"strings"
	"time"
)

// Режимы классификатора категорий (ADR-012). Выключен по умолчанию.
const (
	ClassifierOff    = "off"
	ClassifierShadow = "shadow" // вызывается и логируется, на запись не влияет
	ClassifierOn     = "on"
)

// CategoryClassifierConfig — классификатор категорий на маршруте standard прокси LiteLLM.
// Отдельные ключи, чтобы не трогать настройки основного LLM (LLM_*) и vision (VISION_*): пока адрес
// и ключ не заданы или режим off, классификатор выключен.
type CategoryClassifierConfig struct {
	Mode             string
	BaseURL          string
	APIKey           string
	Model            string
	Timeout          time.Duration
	MaxCallsPerDay   int
	MaxCreatesPerDay int
}

// Enabled — режим не off и заданы адрес и ключ прокси.
func (c CategoryClassifierConfig) Enabled() bool {
	return c.Mode != ClassifierOff && c.BaseURL != "" && c.APIKey != ""
}

func loadCategoryClassifierConfig() CategoryClassifierConfig {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CATEGORY_CLASSIFIER_MODE")))
	if mode != ClassifierShadow && mode != ClassifierOn {
		mode = ClassifierOff // неизвестное значение = выключено, а не «включить по ошибке»
	}
	return CategoryClassifierConfig{
		Mode:    mode,
		BaseURL: strings.TrimSpace(os.Getenv("CATEGORY_LLM_BASE_URL")),
		APIKey:  strings.TrimSpace(os.Getenv("CATEGORY_LLM_API_KEY")),
		Model:   strings.TrimSpace(getenvOrDefault("CATEGORY_LLM_MODEL", "standard")),
		// Бесплатные модели отвечают за 3-5 с (замер 2026-10-21, маршрут standard), 5 с давало бы частые таймауты; при сбое запись идёт в «Прочее».
		Timeout:          parseDurationSeconds(os.Getenv("CATEGORY_LLM_TIMEOUT_SECONDS"), 10),
		MaxCallsPerDay:   positiveInt(os.Getenv("CATEGORY_LLM_MAX_CALLS_PER_DAY"), 200),
		MaxCreatesPerDay: positiveInt(os.Getenv("CATEGORY_LLM_MAX_CREATES_PER_DAY"), 2),
	}
}
