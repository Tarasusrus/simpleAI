// Package core задает доменные контракты приложения (интерфейсы и модели),
// чтобы бизнес-логика зависела только от абстракций, а не от провайдеров.
package core

import "context"

// Attachment описывает вложение в сообщении бота.
type Attachment struct {
	ID       string
	Name     string
	Path     string
	MimeType string
	Size     int64
	Kind     string
}

// Update описывает входящее сообщение бота в нейтральном виде.
type Update struct {
	ID          int
	ChatID      int64
	UserID      int64
	UserName    string
	DisplayName string
	Text        string
	MessageID   int
	Attachments []Attachment
}

// Bot задает интерфейс для приема обновлений и отправки сообщений.
type Bot interface {
	Updates(ctx context.Context) (<-chan Update, error)
	Send(ctx context.Context, chatID int64, text string) error
	Reply(ctx context.Context, chatID int64, replyTo int, text string) error
	SendTyping(ctx context.Context, chatID int64) error
}
