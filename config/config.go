// Package config содержит код пакета config и его задачи.
package config

import (
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	APIKey    string    `env:"API_KEY"`
	SysPrompt string    `env:"SYS_PROMPT"`
	DB        DBConfig  `env:",inline"`
	RAG       RAGConfig `env:",inline"`
	Mail      MailConfig
	Telegram  TelegramBotConfig
	LLM       LLMConfig
}

type DBConfig struct {
	Host string `env:"POSTGRES_HOST" default:"localhost"`
	Port string `env:"POSTGRES_PORT" default:"5432"`
	Name string `env:"POSTGRES_DB" default:"simpleai"`
	User string `env:"POSTGRES_USER" default:"simpleai"`
	Pass string `env:"POSTGRES_PASSWORD" default:"simpleai"`
}

type RAGConfig struct {
	EmbeddingModel string `env:"EMBEDDING_MODEL" default:"text-embedding-3-small"`
}

type LLMConfig struct {
	Provider            string
	ChatModel           string
	Timeout             time.Duration
	RetryCount          int
	RetryBase           time.Duration
	OllamaBaseURL       string
	OllamaModel         string
	OllamaEmbedModel    string
	OllamaFallbackModel string
}

type MailConfig struct {
	Accounts []MailAccount
	Telegram TelegramConfig
}

type MailAccount struct {
	Provider     string   `json:"provider"`
	Email        string   `json:"email"`
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	UseTLS       *bool    `json:"use_tls"`
	Labels       []string `json:"labels"`
	Folders      []string `json:"folders"`
}

type TelegramConfig struct {
	Token  string `env:"TELEGRAM_BOT_TOKEN"`
	ChatID string `env:"TELEGRAM_CHAT_ID"`
}

type TelegramBotConfig struct {
	Token          string
	AllowedChats   []int64
	PollingTimeout time.Duration
	Workers        int
	RateLimit      time.Duration
	MediaDir       string
}

func LoadConfig() (Config, error) {
	if err := godotenv.Load(".env"); err != nil {
		_ = err
	}

	cfg := Config{}
	cfg.LLM = loadLLMConfig()
	cfg.APIKey = os.Getenv("API_KEY")
	cfg.SysPrompt = os.Getenv("SYS_PROMPT")
	if cfg.SysPrompt == "" {
		return Config{}, fmt.Errorf("SysPrompt not found in .env")
	}
	if strings.ToLower(strings.TrimSpace(cfg.LLM.Provider)) != "ollama" && cfg.APIKey == "" {
		return Config{}, fmt.Errorf("API key not found in .env")
	}
	cfg.DB = DBConfig{
		Host: getenvOrDefault("POSTGRES_HOST", "localhost"),
		Port: getenvOrDefault("POSTGRES_PORT", "5432"),
		Name: getenvOrDefault("POSTGRES_DB", "simpleai"),
		User: getenvOrDefault("POSTGRES_USER", "simpleai"),
		Pass: getenvOrDefault("POSTGRES_PASSWORD", "simpleai"),
	}
	cfg.RAG = RAGConfig{
		EmbeddingModel: getenvOrDefault("EMBEDDING_MODEL", "text-embedding-3-small"),
	}
	mailCfg, err := loadMailConfig()
	if err != nil {
		return Config{}, err
	}
	cfg.Mail = mailCfg
	cfg.Telegram = loadTelegramBotConfig()
	return cfg, nil
}

func LoadDBConfig() (DBConfig, error) {
	if err := godotenv.Load(".env"); err != nil {
		_ = err
	}
	return DBConfig{
		Host: getenvOrDefault("POSTGRES_HOST", "localhost"),
		Port: getenvOrDefault("POSTGRES_PORT", "5432"),
		Name: getenvOrDefault("POSTGRES_DB", "simpleai"),
		User: getenvOrDefault("POSTGRES_USER", "simpleai"),
		Pass: getenvOrDefault("POSTGRES_PASSWORD", "simpleai"),
	}, nil
}

func loadMailConfig() (MailConfig, error) {
	accountsJSON := os.Getenv("MAIL_ACCOUNTS_JSON")
	accounts := []MailAccount{}
	if accountsJSON != "" {
		if err := json.Unmarshal([]byte(accountsJSON), &accounts); err != nil {
			return MailConfig{}, fmt.Errorf("invalid MAIL_ACCOUNTS_JSON: %w", err)
		}
	}
	return MailConfig{
		Accounts: accounts,
		Telegram: TelegramConfig{
			Token:  os.Getenv("TELEGRAM_BOT_TOKEN"),
			ChatID: os.Getenv("TELEGRAM_CHAT_ID"),
		},
	}, nil
}

func loadTelegramBotConfig() TelegramBotConfig {
	return TelegramBotConfig{
		Token:          os.Getenv("TELEGRAM_BOT_TOKEN"),
		AllowedChats:   parseChatIDs(os.Getenv("TELEGRAM_ALLOWED_CHATS")),
		PollingTimeout: 30 * time.Second,
		Workers:        parseInt(os.Getenv("TELEGRAM_WORKERS"), 4),
		RateLimit:      parseDurationMs(os.Getenv("TELEGRAM_RATE_LIMIT_MS")),
		MediaDir:       getenvOrDefault("TELEGRAM_MEDIA_DIR", "data/telegram"),
	}
}

func loadLLMConfig() LLMConfig {
	return LLMConfig{
		Provider:            getenvOrDefault("LLM_PROVIDER", "openai"),
		ChatModel:           getenvOrDefault("LLM_CHAT_MODEL", "gpt-4.1-mini"),
		Timeout:             parseDurationSeconds(os.Getenv("LLM_TIMEOUT_SECONDS"), 30),
		RetryCount:          parseInt(os.Getenv("LLM_RETRY_COUNT"), 2),
		RetryBase:           parseDurationMs(os.Getenv("LLM_RETRY_BASE_MS")),
		OllamaBaseURL:       getenvOrDefault("OLLAMA_BASE_URL", "http://localhost:11434"),
		OllamaModel:         getenvOrDefault("OLLAMA_MODEL", "qwen2.5:7b-instruct-q4_K_M"),
		OllamaEmbedModel:    strings.TrimSpace(os.Getenv("OLLAMA_EMBED_MODEL")),
		OllamaFallbackModel: strings.TrimSpace(os.Getenv("OLLAMA_FALLBACK_MODEL")),
	}
}

func parseChatIDs(raw string) []int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func parseInt(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	if v, err := strconv.Atoi(raw); err == nil {
		return v
	}
	return def
}

func parseDurationSeconds(raw string, def int) time.Duration {
	seconds := parseInt(raw, def)
	if seconds <= 0 {
		seconds = def
	}
	return time.Duration(seconds) * time.Second
}

func parseDurationMs(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if v, err := strconv.Atoi(raw); err == nil {
		return time.Duration(v) * time.Millisecond
	}
	return 0
}

func getenvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
