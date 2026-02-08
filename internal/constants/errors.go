// Package constants централизует строковые константы приложения, чтобы
// унифицировать тексты UI и коды/сообщения ошибок между подсистемами.
//
// Назначение пакета:
//   - Снять дублирование строк в Telegram, ingestion и службах.
//   - Обеспечить единые формулировки для пользователя и логов.
//   - Подготовить базу для локализации и переиспользования текстов.
package constants

// LLM error codes/messages.
const (
	ErrCodeLLMEmptyContent    = "LLM_EMPTY_CONTENT"
	ErrMsgLLMEmptyContent     = "empty chat completion content"
	ErrCodeLLMEmptyChoices    = "LLM_EMPTY_CHOICES"
	ErrMsgLLMEmptyChoices     = "empty chat completion choices"
	ErrCodeLLMEmbedEmptyInput = "LLM_EMBED_EMPTY_INPUT"
	ErrMsgLLMEmbedEmptyInput  = "no inputs for embedding"
)
