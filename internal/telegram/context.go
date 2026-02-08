package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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
	_, err = c.Bot.Send(msg)
	return err
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
