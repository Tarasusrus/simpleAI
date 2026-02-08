# Инвентаризация строк и ошибок

Цель: собрать сырые строки/ошибки для выноса в централизованные константы.

## Пользовательские ответы (UI/UX)
- `internal/telegram/handlers.go`: приветствие, help, ошибки агента, ответы на пустые сообщения.
- `internal/telegram/middleware.go`: сообщения о доступе и rate‑limit.
- `internal/telegram/handlers.go`: ответы про сохранение вложений.

## Ошибки конфигурации
- `config/config.go`: ошибки отсутствия `API_KEY`, `SYS_PROMPT`.
- `internal/notify/telegram.go`: `telegram token/chat_id is not configured`.

## Ошибки LLM
- `internal/adapters/llm/openai/client.go`: `LLM_EMPTY_CHOICES`, `LLM_EMPTY_CONTENT`, `LLM_EMBED_EMPTY_INPUT`.
- `internal/adapters/llm/ollama/client.go`: `LLM_EMPTY_CONTENT`, `LLM_EMBED_EMPTY_INPUT`.

## Ошибки ingestion/RAG
- `internal/ingest/model.go`: validation messages (`source is required`, `raw_text is required`, ...).
- `internal/ingest/store.go`: `receipt already exists`.

## Почтовый воркер
- `internal/mail/gmail.go`: ошибки статуса Gmail API.
- `internal/mail/imap.go`: ошибки IMAP (authenticate/select/fetch).

## Прочее (CLI/воркеры)
- `cmd/*`: `Err while ...`, `failed to ...`, `query is required`.
