package web

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
)

// Config — настройки веб-слоя. По умолчанию веб выключен и слушает только loopback.
type Config struct {
	Enabled          bool
	Addr             string
	Token            string
	AllowNonLoopback bool
	// Hosts — допустимые имена в заголовке Host (кроме loopback), например имя Tailscale.
	Hosts []string
}

const defaultAddr = "127.0.0.1:8090"

// ConfigFromEnv читает PLAN_WEB_ENABLED, PLAN_WEB_ADDR, PLAN_WEB_TOKEN,
// PLAN_WEB_ALLOW_NONLOOPBACK, PLAN_WEB_HOSTS (список через запятую).
func ConfigFromEnv() Config {
	cfg := Config{
		Enabled:          strings.EqualFold(strings.TrimSpace(os.Getenv("PLAN_WEB_ENABLED")), "true"),
		Addr:             strings.TrimSpace(os.Getenv("PLAN_WEB_ADDR")),
		Token:            strings.TrimSpace(os.Getenv("PLAN_WEB_TOKEN")),
		AllowNonLoopback: strings.EqualFold(strings.TrimSpace(os.Getenv("PLAN_WEB_ALLOW_NONLOOPBACK")), "true"),
	}
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	for _, h := range strings.Split(os.Getenv("PLAN_WEB_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			cfg.Hosts = append(cfg.Hosts, strings.ToLower(h))
		}
	}
	return cfg
}

var (
	base64urlRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	hexRe       = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// ValidateToken: ≥ 43 символов base64url (32 байта энтропии); строка из одних
// шестнадцатеричных цифр — только от 64 символов (иначе энтропия ниже). Значение
// токена в тексте ошибки не показывается.
func ValidateToken(tok string) error {
	switch {
	case tok == "":
		return fmt.Errorf("токен не задан")
	case !base64urlRe.MatchString(tok):
		return fmt.Errorf("токен содержит недопустимые символы")
	case len(tok) < 43:
		return fmt.Errorf("токен короче 43 символов")
	case hexRe.MatchString(tok) && len(tok) < 64:
		return fmt.Errorf("hex-токен короче 64 символов")
	}
	return nil
}

// Validate проверяет конфиг для запуска. Не loopback без явного разрешения — отказ.
func (c Config) Validate() error {
	if err := ValidateToken(c.Token); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("адрес %q: %w", c.Addr, err)
	}
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !loopback && !c.AllowNonLoopback {
		return fmt.Errorf("адрес %q не loopback: нужен PLAN_WEB_ALLOW_NONLOOPBACK=true", c.Addr)
	}
	return nil
}
