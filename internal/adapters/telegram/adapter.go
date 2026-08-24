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

	botformat "simpleAI/internal/bot/format"
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
	return a.sendMessage(ctx, chatID, 0, text, nil)
}

func (a *Adapter) Reply(ctx context.Context, chatID int64, replyTo int, text string) error {
	return a.sendMessage(ctx, chatID, replyTo, text, nil)
}

// sendMessage — единственная точка отправки текста. Здесь и только здесь
// включается parse_mode: разметка обязана быть централизованной, иначе один
// забытый вызов роняет формат (или, хуже, отправку) уже после ревью.
//
// Порядок такой:
//  1. текст скилла переводится в HTML (экранируется ВСЁ, кроме моноблока и
//     **жирного** — см. internal/bot/format);
//  2. режется на куски в пределах лимита Telegram;
//  3. если Telegram всё же ответил ошибкой на размеченный вариант — тот же
//     текст уходит простым. Молчание бота хуже, чем звёздочки в чате.
//
// Клавиатура вешается на последний кусок: выше неё должен лежать весь текст,
// к которому она относится.
func (a *Adapter) sendMessage(ctx context.Context, chatID int64, replyTo int, text string, rows [][]core.Button) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	chunks := botformat.MessagesHTML(text)
	if len(chunks) == 0 {
		return nil
	}
	sent, err := a.deliver(chatID, replyTo, chunks, tgbotapi.ModeHTML, rows)
	if err == nil {
		return nil
	}
	// Фоллбэк — только если не ушло НИЧЕГО. Иначе повтор простым текстом
	// продублировал бы в чате уже доставленные куски.
	if sent > 0 {
		return err
	}
	_, err = a.deliver(chatID, replyTo, botformat.MessagesPlain(text), "", rows)
	return err
}

func (a *Adapter) deliver(chatID int64, replyTo int, chunks []string, parseMode string, rows [][]core.Button) (int, error) {
	for i, chunk := range chunks {
		msg := tgbotapi.NewMessage(chatID, chunk)
		msg.ParseMode = parseMode
		if replyTo != 0 && i == 0 {
			msg.ReplyToMessageID = replyTo
		}
		if len(rows) > 0 && i == len(chunks)-1 {
			msg.ReplyMarkup = toInlineKeyboard(rows)
		}
		if _, err := a.bot.Send(msg); err != nil {
			return i, err
		}
	}
	return len(chunks), nil
}

func (a *Adapter) SendTyping(ctx context.Context, chatID int64) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	_, err := a.bot.Request(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
	return err
}

func (a *Adapter) SendWithButtons(ctx context.Context, chatID int64, text string, rows [][]core.Button) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return a.sendMessage(ctx, chatID, 0, text, rows)
}

func (a *Adapter) EditWithButtons(ctx context.Context, chatID int64, messageID int, text string, rows [][]core.Button) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	// Правку резать некуда — сообщение одно. Слишком длинный текст всё же
	// усекается по лимиту: 400 «message is too long» стёр бы правку целиком.
	edit := tgbotapi.NewEditMessageText(chatID, messageID, firstChunk(botformat.MessagesHTML(text)))
	edit.ParseMode = tgbotapi.ModeHTML
	edit.ReplyMarkup = editKeyboard(rows)
	if _, err := a.bot.Send(edit); err != nil {
		// Тот же фоллбэк, что и у отправки: разметка не должна стирать содержимое.
		plain := tgbotapi.NewEditMessageText(chatID, messageID, firstChunk(botformat.MessagesPlain(text)))
		plain.ReplyMarkup = editKeyboard(rows)
		_, err := a.bot.Send(plain)
		return err
	}
	return nil
}

// editKeyboard решает, что приложить к правке сообщения:
//   - rows == nil — клавиатуру не трогаем (у сообщения её и не было);
//   - rows пуст, но не nil — ПУСТАЯ клавиатура, то есть «снять кнопки»;
//     ровно так вызывающий код гасит инлайн-кнопки после обработки callback-а,
//     и молчаливый пропуск reply_markup оставил бы их живыми;
//   - иначе — обычная клавиатура.
func editKeyboard(rows [][]core.Button) *tgbotapi.InlineKeyboardMarkup {
	if rows == nil {
		return nil
	}
	kb := toInlineKeyboard(rows)
	if kb.InlineKeyboard == nil {
		kb.InlineKeyboard = [][]tgbotapi.InlineKeyboardButton{}
	}
	return &kb
}

func firstChunk(chunks []string) string {
	if len(chunks) == 0 {
		return ""
	}
	return chunks[0]
}

func (a *Adapter) AnswerCallback(ctx context.Context, callbackID string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	cfg := tgbotapi.NewCallback(callbackID, "")
	_, err := a.bot.Request(cfg)
	return err
}

func toInlineKeyboard(rows [][]core.Button) tgbotapi.InlineKeyboardMarkup {
	keyboard := make([][]tgbotapi.InlineKeyboardButton, 0, len(rows))
	for _, row := range rows {
		kbRow := make([]tgbotapi.InlineKeyboardButton, 0, len(row))
		for _, btn := range row {
			kbRow = append(kbRow, tgbotapi.NewInlineKeyboardButtonData(btn.Text, btn.Data))
		}
		keyboard = append(keyboard, kbRow)
	}
	return tgbotapi.NewInlineKeyboardMarkup(keyboard...)
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
	if update.CallbackQuery != nil {
		cb := update.CallbackQuery
		return core.Update{
			ID:           update.UpdateID,
			ChatID:       cb.Message.Chat.ID,
			UserID:       cb.From.ID,
			UserName:     cb.From.UserName,
			DisplayName:  strings.TrimSpace(cb.From.FirstName + " " + cb.From.LastName),
			MessageID:    cb.Message.MessageID,
			IsCallback:   true,
			CallbackID:   cb.ID,
			CallbackData: cb.Data,
		}
	}
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
