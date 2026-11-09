package plugin

import "context"

// Skill defines a minimal contract for pluggable agent capabilities.
type Skill interface {
	Manifest() Manifest
	Run(ctx context.Context, input string) (string, error)
}

// Manifest describes a skill for registry and discovery.
type Manifest struct {
	ID           string
	Name         string
	Description  string
	Version      string
	InputSchema  *Schema
	OutputSchema *Schema
	// DirectReply: результат Run уже готов к показу пользователю и не требует синтеза LLM.
	// Если в итерации все вызовы успешны и у всех навыков флаг стоит, агент возвращает
	// результаты как есть, без второго вызова LLM, который мог бы их пересказать
	// (потерять предупреждение, подменить символ валюты). Навыки, чей результат —
	// сырьё для ответа (например, промпт с найденными фрагментами), флаг не ставят.
	DirectReply bool
}
