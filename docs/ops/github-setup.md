# GitHub и Railway: что настроить владельцу (≈15 минут)

Для владельца репозитория `podlevskikh/helper` и проекта Railway. Агенты сделать это не могут: нужны права администратора.

## 1. Защита ветки `main` (5 минут)

Зачем: пуш в `main` = выкатка на прод. Сейчас проверку перед пушем делает хук Claude у того, кто пушит; защита ветки делает её обязательной для всех — и для людей, и для любых сессий.

GitHub → `podlevskikh/helper` → **Settings → Rules → Rulesets → New ruleset → New branch ruleset**:

1. Name: `main`. Enforcement status: **Active**.
2. Target branches → **Add target → Include default branch**.
3. Отметить:
   - **Restrict deletions**;
   - **Block force pushes**;
   - **Require status checks to pass** → **Add checks**: `check (UTC)` и `check (Pacific/Kiritimati)` (появятся в списке после первого прогона `ci`, он уже был 19.11.2026). Галочку «Require branches to be up to date» **не** ставить.
4. **Pull request не требовать** — агенты пушат сами (см. ниже).
5. Bypass list — пусто (или только вы, на случай аварии).
6. **Create**.

Как после этого пушат агенты и люди (уже записано в `AGENTS.md`):
```bash
git push origin HEAD:refs/heads/ci/$(git rev-parse --short HEAD)   # ветка → CI
gh run watch $(gh run list -b ci/$(git rev-parse --short HEAD) -w ci -L1 --json databaseId -q '.[0].databaseId') --exit-status
git push origin HEAD:main                                          # тот же коммит, проверки уже зелёные
```
GitHub пускает коммит в `main`, только если у этого коммита зелёные `check (…)`.

## 2. Бэкап базы (5 минут) — подробно в [backup.md](backup.md)

1. В Neon → **SQL Editor** выполнить SQL из `backup.md` (роль только для чтения; база — на Neon, не в Railway).
2. GitHub → **Settings → Secrets and variables → Actions → New repository secret**:
   - `PROD_DATABASE_URL_READONLY` — адрес базы с этой ролью;
   - `BACKUP_AGE_RECIPIENT` — публичный ключ `age1…` (его даст Татьяна, см. `backup.md`).
3. **Actions → backup → Run workflow** — через 1–2 минуты в прогоне появится артефакт `helper-<дата>.dump.age`.

## 3. Уведомления о падении (2 минуты)

GitHub → правый верх → **Watch → Custom → Issues** — тогда письмо придёт, когда `monitor` или `post-deploy` откроют issue «Прод не отвечает» / «смоук упал». Основной монитор с пушем на телефон — UptimeRobot, см. [monitoring.md](monitoring.md).

## 4. (Необязательно) Sentry через переменные Railway

Вместо `scripts/seal-sentry.sh` можно задать `SENTRY_DSN` в Railway → сервис → **Variables**. Подробно — [monitoring.md](monitoring.md).

## Проверка, что всё включено

- `gh api repos/podlevskikh/helper/rulesets` — есть правило `main`;
- попытка `git push origin <непроверенный коммит>:main` отклоняется с «Required status checks»;
- в **Actions → backup** — зелёный прогон с артефактом.
