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

// Ingestion validation errors.
const (
	ErrMsgIngestSourceRequired     = "source is required"
	ErrMsgIngestSourceRefRequired  = "source_ref is required"
	ErrMsgIngestRawTextRequired    = "raw_text is required"
	ErrMsgIngestTotalNonNegative   = "total_amount must be non-negative"
	ErrMsgIngestItemNameRequired   = "items[%s].name is required"
	ErrMsgIngestItemQtyNonNegative = "items[%s].quantity must be non-negative"
	ErrMsgIngestItemUnitNonNeg     = "items[%s].unit_price must be non-negative"
	ErrMsgIngestItemAmtNonNegative = "items[%s].amount must be non-negative"
	ErrMsgIngestReceiptExists      = "receipt already exists"
)
