package core

import "context"

// VisionRequest — один запрос к модели, которая читает картинки.
// MimeType берётся из сигнатуры файла, а не из заявленного клиентом типа:
// провайдер получает тот формат, который реально лежит в Data.
type VisionRequest struct {
	Model     string
	System    string
	Prompt    string
	MimeType  string
	Data      []byte
	MaxTokens int
}

// VisionLLM отправляет картинку ровно в указанную модель. Реализация не
// переключается на другую модель при ошибке: картинка не должна уйти модели
// без vision (ADR-010 §4).
type VisionLLM interface {
	AskVision(ctx context.Context, req VisionRequest) (string, error)
}

// ModelCapabilities — что модель (группа деплойментов) принимает на вход.
type ModelCapabilities struct {
	Vision   bool
	PDFInput bool
}

// ModelInfo — модель из каталога вместе с её возможностями.
type ModelInfo struct {
	Name         string
	Capabilities ModelCapabilities
}

// ModelCatalog перечисляет модели, доступные боту, с их возможностями.
type ModelCatalog interface {
	Models(ctx context.Context) ([]ModelInfo, error)
}
