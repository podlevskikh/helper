# Бэкенд Helper Day — правила для агентов

**Пуш в `origin main` = выкатка на прод** (Railway собирает `Dockerfile`, примерно 1,5 минуты). Поэтому:

- Перед пушем в `main`: `./scripts/release-check.sh` — одна команда:
  - `check.sh`: сборка, vet, все тесты на временной базе;
  - копия прод-подобной базы;
  - сервер;
  - полный смоук.

  Хук Claude (`.claude/settings.json`) запускает её сам и блокирует пуш, если она не прошла.
- GitHub Actions: `ci` (то же в UTC и UTC+14) на каждый пуш, `post-deploy` (смоук прода после выкатки), `monitor` (`/healthz` раз в 15 минут), `backup` (ночной зашифрованный дамп). Провал → issue `prod-smoke` / `prod-down`.
- Если `main` защищена (docs/ops/github-setup.md): сначала `git push origin HEAD:refs/heads/ci/<sha>`, дождаться зелёного `ci` (`gh run watch … --exit-status`), потом `git push origin HEAD:main`.
- После пуша: `python3 scripts/smoke.py https://helper-production-12b0.up.railway.app --expect-sha $(git rev-parse HEAD) --quiet`.
- Пароли на проде не вводим — ни в браузере, ни через API.
- Миграции данных — только в `internal/database/migrations.go`: новый шаг с id, выполняется один раз в транзакции.
  Колонки и таблицы не удаляем в том же релизе, где перестаём использовать (expand/contract).
- Новый маршрут → строка в матрице прав `internal/server/rbac_test.go` (тест падает без неё).
- Ошибки 5xx и паники — в Sentry, если задан `SENTRY_DSN` (`internal/observability`; без личных данных). Секреты без доступа к Railway — `scripts/seal-*.sh` → `secrets.sealed.json`.
- Что настраивают люди (защита ветки, секреты, бэкап, UptimeRobot, Sentry) — `docs/ops/`.
- Веб-версию раздаёт этот сервер из `webapp/` (собирается во фронте `scripts/build-for-backend.sh`).

Регламент, реестр кейсов и журнал — во фронте: `docs/qa/release.md`, `docs/qa/test-cases.md`, `docs/qa/runs.md`.
Коммиты — по-русски, `feat/fix/refactor(scope): …`.
