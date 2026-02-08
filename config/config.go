package config

import (
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	"os"
)

type Config struct {
	APIKey    string    `env:"API_KEY"`
	SysPrompt string    `env:"SYS_PROMPT"`
	DB        DBConfig  `env:",inline"`
	RAG       RAGConfig `env:",inline"`
	Mail      MailConfig
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

func LoadConfig() (Config, error) {
	_ = godotenv.Load(".env")

	cfg := Config{}
	cfg.APIKey = os.Getenv("API_KEY")
	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("API key not found in .env")
	}
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
	return cfg, nil
}

func LoadDBConfig() (DBConfig, error) {
	_ = godotenv.Load(".env")
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

func getenvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
