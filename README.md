# SimpleAI

Персональный AI-ассистент с RAG-поиском по базе знаний, Telegram-интерфейсом и MCP-шлюзом для внешних агентов (Claude Code и др.).

---

## Архитектура

```
Пользователь
    │
    ├─── Telegram сообщение
    │        ↓
    │    Telegram Bot (cmd/telegram)
    │        ↓
    │    agent.Service  ←── plugin.Registry ──── RAGSearchSkill
    │        ↓                                        ↓
    │    LLM (OpenAI/Ollama)              rag.Retriever → pgvector
    │
    └─── Claude Code / внешний агент
             ↓ HTTP/SSE :8080
         MCP Server (cmd/mcp)
             ↓
         plugin.Registry (тот же)
```

**Ключевое:** `plugin.Registry` — единственный источник правды по доступным инструментам.
Оба входа (Telegram и MCP) используют один реестр.

---

## Структура каталогов

```
cmd/
  agent/        — CLI-агент: читает stdin, отвечает через LLM (git diff → code review)
  embeddings/   — Генерация эмбеддингов для rag_document записей без embedding
  ingest/       — Загрузка чеков из JSON в БД + rag_document
  mcp/          — MCP HTTP/SSE сервер на :8080 (внешние агенты)
  rag-query/    — CLI для ручной проверки RAG-поиска
  telegram/     — Telegram-бот (основной пользовательский интерфейс)
  worker/       — Фоновый воркер

internal/
  adapters/
    llm/        — Фабрика LLM-клиентов (OpenAI, Ollama, автофоллбэк)
    telegram/   — Обёртка над telegram-bot-api (отправка, polling, вложения)
  agent/
    core.go     — Struct Agent (LLM + Registry + Logger), используется в CLI
    service.go  — Service.Ask() с tool calling loop для Telegram
  core/
    llm.go      — Интерфейсы LLM, Embedder, LLMClient
    bot.go      — Интерфейсы Bot, Update, Attachment
  db/
    postgres.go — pgxpool.Pool по DBConfig
  ingest/       — Модели и store для загрузки чеков
  mail/         — Gmail/IMAP интеграция, дайджест
  mcp/
    server.go   — Адаптер plugin.Registry → mcp-go инструменты
  notify/       — Telegram-уведомления из фоновых задач
  plugin/
    registry.go — Registry: Register / Get / List
    types.go    — Интерфейс Skill, struct Manifest
    schema.go   — Schema для input/output
  rag/
    store.go    — Хранение и обновление эмбеддингов в rag_document
    retriever.go— Векторный поиск (pgvector) с фильтрами
    prompt.go   — BuildPrompt: контекст + вопрос → строка для LLM
  skills/
    rag_search.go — RAGSearchSkill: embed → search → BuildPrompt
  telegram/     — Роутер, хендлеры, middleware, контекст
  tools/        — NewLogger (slog)

config/
  config.go     — LoadConfig из .env

migrations/     — SQL-миграции (goose)
```

---

## Компоненты и их роли

### agent.Service (internal/agent/service.go)
Основной оркестратор для Telegram. При наличии registry делает tool calling:
1. Строит system prompt со списком skills
2. Отправляет LLM: пользователь получает JSON `{"skill": "...", "input": {...}}`
3. Выполняет skill, передаёт результат обратно в LLM
4. Возвращает финальный ответ

### plugin.Registry (internal/plugin/)
Реестр skills. Ключевые методы: `Register(Skill)`, `Get(id)`, `List() []Manifest`.

### RAGSearchSkill (internal/skills/rag_search.go)
Единственный активный skill. Input: `{"query": "...", "limit": 5}`.
Поток: embed query → pgvector search → BuildPrompt.

### MCP Server (internal/mcp/server.go + cmd/mcp/main.go)
Превращает registry в MCP-инструменты над HTTP/SSE. Claude Code и другие MCP-клиенты подключаются к `:8080/sse`.

---

## Переменные окружения (.env)

```env
# Обязательно
SYS_PROMPT=You are a helpful assistant...
API_KEY=sk-...                     # OpenAI (не нужен при LLM_PROVIDER=ollama)

# LLM
LLM_PROVIDER=openai                # openai | ollama
LLM_CHAT_MODEL=gpt-4.1-mini
EMBEDDING_MODEL=text-embedding-3-small

# Ollama (если LLM_PROVIDER=ollama)
OLLAMA_BASE_URL=http://localhost:11434
OLLAMA_MODEL=qwen2.5:7b-instruct-q4_K_M
OLLAMA_EMBED_MODEL=nomic-embed-text

# База данных
POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_DB=simpleai
POSTGRES_USER=simpleai
POSTGRES_PASSWORD=simpleai

# Telegram
TELEGRAM_BOT_TOKEN=...
TELEGRAM_ALLOWED_CHATS=123456789  # чат ID через запятую
TELEGRAM_MEDIA_DIR=data/telegram
TELEGRAM_WORKERS=4
TELEGRAM_RATE_LIMIT_MS=500

# Логирование
LOG_LEVEL=info                     # debug | info | warn | error
LOG_FORMAT=text                    # text | json
```

---

## Запуск

### 1. Инфраструктура

```bash
docker compose up -d   # Postgres + pgvector
make migrate-up        # применить миграции
```

### 2. Telegram-бот

```bash
go run cmd/telegram/main.go
```

Бот при старте:
- подключается к БД и регистрирует `rag_search` skill
- если БД недоступна — продолжает работу без RAG (только LLM)
- команды `/start`, `/help` — встроены
- любое сообщение — проходит через tool calling loop

### 3. MCP-сервер (для Claude Code и внешних агентов)

```bash
make run-mcp
# или: go run ./cmd/mcp
```

Сервер поднимается на `:8080/sse`. Для подключения Claude Code — файл `.mcp.json` уже в корне:

```json
{
  "mcpServers": {
    "simpleai": { "type": "http", "url": "http://localhost:8080/sse" }
  }
}
```

Проверка в Claude Code:
```
/mcp   →  должен показать инструмент rag_search
```

### 4. Загрузка данных (чеки)

```bash
go run cmd/ingest/main.go -file receipt.json
go run cmd/embeddings/main.go -limit 100 -batch 20
```

### 5. Ручная проверка RAG

```bash
go run cmd/rag-query/main.go -q "сахар в январе" -limit 5
```

### 6. Code review через CLI-агент

```bash
git diff | go run cmd/agent/main.go
# или через make:
make run-diff
```

---

## Добавить новый Skill

1. Создать `internal/skills/my_skill.go`, реализовать интерфейс `plugin.Skill`:
   ```go
   func (s *MySkill) Manifest() plugin.Manifest { ... }
   func (s *MySkill) Run(ctx context.Context, input string) (string, error) { ... }
   ```
2. Зарегистрировать в `cmd/telegram/main.go` → `buildRegistry()` и в `cmd/mcp/main.go`.
3. Обновить README.

---

## Поток запроса через Telegram

```
Пользователь: "найди чеки за январь"
    ↓
telegram.HandleDefault
    ↓
agent.Service.Ask(ctx, "найди чеки за январь")
    ↓  (registry не пуст)
LLM ← system prompt с описанием skills + запрос пользователя
    ↓
LLM → {"skill":"rag_search","input":{"query":"чеки за январь"}}
    ↓
RAGSearchSkill.Run → embed → pgvector search → BuildPrompt
    ↓
LLM ← BuildPrompt (контекст из БД + вопрос)
    ↓
LLM → финальный ответ на русском
    ↓
Telegram: отправить ответ пользователю
```

---

## Make-таргеты

| Команда | Что делает |
|---------|-----------|
| `make db-up` | Поднять Postgres в Docker |
| `make migrate-up` | Применить SQL-миграции |
| `make migrate-down` | Откатить последнюю миграцию |
| `make run-mcp` | Запустить MCP SSE-сервер на :8080 |
| `make run-diff` | Git diff текущего проекта → code review агент |
| `make lint` | Запустить golangci-lint |
