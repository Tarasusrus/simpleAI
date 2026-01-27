package config

import (
	"fmt"
	"github.com/joho/godotenv"
	"os"
)

type Config struct {
	APIKey    string   `env:"API_KEY"`
	SysPrompt string   `env:"SYS_PROMPT"`
	DB        DBConfig `env:",inline"`
	RAG       RAGConfig `env:",inline"`
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
	return cfg, nil
}

func getenvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
