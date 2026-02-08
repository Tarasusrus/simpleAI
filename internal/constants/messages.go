// Package constants централизует строковые константы приложения, чтобы
// унифицировать тексты UI и коды/сообщения ошибок между подсистемами.
//
// Назначение пакета:
//   - Снять дублирование строк в Telegram, ingestion и службах.
//   - Обеспечить единые формулировки для пользователя и логов.
//   - Подготовить базу для локализации и переиспользования текстов.
package constants

// Telegram UI messages.
const (
	MsgTelegramStart              = "Привет! Я фасад для агентов. Напиши сообщение, и я отвечу."
	MsgTelegramHelp               = "Доступные команды:\n/start - приветствие\n/help - справка\n\nПоддержка файлов и фото будет добавлена отдельно."
	MsgTelegramEmpty              = "Пустое сообщение. Напиши запрос."
	MsgTelegramNoAgent            = "Агент не настроен."
	MsgTelegramAttachmentsSaved   = "Вложения сохранены: %s"
	MsgTelegramAttachmentsPending = "Вложения получены. Обработка будет добавлена скоро."
	MsgTelegramRateLimit          = "Слишком часто. Попробуй чуть позже."
	MsgTelegramChatNotAllowed     = "Этот чат (%d) не разрешен."
	MsgTelegramAgentError         = "Ошибка агента: %v"
	MsgTelegramNoReply            = "Нет ответа от агента."
)
