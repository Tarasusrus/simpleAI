package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type AgentService interface {
	Ask(ctx context.Context, input string) (string, error)
}

type Context struct {
	Bot       *tgbotapi.BotAPI
	Update    tgbotapi.Update
	Logger    *slog.Logger
	Agent     AgentService
	Allowed   []int64
	RequestID string
	MediaDir  string
}

func (c *Context) ChatID() (int64, error) {
	if c.Update.Message == nil {
		return 0, errors.New("no message in update")
	}
	return c.Update.Message.Chat.ID, nil
}

func (c *Context) Reply(text string) error {
	chatID, err := c.ChatID()
	if err != nil {
		return err
	}
	msg := tgbotapi.NewMessage(chatID, text)
	return sendWithRetry(c, msg)
}

func (c *Context) Replyf(format string, args ...any) error {
	return c.Reply(fmt.Sprintf(format, args...))
}

func (c *Context) Text() string {
	if c.Update.Message == nil {
		return ""
	}
	return strings.TrimSpace(c.Update.Message.Text)
}

func sendWithRetry(c *Context, msg tgbotapi.MessageConfig) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		if _, err := c.Bot.Send(msg); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}
