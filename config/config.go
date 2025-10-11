package config

import (
	"fmt"
	"github.com/joho/godotenv"
	"os"
)

type Config struct {
	APIKey    string
	SysPrompt string
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
	return cfg, nil
}
