// Package botclient — прямой доступ к ядру бота (agent.Service) в обход
// Telegram-транспорта. Инструмент для ручного/тестового прогона фич на реплике.
//
// Инвариант безопасности: по умолчанию read-only — используется connection
// string реплики с ролью botclient_ro, запись отклоняется на уровне БД.
// Запись возможна только явным --allow-writes + отдельным write-URL.
package botclient

import (
	"context"
	"errors"

	"simpleAI/internal/agent"
)

// Options управляет выбором подключения к БД (guard read-only).
type Options struct {
	AllowWrites bool
	ReadOnlyURL string // BOTCLIENT_DATABASE_URL (роль botclient_ro)
	WriteURL    string // BOTCLIENT_DATABASE_URL_RW (write-роль), нужен только при AllowWrites
}

// ErrNoReadOnlyURL и ErrNoWriteURL — явные ошибки конфигурации подключения.
var (
	ErrNoReadOnlyURL = errors.New("botclient: не задан BOTCLIENT_DATABASE_URL (read-only реплика)")
	ErrNoWriteURL    = errors.New("botclient: --allow-writes требует BOTCLIENT_DATABASE_URL_RW")
)

// ResolveDatabaseURL выбирает connection string, форсируя read-only по умолчанию.
// Это НЕ косметика: default всегда возвращает read-only URL, а запись требует
// и явного флага, и отдельного write-URL — иначе ошибка, а не тихий фолбэк.
func ResolveDatabaseURL(o Options) (string, error) {
	if !o.AllowWrites {
		if o.ReadOnlyURL == "" {
			return "", ErrNoReadOnlyURL
		}
		return o.ReadOnlyURL, nil
	}
	if o.WriteURL == "" {
		return "", ErrNoWriteURL
	}
	return o.WriteURL, nil
}

// Ask прогоняет один запрос через собранный agent.Service с привязкой chatID.
func Ask(ctx context.Context, svc *agent.Service, prompt string, chatID int64) (string, error) {
	return svc.AskWithMeta(ctx, prompt, &chatID)
}
