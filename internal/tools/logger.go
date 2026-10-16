// Package tools содержит код пакета tools и его задачи.
package tools

import (
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
)

func NewLogger() *slog.Logger {
	level := parseLogLevel(os.Getenv("LOG_LEVEL"))
	format := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT")))
	return newLogger(os.Stdout, os.Stderr, level, format)
}

// newLogger — NewLogger без привязки к os.Stdout/Stderr, чтобы вывод можно было
// проверить. Токены бота маскируются в обоих путях: через slog и через
// стандартный log (его использует tgbotapi для ошибок getUpdates).
func newLogger(out, stdLogOut io.Writer, level slog.Level, format string) *slog.Logger {
	log.SetOutput(redactWriter{w: stdLogOut})

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(redactWriter{w: out}, opts)
	} else {
		handler = slog.NewTextHandler(redactWriter{w: out}, opts)
	}
	return slog.New(handler)
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
