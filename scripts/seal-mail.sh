#!/usr/bin/env bash
# Настройка почты на проде без доступа к переменным Railway.
# Спрашивает адрес Gmail и пароль приложения (https://myaccount.google.com/apppasswords),
# шифрует их ключом прод-сервера в secrets.sealed.json. Расшифровать может только сервер.
# После: git add secrets.sealed.json && git commit && git push — письма начнут уходить.
set -euo pipefail
cd "$(dirname "$0")/.."
SERVER="${1:-https://helper-production-12b0.up.railway.app}"
read -r -p "Адрес Gmail, с которого слать письма: " SMTP_USER
read -r -s -p "Пароль приложения Google (16 букв, ввод скрыт): " SMTP_PASSWORD
echo
read -r -p "Имя отправителя [Helper]: " MAIL_FROM_NAME
MAIL_FROM_NAME="${MAIL_FROM_NAME:-Helper}"
printf 'SMTP_USER=%s\nSMTP_PASSWORD=%s\nMAIL_FROM_NAME=%s\n' "$SMTP_USER" "$SMTP_PASSWORD" "$MAIL_FROM_NAME" \
  | go run ./cmd/seal -server "$SERVER" -out secrets.sealed.json
