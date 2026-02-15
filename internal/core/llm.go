// Package core задает доменные контракты приложения (интерфейсы и модели),
// чтобы бизнес-логика зависела только от абстракций, а не от провайдеров.
package core

import "context"

// LLM описывает минимальный контракт LLM для генерации ответа на запрос.
type LLM interface {
	Ask(ctx context.Context, prompt string) (string, error)
	// AskWithSystem объединяет базовый SysPrompt с systemAddition и отправляет
	// userPrompt как user-сообщение. Позволяет динамически расширять system prompt
	// (например, добавлять описание доступных tools) без изменения конфига.
	AskWithSystem(ctx context.Context, systemAddition, userPrompt string) (string, error)
}

// Embedder описывает контракт генерации эмбеддингов.
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

// LLMClient объединяет чат и эмбеддинги в единый контракт.
type LLMClient interface {
	LLM
	Embedder
}
