// Package telegram реализует адаптер Telegram на базе tgbotapi.
package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"simpleAI/internal/core"
)

type Adapter struct {
	bot            *tgbotapi.BotAPI
	pollingTimeout time.Duration
	httpClient     *http.Client
}

type Command struct {
	Command     string
	Description string
}

func NewAdapter(token string, pollingTimeout time.Duration) (*Adapter, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("telegram bot token is empty")
	}
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	if pollingTimeout <= 0 {
		pollingTimeout = 30 * time.Second
	}
	return &Adapter{
		bot:            bot,
		pollingTimeout: pollingTimeout,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (a *Adapter) SetCommands(ctx context.Context, commands []Command) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if len(commands) == 0 {
		return nil
	}
	items := make([]tgbotapi.BotCommand, 0, len(commands))
	for _, cmd := range commands {
		if strings.TrimSpace(cmd.Command) == "" {
			continue
		}
		items = append(items, tgbotapi.BotCommand{
			Command:     cmd.Command,
			Description: cmd.Description,
		})
	}
	if len(items) == 0 {
		return nil
	}
	_, err := a.bot.Request(tgbotapi.NewSetMyCommands(items...))
	return err
}

func (a *Adapter) Updates(ctx context.Context) (<-chan core.Update, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := tgbotapi.NewUpdate(0)
	cfg.Timeout = int(a.pollingTimeout / time.Second)
	raw := a.bot.GetUpdatesChan(cfg)

	out := make(chan core.Update)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case upd, ok := <-raw:
				if !ok {
					return
				}
				if msg := toCoreUpdate(upd); msg.ChatID != 0 {
					out <- msg
				}
			}
		}
	}()
	return out, nil
}

func (a *Adapter) Send(ctx context.Context, chatID int64, text string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := a.bot.Send(msg)
	return err
}

func (a *Adapter) Reply(ctx context.Context, chatID int64, replyTo int, text string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyTo
	_, err := a.bot.Send(msg)
	return err
}

func (a *Adapter) SendTyping(ctx context.Context, chatID int64) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	_, err := a.bot.Request(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
	return err
}

func (a *Adapter) FetchAttachment(ctx context.Context, attachment core.Attachment) (io.ReadCloser, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(attachment.ID) == "" {
		return nil, "", fmt.Errorf("attachment id is empty")
	}
	file, err := a.bot.GetFile(tgbotapi.FileConfig{FileID: attachment.ID})
	if err != nil {
		return nil, "", err
	}
	url := file.Link(a.bot.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, "", err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if err := resp.Body.Close(); err != nil {
			_ = err
		}
		return nil, "", fmt.Errorf("telegram download status %d", resp.StatusCode)
	}
	return resp.Body, file.FilePath, nil
}

func toCoreUpdate(update tgbotapi.Update) core.Update {
	if update.Message == nil {
		return core.Update{}
	}
	msg := update.Message
	coreUpdate := core.Update{
		ID:          update.UpdateID,
		ChatID:      msg.Chat.ID,
		UserID:      msg.From.ID,
		UserName:    msg.From.UserName,
		DisplayName: strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName),
		Text:        strings.TrimSpace(msg.Text),
		MessageID:   msg.MessageID,
	}
	if msg.Document != nil {
		coreUpdate.Attachments = append(coreUpdate.Attachments, core.Attachment{
			Kind:     "document",
			ID:       msg.Document.FileID,
			Name:     msg.Document.FileName,
			MimeType: msg.Document.MimeType,
			Size:     int64(msg.Document.FileSize),
		})
	}
	if len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		coreUpdate.Attachments = append(coreUpdate.Attachments, core.Attachment{
			Kind: "photo",
			ID:   photo.FileID,
			Size: int64(photo.FileSize),
		})
	}
	if msg.Voice != nil {
		coreUpdate.Attachments = append(coreUpdate.Attachments, core.Attachment{
			Kind:     "voice",
			ID:       msg.Voice.FileID,
			MimeType: msg.Voice.MimeType,
			Size:     int64(msg.Voice.FileSize),
		})
	}
	if msg.Audio != nil {
		coreUpdate.Attachments = append(coreUpdate.Attachments, core.Attachment{
			Kind:     "audio",
			ID:       msg.Audio.FileID,
			Name:     msg.Audio.FileName,
			MimeType: msg.Audio.MimeType,
			Size:     int64(msg.Audio.FileSize),
		})
	}
	return coreUpdate
}
