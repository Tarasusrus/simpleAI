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

	botformat "simpleAI/internal/bot/format"
)

type Telegram struct {
	token  string
	chatID string
	client *http.Client
	// baseURL — корень API. Вынесен полем только ради тестов: отправку пуша
	// иначе не проверить, а именно она везёт раскладку конвертов.
	baseURL string
}

func NewTelegram(token, chatID string) *Telegram {
	return &Telegram{
		token:   strings.TrimSpace(token),
		chatID:  strings.TrimSpace(chatID),
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://api.telegram.org",
	}
}

// Утренний пуш идёт мимо адаптера Telegram, своим HTTP-клиентом. Разметка
// здесь обязана быть той же: раскладка конвертов в пуше — тот же текст с
// моноблоком, и без parse_mode он развалился бы ровно там, где в ответе на
// команду он цел.
func (t *Telegram) SendToChatID(ctx context.Context, chatID int64, text string) error {
	if t.token == "" {
		return fmt.Errorf("telegram token is not configured")
	}
	return t.deliver(ctx, chatID, text)
}

func (t *Telegram) Send(ctx context.Context, text string) error {
	if t.token == "" || t.chatID == "" {
		return fmt.Errorf("telegram token/chat_id is not configured")
	}
	return t.deliver(ctx, t.chatID, text)
}

// deliver: размеченные куски, при неудаче — тот же текст простым. Фоллбэк
// срабатывает, только если не ушло НИЧЕГО, иначе доставленные куски
// продублировались бы в чате.
func (t *Telegram) deliver(ctx context.Context, chatID any, text string) error {
	sent, err := t.postChunks(ctx, chatID, botformat.MessagesHTML(text), "HTML")
	if err == nil || sent > 0 {
		return err
	}
	_, err = t.postChunks(ctx, chatID, botformat.MessagesPlain(text), "")
	return err
}

func (t *Telegram) postChunks(ctx context.Context, chatID any, chunks []string, parseMode string) (int, error) {
	for i, chunk := range chunks {
		payload := map[string]any{"chat_id": chatID, "text": chunk}
		if parseMode != "" {
			payload["parse_mode"] = parseMode
		}
		if err := t.post(ctx, payload); err != nil {
			return i, err
		}
	}
	return len(chunks), nil
}

func (t *Telegram) post(ctx context.Context, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.token)
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
