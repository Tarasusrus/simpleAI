// Package telegram реализует прикладной слой Telegram-бота: роутер, middleware, контекст и обработку вложений.
// Пакет также сохраняет raw-ingest payload для дальнейшей обработки; точка входа Router.HandleUpdate.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"simpleAI/internal/core"
)

// IngestPayload сохраняет вход Telegram как сырой артефакт для дальнейшей обработки.
type IngestPayload struct {
	ReceivedAt  time.Time         `json:"received_at"`
	ChatID      int64             `json:"chat_id"`
	UserID      int64             `json:"user_id"`
	UserName    string            `json:"user_name"`
	DisplayName string            `json:"display_name"`
	Text        string            `json:"text"`
	Attachments []core.Attachment `json:"attachments"`
	Paths       []string          `json:"paths"`
}

func SaveIngestPayload(ctx context.Context, dir string, incoming Incoming, paths []string) (string, error) {
	_ = ctx
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("media dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	payload := IngestPayload{
		ReceivedAt:  time.Now().UTC(),
		ChatID:      incoming.ChatID,
		UserID:      incoming.UserID,
		UserName:    incoming.UserName,
		DisplayName: incoming.DisplayName,
		Text:        incoming.Text,
		Attachments: incoming.Attachments,
		Paths:       paths,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("ingest_%s.json", time.Now().UTC().Format("20060102T150405.000000000"))
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", err
	}
	return target, nil
}
