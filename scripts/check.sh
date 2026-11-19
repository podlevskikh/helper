#!/usr/bin/env bash
# Проверка бэкенда перед пушем в main (= выкатка на прод): сборка, vet, все тесты.
# Интеграционные тесты (internal/handlers) идут на временной локальной базе — создаётся и удаляется сама.
# Свою базу можно передать: TEST_DATABASE_URL=postgres://... ./scripts/check.sh
set -euo pipefail
cd "$(dirname "$0")/.."
# Как на проде: сервер в UTC (CHECK_TZ — другой пояс, CI гоняет ещё и UTC+14).
# REQUIRE_DB=1 — интеграционные тесты падают без базы, а не пропускаются молча.
export TZ="${CHECK_TZ:-UTC}" REQUIRE_DB=1
PG="${PG_ADMIN_URL:-postgres://helper:helper_password@localhost:5432/helper_db?sslmode=disable}"
go build ./...
CGO_ENABLED=0 GOOS=linux go build -o /dev/null ./cmd/server   # как в Dockerfile
go vet ./...
if [ -z "${TEST_DATABASE_URL:-}" ]; then
  DB="helper_check_$$"
  psql -q "$PG" -c "CREATE DATABASE $DB" >/dev/null
  trap 'psql -q "$PG" -c "DROP DATABASE IF EXISTS $DB WITH (FORCE)" >/dev/null' EXIT
  export TEST_DATABASE_URL="${PG%/*}/$DB?sslmode=disable"
fi
go test -p 1 -count=1 ./... 2>&1 | grep -v "no test files"
echo "check: ок"
