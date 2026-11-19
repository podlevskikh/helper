#!/usr/bin/env bash
# Почта на проде БЕЗ доступа к переменным Railway (запасной путь; основной — переменные Railway,
# см. docs/ops/mail.md). Подходит любой SMTP: Resend (по умолчанию), Gmail, Postmark…
# Спрашивает адрес сервера, логин, пароль/API-ключ (ввод скрыт) и отправителя, шифрует их ключом
# прод-сервера в secrets.sealed.json (остальные значения файла сохраняются). Расшифровать может только сервер.
# Переменные Railway важнее файла: если SMTP_* заданы там, значения отсюда не используются.
# После: git add secrets.sealed.json && git commit && git push (по регламенту выкатки).
set -euo pipefail
cd "$(dirname "$0")/.."
SERVER="${1:-https://www.helperday.app}"
ask() { local v; read -r -p "$1 [$2]: " v; echo "${v:-$2}"; }
SMTP_HOST="$(ask "SMTP-сервер" "smtp.resend.com")"
SMTP_PORT="$(ask "Порт" "587")"
SMTP_USER="$(ask "Логин" "resend")"
read -r -s -p "Пароль или API-ключ (ввод скрыт): " SMTP_PASSWORD
echo
[ -n "$SMTP_PASSWORD" ] || { echo "Пустой пароль" >&2; exit 1; }
MAIL_FROM="$(ask "Адрес отправителя" "no-reply@helperday.app")"
MAIL_FROM_NAME="$(ask "Имя отправителя" "Helper Day")"
printf 'SMTP_HOST=%s\nSMTP_PORT=%s\nSMTP_USER=%s\nSMTP_PASSWORD=%s\nMAIL_FROM=%s\nMAIL_FROM_NAME=%s\n' \
  "$SMTP_HOST" "$SMTP_PORT" "$SMTP_USER" "$SMTP_PASSWORD" "$MAIL_FROM" "$MAIL_FROM_NAME" \
  | go run ./cmd/seal -server "$SERVER" -out secrets.sealed.json
echo "Готово: secrets.sealed.json. Дальше: git add secrets.sealed.json && git commit -m 'chore: почта' && git push"
