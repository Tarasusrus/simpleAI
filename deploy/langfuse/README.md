# Langfuse self-hosted (dev)

Локальный Langfuse v3 для просмотра трейсов агента simpleAI.

## Старт

```bash
cd deploy/langfuse
cp .env.example .env
# отредактируй секреты (CHANGEME) — для local dev можно оставить дефолты,
# но REDIS_AUTH / MINIO_ROOT_PASSWORD / LANGFUSE_INIT_USER_PASSWORD задай явно
docker compose --env-file .env up -d
```

Первый запуск качает образы (~2 ГБ) и накатывает миграции Postgres + Clickhouse — занимает 1–2 мин. Healthcheck'и блокируют `langfuse-web` пока зависимости не готовы.

## Доступ

| URL | Назначение |
|---|---|
| http://localhost:3001 | Langfuse UI (логин из `LANGFUSE_INIT_USER_*`) |
| http://localhost:9191 | MinIO Console (учётка из `MINIO_ROOT_*`) |
| postgres://postgres:postgres@localhost:5433 | Postgres Langfuse (отдельный, не путать с simpleai на 5432) |

При первом старте автоматически создаются:
- org `simpleai` / project `rag-mm`
- API-ключи `LANGFUSE_INIT_PROJECT_PUBLIC_KEY` / `LANGFUSE_INIT_PROJECT_SECRET_KEY`
- admin user из `LANGFUSE_INIT_USER_*`

## Проверка

```bash
# Статус
docker compose ps

# UI отвечает 200
curl -sI http://localhost:3001 | head -1

# Smoke ingest API (замени ключи на свои из .env)
curl -u "${LANGFUSE_INIT_PROJECT_PUBLIC_KEY}:${LANGFUSE_INIT_PROJECT_SECRET_KEY}" \
  http://localhost:3001/api/public/health
```

Ожидаемо: `200 OK` от обоих.

## Стоп / очистка

```bash
docker compose down            # остановить, volumes сохранены
docker compose down -v         # снести данные (postgres/clickhouse/minio/redis)
```

## Использование из агента

### 1. Включить трейсинг

В корневом `.env` simpleAI задай:

```
LANGFUSE_HOST=http://localhost:3001
LANGFUSE_PUBLIC_KEY=pk-lf-rag-mm-dr-local
LANGFUSE_SECRET_KEY=<сикрет из deploy/langfuse/.env, поле LANGFUSE_INIT_PROJECT_SECRET_KEY>
```

Если хотя бы одна переменная пустая — `observability.NewTracer` вернёт `nil`, агент работает как раньше без трейсинга (no-op).

Запуск приложения как обычно:
```bash
go run ./cmd/app
```

В логах при старте: `langfuse tracer started host=http://localhost:3001`.

### 2. Смотреть трейсы

UI: http://localhost:3001/project/rag-mm/traces

Каждый запрос пользователя в Telegram → один **trace** с именем `agent.run` (или `agent.ask` если skills отключены). Внутри trace:
- `llm.iterN` — generation (LLM-вызов на итерации N), показывает input/output
- `tool.<имя>` — span (вызов скилла), показывает input/output и latency
- `llm.final` — generation последнего ответа, если превышен лимит итераций

В trace проставлены `userId` (chat_id Telegram) и `sessionId` (uuid этого запроса) — можно фильтровать по пользователю.

### 3. Что под капотом

Файл `internal/observability/langfuse.go` — async HTTP-клиент Langfuse Ingestion API:
- События буферизуются в канал (1024 шт)
- Фоновая горутина шлёт батчи раз в 2 сек или по достижении 100 событий
- На переполнении буфера — событие дропается с `WARN` в лог, агент не блокируется
- При остановке (`Close()`) — буфер дренируется и шлётся последним батчем

Точки инструментации в `internal/agent/service.go`:
- `obs.StartTrace(...)` в начале `AskWithMeta` + `defer obsTrace.End(...)` в конце
- `obsTrace.StartGeneration(...)` вокруг каждого `client.AskWithSystem(...)`
- `obsTrace.StartSpan("tool."+name, input)` вокруг каждого `runSkill(...)`

### 4. Отключить трейсинг

Убери `LANGFUSE_HOST` (или `_KEY`) из `.env` — `NewTracer` вернёт nil, инструментация выключится без перекомпиляции.

### 5. Известные ограничения

- `model` поле в generation сейчас пустое → Langfuse не считает стоимость. TODO: пробрасывать имя модели из LLM-клиента.
- `usage` (токены) не шлётся — нужен SDK который их возвращает (DeepSeek/OpenAI клиенты их в `core.LLM` сейчас не отдают).
- Большие input/output не усекаются — отправляются целиком.

## Порт-карта (чтобы не конфликтовать)

| Хост | Сервис | Почему так |
|---|---|---|
| 3001 | langfuse-web | task требует 3001 |
| 5433 | langfuse-postgres | 5432 занят simpleai-postgres |
| 6379 | langfuse-redis | 127.0.0.1 only |
| 8123 / 9000 | clickhouse | 127.0.0.1 only |
| 9190 / 9191 | minio API/console | 9090 занят prometheus |
| 3030 | langfuse-worker | 127.0.0.1 only |
