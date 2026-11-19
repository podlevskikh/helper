# Бэкап базы прода и учебное восстановление

Railway хранит базу, но собственного бэкапа у нас не было. Теперь каждую ночь (01:30 UTC) GitHub Actions (`.github/workflows/backup.yml`):
1. делает `pg_dump` под ролью **только для чтения** (изменить базу этой ролью нельзя);
2. шифрует дамп ключом [age](https://age-encryption.org) — **публичным**; расшифровать может только владелица закрытого ключа;
3. хранит файл 30 дней как артефакт прогона.

Репозиторий публичный, поэтому в GitHub лежит только зашифрованный файл и публичный ключ. Закрытый ключ в GitHub, Railway и репозиторий **не кладём никогда**.

## Настройка (один раз)

### Татьяна: ключ шифрования (2 минуты)
```bash
brew install age
age-keygen -o ~/helper-backup-key.txt
```
Команда напечатает `Public key: age1…` — эту строку отдать мужу для секрета `BACKUP_AGE_RECIPIENT`.
Файл `~/helper-backup-key.txt` — закрытый ключ: сохранить в менеджер паролей (как заметку) и не пересылать.

### Владелец Railway: роль только для чтения (3 минуты)
Railway → проект → **Postgres → Data → Query** (или `psql` по `DATABASE_PUBLIC_URL`). Пароль придумать длинный, случайный (например, `openssl rand -hex 24`):
```sql
CREATE ROLE helper_backup LOGIN PASSWORD '<случайный пароль>';
ALTER ROLE helper_backup SET default_transaction_read_only = on;
GRANT CONNECT ON DATABASE railway TO helper_backup;
GRANT USAGE ON SCHEMA public TO helper_backup;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO helper_backup;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO helper_backup;
-- новые таблицы (AutoMigrate создаёт их от владельца базы) — тоже читаемы:
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO helper_backup;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON SEQUENCES TO helper_backup;
```
Если база называется не `railway` — подставить имя из `DATABASE_URL` (часть после последнего `/`). `ALTER DEFAULT PRIVILEGES` выполнять под тем же пользователем, что в `DATABASE_URL` сервиса.

Адрес для секрета: взять `DATABASE_PUBLIC_URL` из переменных Postgres в Railway и заменить в нём пользователя и пароль:
`postgresql://helper_backup:<пароль>@<host>.proxy.rlwy.net:<port>/railway`

GitHub → Settings → Secrets and variables → Actions:
- `PROD_DATABASE_URL_READONLY` = этот адрес;
- `BACKUP_AGE_RECIPIENT` = `age1…` от Татьяны.

Затем **Actions → backup → Run workflow** и проверить, что появился артефакт.

## Учебное восстановление (раз в квартал, 10 минут)

Только на своём компьютере, в отдельную базу. Прод не трогаем.
```bash
RUN=$(gh run list -R podlevskikh/helper -w backup -s success -L1 --json databaseId -q '.[0].databaseId')
gh run download $RUN -R podlevskikh/helper -D /tmp/helper-backup
age -d -i ~/helper-backup-key.txt /tmp/helper-backup/*/helper-*.dump.age > /tmp/helper.dump
createdb helper_restore_check
pg_restore --no-owner --no-privileges -d helper_restore_check /tmp/helper.dump
psql -d helper_restore_check -c "SELECT (SELECT count(*) FROM organizations) AS homes, (SELECT count(*) FROM users) AS users, (SELECT max(date) FROM daily_schedules) AS last_day"
```
Ожидание: числа как в приложении, `last_day` — не старше недели вперёд от даты бэкапа. Поднять на этой базе сервер и пройти смоук (`scripts/release-check.sh` с `PRODSIM_TEMPLATE=helper_restore_check`) — так заодно проверяется, что текущая версия стартует на свежих данных прода.
После: `dropdb helper_restore_check && rm -rf /tmp/helper.dump /tmp/helper-backup`. Строка в `docs/qa/runs.md` фронта: «учебное восстановление, бэкап от <даты>, ок».

## Восстановление прода (авария)

1. Railway → Postgres → **Backups** (если включены) — быстрее всего.
2. Иначе — из нашего дампа: расшифровать как выше и `pg_restore --clean --if-exists --no-owner -d "$DATABASE_PUBLIC_URL" helper.dump` (делает владелец Railway; перед этим остановить сервис, после — `smoke.py` на прод).
