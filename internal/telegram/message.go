// Package telegram содержит код пакета telegram и его задачи.
package telegram

import (
	"fmt"
	"strings"

	"simpleAI/internal/core"
)

type Incoming struct {
	ChatID      int64
	UserID      int64
	UserName    string
	DisplayName string
	Text        string
	Attachments []core.Attachment
}

func FromUpdate(update core.Update) (Incoming, bool) {
	if update.ChatID == 0 {
		return Incoming{}, false
	}
	return Incoming{
		ChatID:      update.ChatID,
		UserID:      update.UserID,
		UserName:    update.UserName,
		DisplayName: update.DisplayName,
		Text:        strings.TrimSpace(update.Text),
		Attachments: update.Attachments,
	}, true
}

func (i Incoming) AttachmentSummary() string {
	if len(i.Attachments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(i.Attachments))
	for _, a := range i.Attachments {
		label := a.Kind
		if a.Name != "" {
			label = fmt.Sprintf("%s:%s", a.Kind, a.Name)
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}
