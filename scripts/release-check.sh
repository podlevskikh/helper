#!/usr/bin/env bash
# Всё, что нужно перед пушем в main бэкенда (= выкатка на прод), одной командой:
#   1. scripts/check.sh — сборка как в Dockerfile, vet, все тесты на временной базе (TZ=UTC, REQUIRE_DB=1);
#   2. копия прод-подобной базы (helper_prodsim_base) с уникальным именем;
#   3. сервер на свободном порту (MAIL_DEV_LOG=1, PUSH_DRY_RUN=1, RATE_LIMIT_SCALE=100) — миграции на «старых» данных;
#   4. полный смоук (scripts/smoke.py) с проверкой писем через лог;
#   5. уборка: сервер остановлен, база удалена.
# Печатает только провалы и итог. Код выхода 0 — можно пушить.
set -euo pipefail
cd "$(dirname "$0")/.."
PG="${PG_ADMIN_URL:-postgres://helper:helper_password@localhost:5432/helper_db?sslmode=disable}"
TEMPLATE="${PRODSIM_TEMPLATE:-helper_prodsim_base}"
DB="helper_rel_$$"
WORK="$(mktemp -d)"
LOG="$WORK/server.log"
PID=""
cleanup() {
  if [ -n "$PID" ]; then kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; fi
  psql -q "$PG" -c "DROP DATABASE IF EXISTS $DB WITH (FORCE)" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "1/4 check.sh"
./scripts/check.sh > "$WORK/check.log" 2>&1 || { grep -E "^(---|FAIL|panic)|_test.go|error" "$WORK/check.log" | head -40; echo "✗ check.sh"; exit 1; }

echo "2/4 копия прод-данных $TEMPLATE → $DB"
psql -q "$PG" -c "CREATE DATABASE $DB TEMPLATE $TEMPLATE" >/dev/null

PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')"
echo "3/4 сервер на :$PORT"
go build -o "$WORK/server" ./cmd/server
TZ=UTC MAIL_DEV_LOG=1 PUSH_DRY_RUN=1 RATE_LIMIT_SCALE=100 GIT_SHA="$(git rev-parse --short HEAD)" \
  DATABASE_URL="${PG%/*}/$DB?sslmode=disable" PORT="$PORT" "$WORK/server" > "$LOG" 2>&1 &
PID=$!
for _ in $(seq 1 90); do
  curl -sf "http://localhost:$PORT/healthz" >/dev/null 2>&1 && break
  kill -0 "$PID" 2>/dev/null || { tail -30 "$LOG"; echo "✗ сервер не стартовал (миграции?)"; exit 1; }
  sleep 1
done
curl -sf "http://localhost:$PORT/healthz" >/dev/null || { tail -30 "$LOG"; echo "✗ /healthz не ответил"; exit 1; }

echo "4/4 смоук"
python3 scripts/smoke.py "http://localhost:$PORT" --quiet --mail-log "$LOG"
