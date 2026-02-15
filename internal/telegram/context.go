// Package telegram содержит код пакета telegram и его задачи.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"simpleAI/internal/core"
	"simpleAI/internal/plugin"
)

type AgentService interface {
	Ask(ctx context.Context, input string) (string, error)
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

func (c *Context) Replyf(format string, args ...any) error {
	return c.Reply(fmt.Sprintf(format, args...))
}

func (c *Context) Text() string {
	return strings.TrimSpace(c.Update.Text)
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
