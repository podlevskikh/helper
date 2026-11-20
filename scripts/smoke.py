#!/usr/bin/env python3
"""Смоук-тест Helper Day через API — без браузера и без зависимостей.

    python3 scripts/smoke.py http://localhost:8340 [--mail-log server.log] [--quiet]
    python3 scripts/smoke.py https://www.helperday.app [--expect-sha <sha>] [--quiet]

Локально (localhost / *.test): полный прогон — одноразовые учётки *@test.local, ключевые сценарии,
удаление учёток в конце (даже при падении). Сервер для полного прогона запускать с RATE_LIMIT_SCALE=100,
иначе повторные прогоны упрутся в лимит попыток (scripts/release-check.sh делает это сам).
--mail-log: путь к логу сервера с MAIL_DEV_LOG=1 — тогда проверяется весь сценарий сброса пароля.

На любом другом адресе — только проверки без входа: пароли на проде не вводим.
--expect-sha: сначала дождаться, пока /version отдаст этот коммит (до 6 минут), затем проверять.

Коды кейсов (AUTH-01 и т.п.) — из реестра docs/qa/test-cases.md во фронте.
Выход: 0 — всё прошло, 1 — есть провалы.
"""
import argparse
import json
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timezone

ap = argparse.ArgumentParser()
ap.add_argument("base", nargs="?", default="http://localhost:8080")
ap.add_argument("--quiet", action="store_true", help="печатать только провалы и итог")
ap.add_argument("--mail-log", help="лог сервера с MAIL_DEV_LOG=1 (локально)")
ap.add_argument("--expect-sha", help="ждать этот коммит в /version")
args = ap.parse_args()

BASE = args.base.rstrip("/")
HOST = urllib.parse.urlparse(BASE).hostname or ""
LOCAL = HOST in ("localhost", "127.0.0.1", "::1") or HOST.endswith(".localhost") or HOST.endswith(".test")

fails: list[str] = []
passed = 0


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


opener = urllib.request.build_opener(NoRedirect)


def call(method, path, body=None, token=None, org=None, headers=None):
    req = urllib.request.Request(BASE + path, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    if org:
        req.add_header("X-Org-Id", str(org))
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    data = json.dumps(body).encode() if body is not None else None
    try:
        with opener.open(req, data, timeout=20) as r:
            status, raw, hdrs = r.status, r.read(), r.headers
    except urllib.error.HTTPError as e:
        status, raw, hdrs = e.code, e.read(), e.headers
    try:
        js = json.loads(raw) if raw else None
    except ValueError:
        js = None
    return status, js, hdrs


def check(case, ok, detail=""):
    global passed
    if ok:
        passed += 1
        if not args.quiet:
            print(f"  ✓ {case}")
    else:
        fails.append(f"{case} {detail}".strip())
        print(f"  ✗ {case} {detail}")


def expect(case, got, want, extra=""):
    check(case, got == want, f"(получили {got}, ждали {want}) {extra}".strip())


def section(name):
    if not args.quiet:
        print(f"\n{name}")


def sha_matches(sha, want):
    return bool(sha) and bool(want) and (sha.startswith(want) or want.startswith(sha))


# ── Ожидание нужной версии на проде ─────────────────────────────────────────
if args.expect_sha:
    deadline, sha = time.time() + 360, None
    while time.time() < deadline:
        s, js, _ = call("GET", "/version")
        sha = (js or {}).get("sha") if s == 200 else None
        if sha_matches(sha, args.expect_sha):
            break
        time.sleep(10)
    check("PUB-04 /version отдаёт выкаченный коммит", sha_matches(sha, args.expect_sha), f"(отдаёт {sha}, ждали {args.expect_sha})")

# ── Публичные проверки (и прод, и локально) ────────────────────────────────
print(f"Смоук {BASE} — {'полный' if LOCAL else 'только публичный'}")
section("Публичные страницы")
for path, want in [("/app/", 200), ("/app/login", 200), ("/app/delete-account", 200),
                   ("/app/privacy", 200), ("/app/support", 200), ("/app/no-such-page", 404)]:
    s, _, _ = call("GET", path)
    expect(f"PUB-01 GET {path}", s, want)
for path in ["/", "/admin2/", "/helper2/"]:
    s, _, h = call("GET", path)
    check(f"PUB-02 {path} → /app/", s == 302 and (h.get("Location") or "").endswith("/app/"), f"({s} {h.get('Location')})")
for path, ctype in [("/app/favicon.ico", "image/"), ("/app/site-icon.png", "image/png"), ("/app/site-touch-icon.png", "image/png")]:
    s, _, h = call("GET", path)
    check(f"PUB-07 иконка {path}", s == 200 and (h.get("Content-Type") or "").startswith(ctype), f"({s} {h.get('Content-Type')})")
s, js, _ = call("GET", "/healthz")
check("PUB-05 /healthz", s == 200 and (js or {}).get("status") == "ok", f"({s} {js})")
s, _, _ = call("DELETE", "/auth/me")
expect("PUB-03 DELETE /auth/me без входа", s, 401)
s, _, _ = call("GET", "/admin/api/recipes")
expect("PUB-03 /admin/api без входа", s, 401)
s, _, h = call("GET", "/app/login")
check("SEC-16 заголовки безопасности", h.get("X-Frame-Options") == "DENY" and h.get("X-Content-Type-Options") == "nosniff",
      f"({h.get('X-Frame-Options')}, {h.get('X-Content-Type-Options')})")
s, _, h = call("OPTIONS", "/auth/login", headers={"Origin": "https://evil.example", "Access-Control-Request-Method": "POST"})
check("SEC-10 CORS: чужой Origin не разрешён", h.get("Access-Control-Allow-Origin") is None, f"({h.get('Access-Control-Allow-Origin')})")

if not LOCAL:
    print(f"\nИтог: {passed} ✓, {len(fails)} ✗ (полный прогон — только на локальном адресе)")
    for f in fails:
        print("  ✗", f)
    sys.exit(1 if fails else 0)

# ── Полный прогон (только локально) ────────────────────────────────────────
RUN = uuid.uuid4().hex[:10]
PW = "smoke-pass-" + RUN
today = datetime.now(timezone.utc).strftime("%Y-%m-%d")
created: dict[str, tuple[str, str]] = {}  # тег → (токен, пароль): удалить в конце


def email(tag):
    return f"smoke-{RUN}-{tag}@test.local"


def register(tag):
    s, js, _ = call("POST", "/auth/register", {"email": email(tag), "password": PW, "org_name": f"Smoke {tag}", "locale": "ru"})
    org = next((m["organization_id"] for m in (js or {}).get("memberships", []) if m.get("status") == "active"), None)
    tok = (js or {}).get("access")
    if tok:
        created[tag] = (tok, PW)
    return s, tok, org


def log_len():
    try:
        with open(args.mail_log, encoding="utf-8", errors="replace") as f:
            return len(f.read().splitlines())
    except (OSError, TypeError):
        return 0


def mail_link(kind, after_lines):
    """Последняя ссылка вида …/<kind>?token=… из лога сервера (MAIL_DEV_LOG=1)."""
    for _ in range(20):
        try:
            with open(args.mail_log, encoding="utf-8", errors="replace") as f:
                text = "\n".join(f.read().splitlines()[after_lines:])
        except (OSError, TypeError):
            return None
        m = re.findall(r"https?://\S+/" + kind + r"\?token=[0-9a-f]+", text)
        if m:
            return m[-1]
        time.sleep(0.3)
    return None


try:
    section("Наниматель A")
    s, tokA, orgA = register("a")
    expect("AUTH-01 регистрация", s, 201)
    if not tokA:
        raise SystemExit("Дальше без токена не пройти.")
    s, js, _ = call("GET", f"/orgs/{orgA}/task-categories", token=tokA, org=orgA)
    keys = sorted((c.get("system_key") or "") for c in (js or []))
    check("CAT-01 у нового дома ровно 4 системные категории", s == 200 and keys == ["childcare", "cleaning", "custom", "meal"], f"({s}, {keys})")
    s1, js1, _ = call("POST", "/auth/login", {"email": email("a"), "password": "wrong-pass"})
    s2, js2, _ = call("POST", "/auth/login", {"email": email("nobody"), "password": "wrong-pass"})
    check("AUTH-03 неверный пароль и чужой email — одинаковый 401", s1 == 401 and s2 == 401 and js1 == js2, f"({s1} {js1} / {s2} {js2})")
    s, js, _ = call("POST", "/admin/api/custom-tasks", {"date": today, "title": "Smoke задача", "time": "10:00", "priority": "high"}, tokA, orgA)
    expect("TASK-01 разовая задача", s, 201)
    taskA = (js or {}).get("id")
    s, _, _ = call("GET", f"/admin/api/tasks/{taskA}", token=tokA, org=orgA)
    expect("TASK-02 задача открывается по ссылке", s, 200)
    weekday = datetime.now(timezone.utc).isoweekday()
    other = 1 if weekday != 1 else 2
    s, _, _ = call("POST", "/admin/api/recurring-tasks", {"title": "Smoke сегодняшний день недели", "time": "09:00", "weekdays": [weekday], "start_date": today}, tokA, orgA)
    expect("RECUR-01 повторяющаяся задача", s, 201)
    s, _, _ = call("POST", "/admin/api/recurring-tasks", {"title": "Smoke другой день", "weekdays": [other], "start_date": today}, tokA, orgA)
    expect("RECUR-01 повторяющаяся на другой день", s, 201)
    s, _, _ = call("POST", "/admin/api/mealtimes", {"name": "Smoke перекус", "default_time": "16:00", "family_member": "all"}, tokA, orgA)
    expect("MEAL-02 приём пищи", s, 201)
    s, _, _ = call("POST", "/admin/api/zones", {"name": "Smoke кухня", "frequency_per_week": 7, "priority": "high"}, tokA, orgA)
    expect("ZONE-01 зона уборки", s, 201)

    section("Приглашение и помощник")
    s, js, _ = call("POST", f"/orgs/{orgA}/invites", {"email": email("h"), "role": "helper"}, tokA, orgA)
    expect("INV-01 приглашение", s, 201)
    inv = js or {}
    check("INV-01 ссылка ведёт в приложение", "/invite?token=" in inv.get("invite_url", ""), inv.get("invite_url", ""))
    s, _, _ = call("POST", f"/orgs/{orgA}/invites", {"email": email("x"), "role": "owner"}, tokA, orgA)
    expect("INV-05 приглашение с ролью owner запрещено", s, 400)
    s, js, _ = call("POST", f"/invites/{inv.get('token')}/accept", {"name": "Smoke Помощник", "password": PW, "email": email("h")})
    expect("INV-02 приглашение принято", s, 200)
    tokH = (js or {}).get("access")
    if tokH:
        created["h"] = (tokH, PW)
    s, _, _ = call("POST", f"/invites/{inv.get('token')}/accept", {"name": "ещё раз", "password": PW, "email": email("h")})
    expect("INV-05 повторное принятие", s, 410)
    s, js, _ = call("GET", "/helper/api/schedule/today", token=tokH, org=orgA)
    titles = [t.get("title") for t in (js or {}).get("tasks", [])]
    check("DAY-01 помощник видит задачи дня (и только своего дня недели)", s == 200 and "Smoke задача" in titles
          and "Smoke сегодняшний день недели" in titles and "Smoke другой день" not in titles, f"({s} {titles})")
    s, _, _ = call("POST", f"/helper/api/tasks/{taskA}/complete", {}, tokH, orgA)
    expect("DAY-02 отметить выполнение", s, 200)
    s, _, _ = call("POST", f"/helper/api/tasks/{taskA}/uncomplete", {}, tokH, orgA)
    expect("DAY-02 снять отметку", s, 200)
    for case, method, path, body in [
        ("ROLE-01 помощник не создаёт задачи", "POST", "/admin/api/custom-tasks", {"date": today, "title": "нельзя"}),
        ("ROLE-01 помощник не удаляет задачи", "DELETE", f"/admin/api/custom-tasks/{taskA}", None),
        ("ROLE-01 помощник не меняет приёмы пищи", "POST", "/admin/api/mealtimes", {"name": "нельзя", "default_time": "10:00"}),
        ("ROLE-01 помощник не заводит повторяющиеся", "POST", "/admin/api/recurring-tasks", {"title": "нельзя", "weekdays": [1]}),
        ("ROLE-03 помощник не меняет распорядок ребёнка", "POST", "/helper/api/childcare/today", {}),
    ]:
        s, _, _ = call(method, path, body, tokH, orgA)
        expect(case, s, 403)

    section("Покупки")
    s, js, _ = call("POST", "/helper/api/shopping-lists", {}, tokH, orgA)
    expect("SHOP-01 черновик списка", s, 201)
    lid = (js or {}).get("id")
    for it in ("молоко", "хлеб"):
        s, _, _ = call("POST", f"/helper/api/shopping-lists/{lid}/items", {"item": it}, tokH, orgA)
        expect(f"SHOP-01 пункт «{it}»", s, 201)
    s, _, _ = call("POST", f"/helper/api/shopping-lists/{lid}/send", {}, tokH, orgA)
    expect("SHOP-01 отправить нанимателю", s, 200)
    s, js, _ = call("GET", f"/helper/api/shopping-lists/{lid}", token=tokA, org=orgA)
    items = (js or {}).get("items") or []
    check("SHOP-02 наниматель видит отправленный список", s == 200 and (js or {}).get("status") == "open" and len(items) == 2,
          f"({s} {(js or {}).get('status')} {len(items)})")
    if items:
        s, _, _ = call("PATCH", f"/helper/api/shopping-lists/{lid}/items/{items[0]['id']}", {"purchased": True}, tokA, orgA)
        expect("SHOP-02 «в корзине»", s, 200)
    s, _, _ = call("POST", f"/helper/api/shopping-lists/{lid}/close", {}, tokA, orgA)
    expect("SHOP-04 закрыть список", s, 200)
    s, js, _ = call("GET", "/helper/api/shopping-lists", token=tokA, org=orgA)
    lists = js if isinstance(js, list) else []
    carried = [l for l in lists if l.get("parent_list_id") == lid]
    check("SHOP-04 некупленное — в новом списке", len(carried) == 1 and carried[0].get("total") == 1,
          f"({[(l.get('id'), l.get('parent_list_id'), l.get('total')) for l in lists]})")

    section("Изоляция домов")
    s, tokB, orgB = register("b")
    expect("AUTH-01 регистрация второго дома", s, 201)
    s, _, _ = call("GET", f"/admin/api/tasks/{taskA}", token=tokB, org=orgB)
    expect("SEC-01 чужая задача по id", s, 404)
    s, _, _ = call("GET", "/helper/api/schedule/today", token=tokB, org=orgA)
    expect("SEC-02 чужой дом через X-Org-Id", s, 403)
    s, _, _ = call("GET", f"/orgs/{orgA}/settings", token=tokB, org=orgB)
    expect("SEC-06 чужой дом в адресе", s, 403)
    # В доме A есть свой приём пищи и зона — им есть куда «протечь». У дома B — только приёмы пищи по умолчанию.
    s, _, _ = call("POST", "/admin/api/regenerate-schedule", {}, tokA, orgA)
    expect("SCHED-04 пересборка дней своего дома", s, 200)
    call("POST", "/admin/api/regenerate-schedule", {}, tokB, orgB)
    s, js, _ = call("GET", "/helper/api/schedule/today", token=tokB, org=orgB)
    tasksB = (js or {}).get("tasks", [])
    titlesB = [t.get("title") or "" for t in tasksB]
    _, jsA, _ = call("GET", "/helper/api/schedule/today", token=tokA, org=orgA)
    tasksA = (jsA or {}).get("tasks", [])
    titlesA = [t.get("title") or "" for t in tasksA]
    shared_ids = {t.get("id") for t in tasksA} & {t.get("id") for t in tasksB}
    check("SEC-03 в расписании B нет задач, еды и уборки A, у A они есть",
          s == 200 and not shared_ids and not any("smoke" in t.lower() for t in titlesB)
          and any("кухня" in t.lower() for t in titlesA) and any("перекус" in t.lower() for t in titlesA),
          f"(B={titlesB}, A={titlesA}, общие id={shared_ids})")
    s, js, _ = call("GET", "/admin/api/mealtimes", token=tokB, org=orgB)
    check("MEAL-01 у нового дома завтрак, обед и ужин", s == 200 and len(js or []) == 3, f"({s} {js})")

    section("Почта")
    s, _, _ = call("POST", "/auth/email", {"current_password": "wrong-pass", "new_email": email("b2")}, tokB)
    expect("AUTH-EMAIL-01 смена почты с неверным паролем", s, 403)
    s, js_free, _ = call("POST", "/auth/email", {"current_password": PW, "new_email": email("b-free")}, tokB)
    s2, js_busy, _ = call("POST", "/auth/email", {"current_password": PW, "new_email": email("a")}, tokB)
    check("AUTH-EMAIL-04 занятый адрес отвечает так же, как свободный", s == s2 == 202, f"({s} / {s2})")
    mark = log_len()
    s, js, _ = call("POST", "/auth/email", {"current_password": PW, "new_email": email("b2")}, tokB)
    expect("AUTH-EMAIL-01 запрос смены почты", s, 202)
    s, js, _ = call("GET", "/auth/email", token=tokB)
    check("AUTH-EMAIL-02 «ожидает подтверждения»", s == 200 and ((js or {}).get("pending") or {}).get("email") == email("b2"), f"({s} {js})")
    s, _, _ = call("POST", "/auth/login", {"email": email("b"), "password": PW})
    expect("AUTH-EMAIL-02 до подтверждения вход по старой почте", s, 200)
    if args.mail_log:
        link = mail_link("confirm-email", mark)
        check("AUTH-EMAIL-01 ссылка подтверждения в письме", bool(link), "(в логе нет ссылки — сервер запущен с MAIL_DEV_LOG=1?)")
        cancel = mail_link("cancel-email", mark)
        check("AUTH-EMAIL-03 ссылка «это не я» в письме на старый адрес", bool(cancel))
        if link:
            token = link.split("token=")[1]
            s, _, _ = call("POST", "/auth/email/check", {"token": token})
            expect("AUTH-EMAIL-05 ссылка проверяется до действий", s, 200)
            s, js, _ = call("POST", "/auth/email/confirm", {"token": token})
            expect("AUTH-EMAIL-01 подтверждение по ссылке", s, 200)
            s, _, _ = call("POST", "/auth/email/confirm", {"token": token})
            expect("AUTH-EMAIL-05 ссылка одноразовая", s, 410)
            s, js, _ = call("POST", "/auth/login", {"email": email("b2"), "password": PW})
            expect("AUTH-EMAIL-01 вход по новой почте", s, 200)
            s, _, _ = call("POST", "/auth/login", {"email": email("b"), "password": PW})
            expect("AUTH-EMAIL-01 старая почта не работает", s, 401)
            s, _, _ = call("GET", "/auth/me", token=tokB)
            expect("AUTH-EMAIL-01 старые сессии завершены", s, 401)
            if (js or {}).get("access"):
                tokB = js["access"]
                created["b"] = (tokB, PW)
    elif not args.quiet:
        print("  · AUTH-EMAIL полный сценарий пропущен: нужен --mail-log")

    section("Пароль")
    mark = log_len()
    s, _, _ = call("POST", "/auth/password/forgot", {"email": email("a")})
    expect("PWD-01 «Забыли пароль?»", s, 200)
    if args.mail_log:
        link = mail_link("reset-password", mark)
        check("PWD-01 ссылка в письме", bool(link), "(в логе нет ссылки — сервер запущен с MAIL_DEV_LOG=1?)")
        if link:
            token = link.split("token=")[1]
            newpw = "smoke-new-" + RUN
            s, js, _ = call("POST", "/auth/password/reset", {"token": token, "password": newpw})
            expect("PWD-01 новый пароль по ссылке", s, 200)
            s, _, _ = call("GET", "/auth/me", token=tokA)
            expect("PWD-04 старый токен после сброса", s, 401)
            s, _, _ = call("POST", "/auth/password/reset", {"token": token, "password": "again-" + RUN})
            expect("PWD-02 ссылка одноразовая", s, 410)
            if (js or {}).get("access"):
                created["a"] = (js["access"], newpw)
    elif not args.quiet:
        print("  · PWD-01 полный сценарий пропущен: нужен --mail-log")

    section("Удаление аккаунтов")
    s, _, _ = call("DELETE", "/auth/me", {"password": "wrong-pass"}, tokH)
    expect("DEL-03 неверный пароль при удалении", s, 403)
    s, _, _ = call("DELETE", "/auth/me", {"password": PW}, tokH)
    expect("DEL-02 удаление помощника", s, 204)
    s, _, _ = call("POST", "/auth/login", {"email": email("h"), "password": PW})
    expect("DEL-02 после удаления вход невозможен", s, 401)
    s, _, _ = call("GET", "/auth/me", token=tokH)
    expect("SEC-08 токен удалённого не работает", s, 401)
    created.pop("h", None)
finally:
    for tag, (tok, pw) in created.items():  # уборка даже при падении
        s, _, _ = call("DELETE", "/auth/me", {"password": pw}, tok)
        if s != 204:
            print(f"  ! не удалось удалить учётку {tag}: {s}")

print(f"\nИтог: {passed} ✓, {len(fails)} ✗")
for f in fails:
    print("  ✗", f)
sys.exit(1 if fails else 0)
