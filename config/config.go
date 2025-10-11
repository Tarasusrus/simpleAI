package config

import (
	"fmt"
	"github.com/joho/godotenv"
	"os"
)

type Config struct {
	APIKey string
}

func LoadConfig() (*Config, error) {
	_ = godotenv.Load(".env")

	config := Config{}
	config.APIKey = os.Getenv("API_KEY")
	if config.APIKey == "" {
		return nil, fmt.Errorf("API key not found in .env")
	}
	fmt.Println("Загрузка конфига ок")
	return &config, nil
}
