# Бэкап базы прода и учебное восстановление

База — **Neon** (не Railway: в Railway только сервер). У Neon есть своё восстановление на момент времени
(Restore / history retention: окно зависит от тарифа — на бесплатном часы, на платных дни); посмотреть —
Neon → проект → **Settings → Storage / Instant restore**. Это защита от «испортили данные час назад», но не от
удаления проекта или аккаунта Neon. Поэтому и нужен свой ночной дамп вне Neon. Теперь каждую ночь (01:30 UTC) GitHub Actions (`.github/workflows/backup.yml`):
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

### Владелец Neon: роль только для чтения (3 минуты)
Neon → проект → **SQL Editor** (под владельцем базы — тем же пользователем, что в `DATABASE_URL` сервера в Railway). Пароль придумать длинный, случайный (например, `openssl rand -hex 24`):
```sql
CREATE ROLE helper_backup LOGIN PASSWORD '<случайный пароль>';
ALTER ROLE helper_backup SET default_transaction_read_only = on;
GRANT CONNECT ON DATABASE neondb TO helper_backup;
GRANT USAGE ON SCHEMA public TO helper_backup;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO helper_backup;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO helper_backup;
-- новые таблицы (AutoMigrate создаёт их от владельца базы) — тоже читаемы:
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO helper_backup;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON SEQUENCES TO helper_backup;
```
Если база называется не `neondb` — подставить имя из `DATABASE_URL` (часть после последнего `/`, до `?`). `ALTER DEFAULT PRIVILEGES` выполнять под тем же пользователем, что в `DATABASE_URL` сервиса.
(В Neon роль можно создать и в интерфейсе: **Roles → New role**, но права выше всё равно выдаются SQL.)

Адрес для секрета: Neon → **Connect** → роль `helper_backup`, **без** «Connection pooling» (для `pg_dump` нужен прямой адрес, без `-pooler` в имени хоста):
`postgresql://helper_backup:<пароль>@ep-….neon.tech/neondb?sslmode=require`

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

1. Neon → проект → **Restore** (восстановить ветку на момент до сбоя, в пределах окна тарифа) — быстрее всего.
2. Иначе — из нашего дампа: расшифровать как выше и `pg_restore --clean --if-exists --no-owner -d "<прямой адрес Neon владельца базы>" helper.dump` (перед этим остановить сервис в Railway, после — `smoke.py` на прод).
