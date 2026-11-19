# Мониторинг прода

| Что | Где | Кто узнаёт |
|---|---|---|
| Сервер не отвечает или база недоступна (`/healthz` ≠ 200) | UptimeRobot, каждые 5 минут | Татьяна — пуш в приложении UptimeRobot и письмо |
| То же, страховка | GitHub Actions `monitor.yml`, каждые 15 минут | issue `prod-down` → письмо тем, кто следит за репозиторием |
| Выкатка сломала публичное поведение | GitHub Actions `post-deploy.yml` после каждого пуша в `main` | issue `prod-smoke` |
| Ошибки 5xx и паники сервера | Sentry (если задан `SENTRY_DSN`) | письмо Sentry |

## UptimeRobot (Татьяна, 5 минут, бесплатно)

1. https://uptimerobot.com → **Register for FREE** (своя почта).
2. **+ New monitor**:
   - Monitor type: **HTTP(s)**;
   - Friendly name: `Helper прод`;
   - URL: `https://helper-production-12b0.up.railway.app/healthz`;
   - Monitoring interval: **5 minutes**.
3. Ниже **Advanced settings → Keyword** (если доступно): тип *Keyword exists*, слово `ok` — так монитор поймёт и случай «страница открылась, но база недоступна» (тогда ответ 503 `db_unavailable`).
4. **Integrations & Team** → включить уведомления на почту и поставить мобильное приложение UptimeRobot (пуш на телефон).
5. **Create monitor**. Через 5 минут статус — **Up**.

Пароли от прода сюда не нужны: `/healthz` открыт без входа и ничего, кроме «ok», не показывает.

## Sentry (кто-то из вас, 10 минут, бесплатный план)

1. https://sentry.io → регистрация → **Create project** → платформа **Go** → имя `helper-backend`.
2. Скопировать **DSN** (Settings → Projects → helper-backend → Client Keys).
3. Включить на проде — любой из способов:
   - без доступа к Railway: в репозитории бэкенда `./scripts/seal-sentry.sh` → вставить DSN → `git add secrets.sealed.json && git commit -m "chore: SENTRY_DSN" && git push` (по обычному регламенту выкатки);
   - с доступом к Railway: сервис → **Variables** → `SENTRY_DSN` = DSN.
4. После выкатки в логе Railway — строка `sentry: включён (окружение production)`.

Что уходит в Sentry: паники и ответы 5xx — метод, шаблон маршрута (`/admin/api/tasks/:id`), код, коммит.
Что **не** уходит: тела запросов, строка запроса (в ней бывают токены ссылок), заголовки `Authorization`/`Cookie`, IP-адреса, почты (`internal/observability`, тесты OBS-01/02).
