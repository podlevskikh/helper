#!/usr/bin/env python3
"""Смоук-тест Helper Day через API — без браузера и без зависимостей.

    python3 scripts/smoke.py http://localhost:8300          # полный прогон (локально)
    python3 scripts/smoke.py https://helper-production-12b0.up.railway.app   # только публичные проверки

Полный прогон заводит одноразовые учётки *@test.local, проходит ключевые сценарии и удаляет их.
На не-локальном адресе выполняются только проверки без входа: пароли на проде не вводим.
Коды кейсов (AUTH-01 и т.п.) — из реестра docs/qa/test-cases.md во фронте.
Выход: 0 — всё прошло, 1 — есть провалы.
"""
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080").rstrip("/")
HOST = urllib.parse.urlparse(BASE).hostname or ""
LOCAL = HOST in ("localhost", "127.0.0.1", "::1") or HOST.endswith(".localhost") or HOST.endswith(".test")

fails: list[str] = []
passed = 0


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


opener = urllib.request.build_opener(NoRedirect)


def call(method, path, body=None, token=None, org=None):
    req = urllib.request.Request(BASE + path, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    if org:
        req.add_header("X-Org-Id", str(org))
    data = json.dumps(body).encode() if body is not None else None
    try:
        with opener.open(req, data, timeout=20) as r:
            status, raw, headers = r.status, r.read(), r.headers
    except urllib.error.HTTPError as e:
        status, raw, headers = e.code, e.read(), e.headers
    try:
        js = json.loads(raw) if raw else None
    except ValueError:
        js = None
    return status, js, headers


def check(case, ok, detail=""):
    global passed
    if ok:
        passed += 1
        print(f"  ✓ {case}")
    else:
        fails.append(f"{case} {detail}".strip())
        print(f"  ✗ {case} {detail}")


def expect(case, got, *want, extra=""):
    check(case, got in want, f"(получили {got}, ждали {'/'.join(map(str, want))}) {extra}".strip())


# ── Публичные проверки (и прод, и локально) ────────────────────────────────
print(f"Смоук {BASE} — {'полный' if LOCAL else 'только публичный'}")
for path, want in [("/app/", 200), ("/app/login", 200), ("/app/delete-account", 200),
                   ("/app/privacy", 200), ("/app/support", 200), ("/app/no-such-page", 404)]:
    s, _, _ = call("GET", path)
    expect(f"PUB-01 GET {path}", s, want)
for path in ["/", "/admin2/", "/helper2/"]:
    s, _, h = call("GET", path)
    check(f"PUB-02 {path} → /app/", s in (301, 302) and "/app/" in (h.get("Location") or ""), f"({s} {h.get('Location')})")
s, _, _ = call("DELETE", "/auth/me")
expect("PUB-03 DELETE /auth/me без входа", s, 401)
s, _, _ = call("GET", "/admin/api/recipes")
expect("PUB-03 /admin/api без входа", s, 401)

if not LOCAL:
    print(f"\nИтог: {passed} ✓, {len(fails)} ✗ (полный прогон — только на локальном адресе)")
    sys.exit(1 if fails else 0)

# ── Полный прогон (только локально) ────────────────────────────────────────
stamp = int(time.time())
PW = "smoke-pass-123"
today = datetime.now(timezone.utc).strftime("%Y-%m-%d")


def register(tag):
    s, js, _ = call("POST", "/auth/register", {"email": f"smoke-{stamp}-{tag}@test.local", "password": PW,
                                                "org_name": f"Smoke {tag}", "locale": "ru"})
    org = next((m["organization_id"] for m in (js or {}).get("memberships", []) if m.get("status") == "active"), None)
    return s, (js or {}).get("access"), org


print("\nНаниматель A")
s, tokA, orgA = register("a")
expect("AUTH-01 регистрация", s, 201)
if not tokA:
    print("Дальше без токена не пройти."); sys.exit(1)
s, js, _ = call("GET", f"/orgs/{orgA}/task-categories", token=tokA, org=orgA)
check("CAT-01 у нового дома есть категории", s == 200 and isinstance(js, list) and len(js) >= 4, f"({s}, {len(js or [])})")
s, _, _ = call("POST", "/auth/login", {"email": f"smoke-{stamp}-a@test.local", "password": "wrong-pass"})
expect("AUTH-03 неверный пароль", s, 401)
s, js, _ = call("POST", "/admin/api/custom-tasks", {"date": today, "title": "Smoke задача", "time": "10:00",
                                                    "priority": "high"}, tokA, orgA)
expect("TASK-01 разовая задача", s, 200, 201)
taskA = (js or {}).get("id")
s, _, _ = call("GET", f"/admin/api/tasks/{taskA}", token=tokA, org=orgA)
expect("TASK-02 задача открывается по ссылке", s, 200)
s, js, _ = call("POST", "/admin/api/recurring-tasks", {"title": "Smoke каждый день", "time": "09:00",
                                                       "weekdays": [1, 2, 3, 4, 5, 6, 7], "start_date": today}, tokA, orgA)
expect("RECUR-01 повторяющаяся задача", s, 200, 201)

print("\nПриглашение и помощник")
s, js, _ = call("POST", f"/orgs/{orgA}/invites", {"email": f"smoke-{stamp}-h@test.local", "role": "helper"}, tokA, orgA)
expect("INV-01 приглашение", s, 200, 201)
inv = js or {}
check("INV-01 ссылка ведёт в приложение", "/invite?token=" in inv.get("invite_url", ""), inv.get("invite_url", ""))
s, js, _ = call("POST", f"/invites/{inv.get('token')}/accept", {"name": "Smoke Помощник", "password": PW,
                                                               "email": f"smoke-{stamp}-h@test.local"})
expect("INV-02 приглашение принято", s, 200, 201)
tokH = (js or {}).get("access")
s, js, _ = call("GET", "/helper/api/schedule/today", token=tokH, org=orgA)
titles = [t.get("title") for t in (js or {}).get("tasks", [])]
check("DAY-01 помощник видит задачи дня", s == 200 and "Smoke задача" in titles and "Smoke каждый день" in titles, f"({s} {titles})")
s, _, _ = call("POST", f"/helper/api/tasks/{taskA}/complete", {}, tokH, orgA)
expect("DAY-02 отметить выполнение", s, 200)
s, _, _ = call("POST", f"/helper/api/tasks/{taskA}/uncomplete", {}, tokH, orgA)
expect("DAY-02 снять отметку", s, 200)
s, _, _ = call("POST", "/admin/api/custom-tasks", {"date": today, "title": "нельзя"}, tokH, orgA)
expect("ROLE-01 помощник не создаёт задачи", s, 403)
s, _, _ = call("DELETE", f"/admin/api/custom-tasks/{taskA}", token=tokH, org=orgA)
expect("ROLE-01 помощник не удаляет задачи", s, 403)
s, _, _ = call("POST", "/admin/api/mealtimes", {"name": "нельзя", "default_time": "10:00"}, tokH, orgA)
expect("ROLE-01 помощник не меняет приёмы пищи", s, 403)
s, _, _ = call("POST", "/admin/api/recurring-tasks", {"title": "нельзя", "weekdays": [1]}, tokH, orgA)
expect("ROLE-01 помощник не заводит повторяющиеся", s, 403)
s, _, _ = call("GET", "/helper/api/shopping-lists", token=tokH, org=orgA)
expect("SHOP-01 списки покупок открываются", s, 200)

print("\nИзоляция домов")
s, tokB, orgB = register("b")
expect("AUTH-01 регистрация второго дома", s, 201)
s, _, _ = call("GET", f"/admin/api/tasks/{taskA}", token=tokB, org=orgB)
expect("SEC-01 чужая задача по id недоступна", s, 403, 404)
s, _, _ = call("GET", "/helper/api/schedule/today", token=tokB, org=orgA)
expect("SEC-02 чужой дом через X-Org-Id", s, 403, 404)
s, js, _ = call("GET", "/helper/api/schedule/today", token=tokB, org=orgB)
titlesB = [t.get("title") for t in (js or {}).get("tasks", [])]
check("SEC-03 в расписании B нет задач A", s == 200 and not any(t and t.startswith("Smoke") for t in titlesB), f"({titlesB})")

print("\nПароль")
s, js, _ = call("POST", "/auth/password/forgot", {"email": f"smoke-{stamp}-a@test.local"})
expect("PWD-01 «Забыли пароль?»", s, 200)

print("\nУдаление аккаунтов")
s, _, _ = call("DELETE", "/auth/me", {"password": "wrong-pass"}, tokH)
expect("DEL-03 неверный пароль при удалении", s, 401, 403)
s, _, _ = call("DELETE", "/auth/me", {"password": PW}, tokH)
expect("DEL-02 удаление помощника", s, 200, 204)
s, _, _ = call("POST", "/auth/login", {"email": f"smoke-{stamp}-h@test.local", "password": PW})
expect("DEL-02 после удаления вход невозможен", s, 401)
for tag, tok in (("a", tokA), ("b", tokB)):
    s, _, _ = call("DELETE", "/auth/me", {"password": PW}, tok)
    expect(f"DEL-01 удаление нанимателя {tag}", s, 200, 204)
s, _, _ = call("GET", f"/admin/api/tasks/{taskA}", token=tokA, org=orgA)
expect("DEL-01 старый токен не работает", s, 401, 403, 404)

print(f"\nИтог: {passed} ✓, {len(fails)} ✗")
for f in fails:
    print("  ✗", f)
sys.exit(1 if fails else 0)
