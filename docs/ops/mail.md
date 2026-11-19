# Почта (письма сброса пароля, приглашений, смены почты)

Сервер шлёт письма через любой SMTP. Сейчас — **Resend**, домен `helperday.app` подтверждён (регион eu-west-1).

## Основной путь: переменные Railway (так настроено с 19.11.2026)

Railway → проект → сервис → **Variables**:

| Переменная | Значение |
|---|---|
| `SMTP_HOST` | `smtp.resend.com` |
| `SMTP_PORT` | `587` |
| `SMTP_USER` | `resend` |
| `SMTP_PASSWORD` | API-ключ Resend (`re_…`, права *Sending access* на домен `helperday.app`) — вводит владелица сама |
| `MAIL_FROM` | `no-reply@helperday.app` |
| `MAIL_FROM_NAME` | `Helper Day` |
| `PUBLIC_APP_URL` | `https://www.helperday.app/app` — из него строятся ссылки в письмах |

Railway перезапустит сервис. Проверка без пароля и без входа:
```bash
curl -s -X POST https://www.helperday.app/auth/password/forgot -H 'Content-Type: application/json' \
  -d '{"email":"nobody@example.com"}'          # ждём "mail_configured":true
```
Живое письмо: «Забыли пароль?» на свою почту в приложении или смена почты в «Профиле». В Resend → **Emails** видно, что письмо ушло и дошло.

## Как подключали домен (на случай нового домена или пересоздания)

1. resend.com → **Domains → Add domain** → `helperday.app`, регион eu-west-1.
2. Resend покажет DNS-записи (MX и TXT для `send.helperday.app`, TXT `resend._domainkey` для DKIM). В GoDaddy → **My Products → helperday.app → DNS → Add** — каждую запись как есть (в поле *Name* без `.helperday.app` на конце).
3. Рекомендуем добавить DMARC: TXT, имя `_dmarc`, значение `v=DMARC1; p=none; rua=mailto:<ваша почта>`.
4. В Resend → **Verify DNS records** (обычно 5–30 минут).
5. **API Keys → Create** → *Sending access*, домен `helperday.app` → ключ в `SMTP_PASSWORD`.

## Запасной путь: без доступа к Railway

```bash
./scripts/seal-mail.sh            # спросит сервер (по умолчанию smtp.resend.com), логин, ключ, отправителя
git add secrets.sealed.json && git commit -m "chore: почта" && git push   # по регламенту выкатки
```
Значения шифруются ключом прод-сервера; расшифровать их может только он. Переменные Railway важнее файла.

## Если письма не доходят

- `mail_configured:false` — не заданы `SMTP_USER`/`SMTP_PASSWORD`.
- В логах Railway `mail: не удалось отправить …` — неверный ключ или домен не подтверждён в Resend.
- Письма в «Спаме» — проверьте DKIM/DMARC в Resend → Domains.
