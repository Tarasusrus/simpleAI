# SimpleAI

Простой агент с базовой инфраструктурой для чеков, RAG и каналов ввода.

## Что есть сейчас
- CLI‑агент, который отвечает через LLM.
- База Postgres + pgvector и миграции.
- Ingestion чеков (receipt + items + rag_document).
- RAG‑пайплайн: генерация эмбеддингов и поиск по векторам.
- Черновой Telegram‑фасад (текст + сохранение вложений).
- Чековые вложения храним как файлы, а в БД — ссылки на них.
- Telegram‑вход сохраняет сырой payload в `TELEGRAM_MEDIA_DIR` для последующей обработки.
- Централизация текстовых сообщений и кодов ошибок в `internal/constants` (в процессе).
- Базовые интерфейсы LLM/Bot вынесены в `internal/core` для устранения vendor lock‑in.
- Telegram‑адаптер вынесен в `internal/adapters/telegram`.
- Докстринги пакетов обновлены: описывают назначение, границы и ключевые точки входа.

## Как пользоваться (кратко)
1) Поднять базу: `docker compose up -d`, `make migrate-up`.
2) Загрузить чек: `go run cmd/ingest/main.go -file receipt.json`.
3) Сгенерировать эмбеддинги: `go run cmd/embeddings/main.go -limit 100 -batch 20`.
4) Проверить retrieval: `go run cmd/rag-query/main.go -q "сахар в январе" -limit 5`.

## Telegram‑бот: запуск и тест
1) Заполни `.env` (минимум): `SYS_PROMPT`, `TELEGRAM_BOT_TOKEN`.
   - Если `LLM_PROVIDER=openai`, нужен `API_KEY`.
   - Если `LLM_PROVIDER=ollama`, `API_KEY` не нужен.
2) Если LLM недоступен (ошибка тестового запроса), будет автопереход на Ollama.
3) Меню команд бота добавляется автоматически (/start, /help).
4) Для расширенного логирования установи `LOG_LEVEL=debug`.
2) Опционально: `TELEGRAM_ALLOWED_CHATS`, `TELEGRAM_MEDIA_DIR`, `TELEGRAM_RATE_LIMIT_MS`.
3) Запуск: `go run cmd/telegram/main.go`.
4) Тест‑чеклист:
   - `/start` и `/help` отвечают корректно.
   - Обычный текст вызывает ответ агента.
   - Пустое сообщение возвращает подсказку.
   - Вложения сохраняются в `TELEGRAM_MEDIA_DIR`.

## Статус
Фокус сейчас на RAG по тратам и стабильности LLM‑клиента.
Параллельно идет унификация строк и ошибок в константы.
Описание пакетов в коде приведено к подробным docstring‑комментариям.
