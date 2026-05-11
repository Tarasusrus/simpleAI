// Package telegram содержит код пакета telegram и его задачи.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
)

type AgentService interface {
	Ask(ctx context.Context, input string) (string, error)
	AskWithMeta(ctx context.Context, input string, chatID *int64) (string, error)
}

// TypingSender — опциональный интерфейс для отправки индикатора набора.
type TypingSender interface {
	SendTyping(ctx context.Context, chatID int64) error
}

type Context struct {
	Bot       core.Bot
	Update    core.Update
	Logger    *slog.Logger
	Agent     AgentService
	Fetcher   AttachmentFetcher
	Allowed   []int64
	RequestID string
	MediaDir  string
	Registry  *plugin.Registry
}

func (c *Context) ChatID() (int64, error) {
	if c.Update.ChatID == 0 {
		return 0, errors.New("no message in update")
	}
	return c.Update.ChatID, nil
}

func (c *Context) Reply(text string) error {
	chatID, err := c.ChatID()
	if err != nil {
		return err
	}
	return sendWithRetry(c, chatID, text)
}

// ReplyWithButtons отправляет новое сообщение с inline-кнопками.
// Если Bot не реализует ButtonSender — отправляет текст без кнопок.
func (c *Context) ReplyWithButtons(ctx context.Context, text string, rows [][]core.Button) error {
	chatID, err := c.ChatID()
	if err != nil {
		return err
	}
	if bs, ok := c.Bot.(core.ButtonSender); ok {
		return bs.SendWithButtons(ctx, chatID, text, rows)
	}
	return sendWithRetry(c, chatID, text)
}

// EditWithButtons редактирует исходное сообщение callback-а и обновляет кнопки.
// Если Bot не реализует ButtonSender — отправляет новое сообщение.
func (c *Context) EditWithButtons(ctx context.Context, text string, rows [][]core.Button) error {
	chatID, err := c.ChatID()
	if err != nil {
		return err
	}
	if bs, ok := c.Bot.(core.ButtonSender); ok {
		if c.Update.IsCallback && c.Update.MessageID > 0 {
			if err := bs.AnswerCallback(ctx, c.Update.CallbackID); err != nil {
				slog.Default().WarnContext(ctx, "answer callback failed", "err", err, "callback_id", c.Update.CallbackID)
			}
			return bs.EditWithButtons(ctx, chatID, c.Update.MessageID, text, rows)
		}
		return bs.SendWithButtons(ctx, chatID, text, rows)
	}
	return sendWithRetry(c, chatID, text)
}

func (c *Context) Replyf(format string, args ...any) error {
	return c.Reply(fmt.Sprintf(format, args...))
}

func (c *Context) Text() string {
	return strings.TrimSpace(c.Update.Text)
}

// StartTyping отправляет typing action и обновляет его каждые 4 секунды.
// Возвращает функцию stop — вызови её когда ответ готов.
// Если Bot не реализует TypingSender — ничего не делает.
func (c *Context) StartTyping(ctx context.Context) (stop func()) {
	ts, ok := c.Bot.(TypingSender)
	if !ok {
		return func() {}
	}
	chatID, err := c.ChatID()
	if err != nil {
		return func() {}
	}

	done := make(chan struct{})
	if err := ts.SendTyping(ctx, chatID); err != nil {
		slog.Default().DebugContext(ctx, "typing action failed", "err", err)
	}

	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := ts.SendTyping(ctx, chatID); err != nil {
					slog.Default().DebugContext(ctx, "typing action failed", "err", err)
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

func sendWithRetry(c *Context, chatID int64, text string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		if c.Bot == nil {
			return errors.New("bot adapter is nil")
		}
		var err error
		if c.Update.MessageID > 0 {
			err = c.Bot.Reply(context.Background(), chatID, c.Update.MessageID, text)
		} else {
			err = c.Bot.Send(context.Background(), chatID, text)
		}
		if err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}
