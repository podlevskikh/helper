#!/usr/bin/env bash
# Включение Sentry на проде без доступа к переменным Railway.
# Спрашивает DSN проекта Sentry (Settings → Projects → <проект> → Client Keys (DSN)),
# шифрует его ключом прод-сервера в secrets.sealed.json (остальные значения файла сохраняются).
# Расшифровать может только сервер. После: git add secrets.sealed.json && git commit && git push.
# Проверка после выкатки: в логе Railway строка «sentry: включён (окружение production)».
set -euo pipefail
cd "$(dirname "$0")/.."
SERVER="${1:-https://www.helperday.app}"
read -r -s -p "SENTRY_DSN (https://…@…ingest.sentry.io/…, ввод скрыт): " SENTRY_DSN
echo
case "$SENTRY_DSN" in https://*@*) ;; *) echo "Это не похоже на DSN Sentry" >&2; exit 1 ;; esac
printf 'SENTRY_DSN=%s\n' "$SENTRY_DSN" | go run ./cmd/seal -server "$SERVER" -out secrets.sealed.json
echo "Готово: secrets.sealed.json. Дальше: git add secrets.sealed.json && git commit -m 'chore: SENTRY_DSN' && git push"
