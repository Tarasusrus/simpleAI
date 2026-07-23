#!/usr/bin/env bash
#
# refresh-replica.sh — обновить локальную read-only реплику боевой БД.
#
# Прод НЕ мутируется: только pg_dump (read-only) внутри прод-контейнера через SSH.
# Локально: restore в docker-контейнер simpleai-postgres + пересоздание read-only
# роли botclient_ro (её SELECT-гранты слетают при --clean, поэтому выдаём заново).
#
# GRACEFUL: любой недоступный ресурс (docker спит, комп/сеть/прод недоступны,
# postgres не поднят) — НЕ фатально: пишем warn в лог с таймстампом и выходим 0,
# оставляя предыдущую реплику нетронутой. launchd не спамит и не копит ошибки.
#
# Ручной запуск:  make -f MakeFile refresh-replica   (или  bash scripts/refresh-replica.sh)
# Почасовой:      launchd — deploy/launchd/com.simpleai.replica-refresh.plist
#
set -uo pipefail

# --- config ---
SSH_HOST="${REPLICA_SSH_HOST:-simpleai}"
PROD_CONTAINER="${REPLICA_PROD_CONTAINER:-simpleai-postgres}"
LOCAL_CONTAINER="${REPLICA_LOCAL_CONTAINER:-simpleai-postgres}"
DB="${POSTGRES_DB:-simpleai}"
DB_USER="${POSTGRES_USER:-simpleai}"
DUMP_DIR="${REPLICA_DUMP_DIR:-$HOME/.simpleai-replica}"
ENV_FILE="$DUMP_DIR/botclient.env"
LOG_FILE="${REPLICA_LOG_FILE:-$DUMP_DIR/refresh.log}"
DOCKER="${DOCKER_BIN:-docker}"
SSH="${SSH_BIN:-ssh}"
KEEP_DUMPS="${REPLICA_KEEP_DUMPS:-7}"
SSH_TIMEOUT="${REPLICA_SSH_TIMEOUT:-15}"

mkdir -p "$DUMP_DIR"

log()  { printf '%s [%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$1" "$2" | tee -a "$LOG_FILE" >&2; }
info() { log INFO "$1"; }
warn() { log WARN "$1"; }
# soft-fail: не фатально — пишем warn и выходим 0 (реплика остаётся прежней).
skip() { warn "SKIP: $1 → реплика не обновлена, выхожу без ошибки"; exit 0; }

info "=== refresh-replica start ==="

# --- 0. preflight: docker daemon ---
if ! "$DOCKER" info >/dev/null 2>&1; then
  skip "docker daemon недоступен (Docker спит/не запущен)"
fi
# локальный контейнер поднят?
if ! "$DOCKER" exec "$LOCAL_CONTAINER" pg_isready -U "$DB_USER" >/dev/null 2>&1; then
  skip "локальный контейнер $LOCAL_CONTAINER не готов (postgres не поднят)"
fi
# прод достижим по ssh?
if ! "$SSH" -o BatchMode=yes -o ConnectTimeout="$SSH_TIMEOUT" "$SSH_HOST" 'true' >/dev/null 2>&1; then
  skip "прод $SSH_HOST недоступен по SSH (комп/сеть/сервер offline)"
fi

# --- read-only роль: пароль стабилен между рефрешами ---
RO_PASS=""
if [[ -f "$ENV_FILE" ]]; then
  RO_PASS="$(sed -n 's#.*botclient_ro:\([^@]*\)@.*#\1#p' "$ENV_FILE")"
fi
if [[ -z "$RO_PASS" ]]; then
  RO_PASS="$(openssl rand -hex 16)"
  info "botclient.env отсутствует — сгенерирован новый пароль read-only роли"
fi

# --- 1. dump прода (read-only) ---
DUMP="$DUMP_DIR/prod-$(date +%Y%m%d-%H%M%S).dump"
info "dump прода через ssh $SSH_HOST (read-only)..."
if ! "$SSH" -o BatchMode=yes -o ConnectTimeout="$SSH_TIMEOUT" "$SSH_HOST" \
      "docker exec $PROD_CONTAINER pg_dump -U $DB_USER -Fc $DB" > "$DUMP" 2>>"$LOG_FILE"; then
  rm -f "$DUMP"; skip "pg_dump на проде упал (контейнер прода не запущен?)"
fi
if [[ ! -s "$DUMP" ]]; then
  rm -f "$DUMP"; skip "получен пустой дамп"
fi
info "дамп: $(du -h "$DUMP" | cut -f1)"

# --- 2. restore в локальный контейнер ---
info "restore в локальный $LOCAL_CONTAINER..."
if ! "$DOCKER" exec -i "$LOCAL_CONTAINER" pg_restore -U "$DB_USER" -d "$DB" \
      --clean --if-exists --no-owner --no-privileges < "$DUMP" 2>>"$LOG_FILE"; then
  warn "pg_restore вернул ненулевой код (возможны безобидные NOTICE) — проверяю данные ниже"
fi

# --- 3. read-only роль + гранты (идемпотентно) ---
if ! "$DOCKER" exec -i "$LOCAL_CONTAINER" psql -U "$DB_USER" -d "$DB" -v ON_ERROR_STOP=1 >/dev/null 2>>"$LOG_FILE" <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='botclient_ro') THEN
    CREATE ROLE botclient_ro LOGIN PASSWORD '$RO_PASS';
  ELSE
    ALTER ROLE botclient_ro PASSWORD '$RO_PASS';
  END IF;
END \$\$;
GRANT CONNECT ON DATABASE $DB TO botclient_ro;
GRANT USAGE ON SCHEMA public TO botclient_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO botclient_ro;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO botclient_ro;
SQL
then
  warn "не удалось пересоздать read-only роль — botclient может не подключиться"
fi

# --- 4. connection string для botclient ---
HOST_PORT="$("$DOCKER" port "$LOCAL_CONTAINER" 5432/tcp 2>/dev/null | head -1 | sed 's/.*://')"
HOST_PORT="${HOST_PORT:-55432}"
printf 'BOTCLIENT_DATABASE_URL=postgres://botclient_ro:%s@localhost:%s/%s\n' \
  "$RO_PASS" "$HOST_PORT" "$DB" > "$ENV_FILE"
chmod 600 "$ENV_FILE"

# --- 5. prune старых дампов ---
ls -t "$DUMP_DIR"/prod-*.dump 2>/dev/null | tail -n +"$((KEEP_DUMPS+1))" | xargs -r rm -f

TX="$("$DOCKER" exec "$LOCAL_CONTAINER" psql -U "$DB_USER" -d "$DB" -tAc 'select count(*) from budget_transaction' 2>/dev/null | tr -d '[:space:]')"
info "OK: budget_transaction=${TX:-?} строк, порт=$HOST_PORT, conn → $ENV_FILE"
info "=== refresh-replica done ==="
