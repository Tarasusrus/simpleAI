package config

import (
	"os"
	"strings"
	"time"
)

// ReceiptConfig — vision-вход для фото чеков (ADR-010). Отдельные ключи,
// чтобы не трогать настройки основного LLM: пока они не заданы, фича
// выключена и бот отвечает «обработка фото не настроена».
type ReceiptConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	MaxBytes   int64
	Timeout    time.Duration
	CatalogTTL time.Duration
}

// Enabled — заданы адрес и ключ прокси. Модель имеет значение по умолчанию.
func (c ReceiptConfig) Enabled() bool {
	return c.BaseURL != "" && c.APIKey != ""
}

// defaultReceiptMaxMB: Bot API не отдаёт боту файлы больше 20 МБ, а сжатое
// Telegram фото чека весит сотни килобайт; 10 МБ оставляют запас под
// несжатый файл-фото.
const defaultReceiptMaxMB = 10

func loadReceiptConfig() ReceiptConfig {
	return ReceiptConfig{
		BaseURL:  strings.TrimSpace(os.Getenv("VISION_LLM_BASE_URL")),
		APIKey:   strings.TrimSpace(os.Getenv("VISION_LLM_API_KEY")),
		Model:    strings.TrimSpace(getenvOrDefault("VISION_MODEL", "vision")),
		MaxBytes: int64(positiveInt(os.Getenv("RECEIPT_MAX_MB"), defaultReceiptMaxMB)) << 20,
		// Бесплатные модели отвечают за 10–20 с, а цепочка прокси может
		// пройти несколько провайдеров подряд.
		Timeout:    parseDurationSeconds(os.Getenv("VISION_TIMEOUT_SECONDS"), 90),
		CatalogTTL: parseDurationSeconds(os.Getenv("VISION_CATALOG_TTL_SECONDS"), 300),
	}
}

func positiveInt(raw string, def int) int {
	if v := parseInt(raw, def); v > 0 {
		return v
	}
	return def
}
