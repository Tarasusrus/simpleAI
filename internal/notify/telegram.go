// Package notify содержит код пакета notify и его задачи.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Telegram struct {
	token  string
	chatID string
	client *http.Client
}

func NewTelegram(token, chatID string) *Telegram {
	return &Telegram{
		token:  strings.TrimSpace(token),
		chatID: strings.TrimSpace(chatID),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *Telegram) SendToChatID(ctx context.Context, chatID int64, text string) error {
	if t.token == "" {
		return fmt.Errorf("telegram token is not configured")
	}
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			_ = err
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram send failed: status=%d", resp.StatusCode)
	}
	return nil
}

func (t *Telegram) Send(ctx context.Context, text string) error {
	if t.token == "" || t.chatID == "" {
		return fmt.Errorf("telegram token/chat_id is not configured")
	}
	payload := map[string]string{
		"chat_id": t.chatID,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			_ = err
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram send failed: status=%d", resp.StatusCode)
	}
	return nil
}
