// Package telegram содержит код пакета telegram и его задачи.
package telegram

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Incoming struct {
	ChatID      int64
	UserID      int64
	UserName    string
	DisplayName string
	Text        string
	Attachments []Attachment
}

type Attachment struct {
	Type     string
	FileID   string
	FileName string
	MimeType string
	Size     int
}

func FromUpdate(update tgbotapi.Update) (Incoming, bool) {
	if update.Message == nil {
		return Incoming{}, false
	}
	msg := update.Message
	in := Incoming{
		ChatID:      msg.Chat.ID,
		UserID:      msg.From.ID,
		UserName:    msg.From.UserName,
		DisplayName: strings.TrimSpace(strings.Join([]string{msg.From.FirstName, msg.From.LastName}, " ")),
		Text:        strings.TrimSpace(msg.Text),
	}

	if msg.Document != nil {
		in.Attachments = append(in.Attachments, Attachment{
			Type:     "document",
			FileID:   msg.Document.FileID,
			FileName: msg.Document.FileName,
			MimeType: msg.Document.MimeType,
			Size:     msg.Document.FileSize,
		})
	}
	if len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		in.Attachments = append(in.Attachments, Attachment{
			Type:   "photo",
			FileID: photo.FileID,
			Size:   photo.FileSize,
		})
	}
	if msg.Voice != nil {
		in.Attachments = append(in.Attachments, Attachment{
			Type:     "voice",
			FileID:   msg.Voice.FileID,
			MimeType: msg.Voice.MimeType,
			Size:     msg.Voice.FileSize,
		})
	}
	if msg.Audio != nil {
		in.Attachments = append(in.Attachments, Attachment{
			Type:     "audio",
			FileID:   msg.Audio.FileID,
			FileName: msg.Audio.FileName,
			MimeType: msg.Audio.MimeType,
			Size:     msg.Audio.FileSize,
		})
	}
	return in, true
}

func (i Incoming) AttachmentSummary() string {
	if len(i.Attachments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(i.Attachments))
	for _, a := range i.Attachments {
		label := a.Type
		if a.FileName != "" {
			label = fmt.Sprintf("%s:%s", a.Type, a.FileName)
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}
