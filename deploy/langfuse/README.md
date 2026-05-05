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

В корневом `.env` simpleAI задай:

```
LANGFUSE_HOST=http://localhost:3001
LANGFUSE_PUBLIC_KEY=pk-lf-rag-mm-dr-local
LANGFUSE_SECRET_KEY=sk-lf-rag-mm-dr-local-...
```

Инструментация — задача `simpleAI-8h8` (разблокируется после закрытия `simpleAI-0ya`).

## Порт-карта (чтобы не конфликтовать)

| Хост | Сервис | Почему так |
|---|---|---|
| 3001 | langfuse-web | task требует 3001 |
| 5433 | langfuse-postgres | 5432 занят simpleai-postgres |
| 6379 | langfuse-redis | 127.0.0.1 only |
| 8123 / 9000 | clickhouse | 127.0.0.1 only |
| 9190 / 9191 | minio API/console | 9090 занят prometheus |
| 3030 | langfuse-worker | 127.0.0.1 only |
