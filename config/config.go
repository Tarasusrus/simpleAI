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
	SysPrompt string `env:"SYS_PROMPT"`
	DB        DBConfig
	RAG       RAGConfig
	Mail      MailConfig
	Telegram  TelegramBotConfig
	// PlanChatIDs — PLAN_CHAT_IDS, область чатов плана; пусто — берётся Telegram.AllowedChats (см. PlanScopeChats).
	PlanChatIDs []int64
	LLM         LLMConfig
	Receipt     ReceiptConfig
	Classifier  CategoryClassifierConfig
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
	// Primary provider
	Provider          string
	APIKey            string
	BaseURL           string
	ChatModel         string
	Timeout           time.Duration
	RetryCount        int
	RetryBase         time.Duration
	AdvisorLLMTimeout time.Duration // independent timeout for advisor skill LLM calls

	// Fallback OpenAI-compatible provider (e.g. Gemini)
	FallbackAPIKey    string
	FallbackBaseURL   string
	FallbackChatModel string

	// Ollama (local dev)
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
	AllowAll       bool // ALLOW_ALL=true: подтверждение, что пустой список чатов (= бот открыт всем) задуман; при непустом списке ничего не меняет
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
	cfg.SysPrompt = os.Getenv("SYS_PROMPT")
	if cfg.SysPrompt == "" {
		return Config{}, fmt.Errorf("SysPrompt not found in .env")
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
	tgCfg, err := loadTelegramBotConfig()
	if err != nil {
		return Config{}, err
	}
	cfg.Telegram = tgCfg
	planChats, err := parseChatIDsStrict(os.Getenv("PLAN_CHAT_IDS"))
	if err != nil {
		return Config{}, fmt.Errorf("PLAN_CHAT_IDS: %w", err)
	}
	cfg.PlanChatIDs = planChats
	cfg.Receipt = loadReceiptConfig()
	cfg.Classifier = loadCategoryClassifierConfig()
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

func loadTelegramBotConfig() (TelegramBotConfig, error) {
	allowed, err := parseChatIDsStrict(os.Getenv("TELEGRAM_ALLOWED_CHATS"))
	if err != nil {
		return TelegramBotConfig{}, fmt.Errorf("TELEGRAM_ALLOWED_CHATS: %w", err)
	}
	return TelegramBotConfig{
		Token:          os.Getenv("TELEGRAM_BOT_TOKEN"),
		AllowedChats:   allowed,
		AllowAll:       parseBool(os.Getenv("ALLOW_ALL")),
		PollingTimeout: 30 * time.Second,
		Workers:        parseInt(os.Getenv("TELEGRAM_WORKERS"), 4),
		RateLimit:      parseDurationMs(os.Getenv("TELEGRAM_RATE_LIMIT_MS")),
		MediaDir:       getenvOrDefault("TELEGRAM_MEDIA_DIR", "data/telegram"),
	}, nil
}

func loadLLMConfig() LLMConfig {
	return LLMConfig{
		Provider:          getenvOrDefault("LLM_PROVIDER", "openai"),
		APIKey:            os.Getenv("DEEPSEEK_API_KEY"),
		BaseURL:           getenvOrDefault("LLM_BASE_URL", "https://api.deepseek.com/v1"),
		ChatModel:         getenvOrDefault("LLM_CHAT_MODEL", "deepseek-chat"),
		Timeout:           parseDurationSeconds(os.Getenv("LLM_TIMEOUT_SECONDS"), 30),
		RetryCount:        parseInt(os.Getenv("LLM_RETRY_COUNT"), 0),
		RetryBase:         parseDurationMs(os.Getenv("LLM_RETRY_BASE_MS")),
		AdvisorLLMTimeout: parseDurationSeconds(os.Getenv("ADVISOR_LLM_TIMEOUT_SECONDS"), 45),

		FallbackAPIKey:    os.Getenv("GEMINI_API_KEY"),
		FallbackBaseURL:   getenvOrDefault("LLM_FALLBACK_BASE_URL", "https://generativelanguage.googleapis.com/v1beta/openai/"),
		FallbackChatModel: getenvOrDefault("LLM_FALLBACK_MODEL", "gemini-2.0-flash"),

		OllamaBaseURL:       getenvOrDefault("OLLAMA_BASE_URL", "http://localhost:11434"),
		OllamaModel:         getenvOrDefault("OLLAMA_MODEL", "qwen2.5:7b-instruct-q4_K_M"),
		OllamaEmbedModel:    strings.TrimSpace(os.Getenv("OLLAMA_EMBED_MODEL")),
		OllamaFallbackModel: strings.TrimSpace(os.Getenv("OLLAMA_FALLBACK_MODEL")),
	}
}

// parseChatIDsStrict не выбрасывает молча неразобранные записи: опечатка вроде
// «12345;6789» иначе давала бы пустой список, а пустой список в RequireAllowedChats
// открывает бота всем (и тихо сдвигает область плана на другой список).
func parseChatIDsStrict(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("запись %q не число (разделитель — запятая)", part)
		}
		out = append(out, id)
	}
	return out, nil
}

func parseBool(raw string) bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(raw))
	return v
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

// PlanScopeChats — область чатов плана: PLAN_CHAT_IDS, иначе TELEGRAM_ALLOWED_CHATS.
func (c Config) PlanScopeChats() []int64 {
	if len(c.PlanChatIDs) > 0 {
		return c.PlanChatIDs
	}
	return c.Telegram.AllowedChats
}
