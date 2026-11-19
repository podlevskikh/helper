package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/models"
)

// SEC-14: ссылки в приглашениях не строятся из Host / X-Forwarded-* (отравление ссылки сброса пароля).
func TestLinksIgnoreHostHeader(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("host")
	w := e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org,
		body:    map[string]string{"email": "x-" + uniq() + "@test.local", "role": "helper"},
		headers: map[string]string{"Host": "evil.attacker.example", "X-Forwarded-Proto": "https", "X-Forwarded-Host": "evil.attacker.example"}})
	e.expect("приглашение", w, 201)
	if u, _ := e.json(w)["invite_url"].(string); !strings.HasPrefix(u, "https://helper-production-12b0.up.railway.app/app/invite?token=") {
		t.Errorf("ссылка из чужого Host: %s", u)
	}
	w = e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org,
		body: map[string]string{"email": "y-" + uniq() + "@test.local", "role": "helper"}, headers: map[string]string{"Host": "localhost:8345"}})
	if u, _ := e.json(w)["invite_url"].(string); !strings.HasPrefix(u, "http://localhost:8345/app/invite?token=") {
		t.Errorf("локальная ссылка: %s", u)
	}
}

// SEC-08 / PWD-04: после смены пароля и удаления аккаунта старый access-токен не работает.
func TestTokenRevocation(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("tv")
	e.expect("me до смены", e.do(req{method: "GET", path: "/auth/me", token: o.Token}), 200)
	w := e.do(req{method: "POST", path: "/auth/password", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_password": "new-pass-" + uniq()}})
	e.expect("смена пароля", w, 200)
	fresh, _ := e.json(w)["access"].(string)
	e.expect("старый токен после смены пароля", e.do(req{method: "GET", path: "/auth/me", token: o.Token}), 401)
	e.expect("новый токен", e.do(req{method: "GET", path: "/auth/me", token: fresh}), 200)
	o.Token = fresh

	h := e.member(o, "helper", "tv")
	e.expect("удаление помощника", e.do(req{method: "DELETE", path: "/auth/me", token: h.Token, body: map[string]string{"password": h.Password}}), 204)
	e.expect("токен удалённого: /auth/me", e.do(req{method: "GET", path: "/auth/me", token: h.Token}), 401)
	e.expect("токен удалённого: /me/devices", e.do(req{method: "POST", path: "/me/devices", token: h.Token,
		body: map[string]string{"token": "ExponentPushToken[x]", "platform": "ios"}}), 401)
}

// AUTH-07: «нет такого email» и «неверный пароль» неразличимы по ответу.
func TestLoginUniformResponse(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("enum")
	a := e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": "wrong-" + uniq()}})
	b := e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": "nobody-" + uniq() + "@test.local", "password": "wrong"}})
	e.expect("неверный пароль", a, 401)
	e.expect("нет email", b, 401)
	if a.Body.String() != b.Body.String() {
		t.Errorf("ответы различаются: %s / %s", a.Body.String(), b.Body.String())
	}
}

// ROLE-03, ROLE-04, SHOP-05, ROLE-07: помощник не меняет распорядок ребёнка, не удаляет комментарии,
// старых /helper/api/shopping нет, личные задачи нанимателя ему не видны.
func TestHelperRestrictions(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("help")
	h := e.member(o, "helper", "help")
	e.expect("помощник: POST childcare/today", e.do(req{method: "POST", path: "/helper/api/childcare/today", token: h.Token, org: o.Org, body: map[string]any{}}), 403)
	e.expect("помощник: DELETE childcare/today", e.do(req{method: "DELETE", path: "/helper/api/childcare/today", token: h.Token, org: o.Org}), 403)
	e.expect("помощник: DELETE comment", e.do(req{method: "DELETE", path: "/admin/api/comments/1", token: h.Token, org: o.Org}), 403)
	e.expect("наниматель: DELETE несуществующего comment", e.do(req{method: "DELETE", path: "/admin/api/comments/999999999", token: o.Token, org: o.Org}), 404)
	e.expect("старый /helper/api/shopping", e.do(req{method: "GET", path: "/helper/api/shopping", token: h.Token, org: o.Org}), 404)

	today := time.Now().UTC().Format("2006-01-02")
	mk := func(title string, assignee *uint) float64 {
		body := map[string]any{"date": today, "title": title}
		if assignee != nil {
			body["assigned_to_user_id"] = *assignee
		}
		w := e.do(req{method: "POST", path: "/admin/api/custom-tasks", token: o.Token, org: o.Org, body: body})
		e.expect("задача "+title, w, 201)
		id, _ := e.json(w)["id"].(float64)
		return id
	}
	private := mk("личное нанимателя", &o.UserID)
	mine := mk("для помощника", &h.UserID)
	common := mk("общее", nil)
	e.expect("помощник: личная задача нанимателя", e.do(req{method: "GET", path: fmt.Sprintf("/admin/api/tasks/%.0f", private), token: h.Token, org: o.Org}), 404)
	e.expect("помощник: своя задача", e.do(req{method: "GET", path: fmt.Sprintf("/admin/api/tasks/%.0f", mine), token: h.Token, org: o.Org}), 200)
	e.expect("помощник: общая задача", e.do(req{method: "GET", path: fmt.Sprintf("/admin/api/tasks/%.0f", common), token: h.Token, org: o.Org}), 200)
	e.expect("наниматель: личная задача", e.do(req{method: "GET", path: fmt.Sprintf("/admin/api/tasks/%.0f", private), token: o.Token, org: o.Org}), 200)
}

// SEC-05: категория и исполнитель задачи — только из своего дома.
func TestForeignReferencesRejected(t *testing.T) {
	e := newEnv(t, 1000)
	a := e.owner("refA")
	b := e.owner("refB")
	w := e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/task-categories", b.Org), token: b.Token, org: b.Org})
	var foreignCat float64
	if list := e.jsonList(w); len(list) > 0 {
		foreignCat, _ = list[0]["id"].(float64)
	}
	today := time.Now().UTC().Format("2006-01-02")
	e.expect("чужая категория", e.do(req{method: "POST", path: "/admin/api/custom-tasks", token: a.Token, org: a.Org,
		body: map[string]any{"date": today, "title": "x", "task_category_id": foreignCat}}), 400)
	e.expect("чужой исполнитель", e.do(req{method: "POST", path: "/admin/api/custom-tasks", token: a.Token, org: a.Org,
		body: map[string]any{"date": today, "title": "x", "assigned_to_user_id": b.UserID}}), 400)
	e.expect("чужая категория в повторяющейся", e.do(req{method: "POST", path: "/admin/api/recurring-tasks", token: a.Token, org: a.Org,
		body: map[string]any{"title": "x", "weekdays": []int{1}, "task_category_id": foreignCat}}), 400)
}

// DATE-06: upcoming принимает только 1..31 день.
func TestUpcomingBounds(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("up")
	for _, d := range []string{"0", "-1", "32", "10000", "abc"} {
		e.expect("days="+d, e.do(req{method: "GET", path: "/helper/api/schedule/upcoming?days=" + d, token: o.Token, org: o.Org}), 400)
	}
	e.expect("days=7", e.do(req{method: "GET", path: "/helper/api/schedule/upcoming?days=7", token: o.Token, org: o.Org}), 200)
}

// SEC-15: пуш-токен активного устройства другого человека не перехватить.
func TestPushTokenTakeover(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("push")
	h := e.member(o, "helper", "push")
	tok := "ExponentPushToken[" + uniq() + "]"
	dev := map[string]string{"token": tok, "platform": "ios"}
	e.expect("помощник регистрирует", e.do(req{method: "POST", path: "/me/devices", token: h.Token, body: dev}), 200)
	e.expect("чужой перехват", e.do(req{method: "POST", path: "/me/devices", token: o.Token, body: dev}), 409)
	e.expect("помощник отвязывает", e.do(req{method: "DELETE", path: "/me/devices/" + tok, token: h.Token}), 200)
	e.expect("после выхода — можно", e.do(req{method: "POST", path: "/me/devices", token: o.Token, body: dev}), 200)
}

// SEC-16: заголовки безопасности.
func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, 1000)
	w := e.do(req{method: "GET", path: "/auth/me"})
	for k, v := range map[string]string{"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Content-Security-Policy": "frame-ancestors 'none'"} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, ждали %q", k, got, v)
		}
	}
	w = e.do(req{method: "GET", path: "/auth/me", headers: map[string]string{"X-Forwarded-Proto": "https"}})
	if !strings.HasPrefix(w.Header().Get("Strict-Transport-Security"), "max-age=") {
		t.Errorf("нет HSTS за https")
	}
}

// SEC-12: подставной X-Forwarded-For не обходит лимит попыток входа.
func TestRateLimitIgnoresSpoofedXFF(t *testing.T) {
	e := newEnv(t, 1)
	codes := []int{}
	for i := 0; i < 11; i++ {
		w := e.do(req{method: "POST", path: "/auth/login", remote: "203.0.113.7:4321",
			headers: map[string]string{"X-Forwarded-For": fmt.Sprintf("10.9.8.%d", i)},
			body:    map[string]string{"email": "nobody-" + uniq() + "@test.local", "password": "wrong"}})
		codes = append(codes, w.Code)
	}
	if codes[10] != 429 {
		t.Errorf("11-я попытка с подменой X-Forwarded-For: %v, ждали 429", codes)
	}
}

// SEC-12b: за прокси платформы ключ — последний адрес в X-Forwarded-For (его дописывает прокси);
// подмена левых частей не помогает, а разные клиенты не делят один счётчик.
func TestRateLimitBehindProxy(t *testing.T) {
	t.Setenv("TRUST_PROXY", "1")
	e := newEnv(t, 1)
	login := func(xff string) int {
		return e.do(req{method: "POST", path: "/auth/login", remote: "10.1.2.3:5555",
			headers: map[string]string{"X-Forwarded-For": xff},
			body:    map[string]string{"email": "nobody-" + uniq() + "@test.local", "password": "wrong"}}).Code
	}
	var last int
	for i := 0; i < 11; i++ {
		last = login(fmt.Sprintf("6.6.6.%d, 198.51.100.9", i))
	}
	if last != 429 {
		t.Errorf("11-я попытка одного клиента за прокси: %d, ждали 429", last)
	}
	if c := login("198.51.100.10"); c != 401 {
		t.Errorf("другой клиент за тем же прокси: %d, ждали 401 (свой счётчик)", c)
	}
}

// MIG-03: на пустой базе без SEED_OWNER_PASSWORD владелец не получает пароль из кода.
func TestSeedHasNoDefaultPassword(t *testing.T) {
	url := requireDB(t)
	t.Setenv("SEED_OWNER_PASSWORD", "")
	t.Setenv("SEED_OWNER_EMAIL", "")
	admin, err := gorm.Open(postgres.Open(url), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	name := "helper_seedtest_" + uniq()
	if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Skipf("нет прав на CREATE DATABASE: %v", err)
	}
	defer admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	fresh := strings.Replace(url, "/"+dbName(url)+"?", "/"+name+"?", 1)
	if err := database.Initialize(fresh); err != nil {
		t.Fatal(err)
	}
	var n int64
	database.DB.Model(&models.User{}).Where("email = ?", "owner@example.com").Count(&n)
	if n != 1 {
		t.Fatalf("владелец не создан: %d", n)
	}
	var u models.User
	database.DB.Where("email = ?", "owner@example.com").First(&u)
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("changeme123")) == nil {
		t.Error("у сид-владельца пароль из кода")
	}
	if sqlDB, err := database.DB.DB(); err == nil {
		sqlDB.Close()
	}
}

func dbName(url string) string {
	s := url[strings.LastIndex(url, "/")+1:]
	if i := strings.Index(s, "?"); i >= 0 {
		s = s[:i]
	}
	return s
}

// DEL-05: после удаления владельца в базе не остаётся данных его дома — ни в одной таблице с organization_id.
func TestDeleteOwnerLeavesNoOrgData(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("del5")
	h := e.member(o, "helper", "del5")
	today := time.Now().UTC().Format("2006-01-02")
	e.expect("задача", e.do(req{method: "POST", path: "/admin/api/custom-tasks", token: o.Token, org: o.Org, body: map[string]any{"date": today, "title": "x"}}), 201)
	e.expect("зона", e.do(req{method: "POST", path: "/admin/api/zones", token: o.Token, org: o.Org, body: map[string]any{"name": "кухня", "frequency_per_week": 3}}), 201)
	e.expect("список", e.do(req{method: "POST", path: "/helper/api/shopping-lists", token: h.Token, org: o.Org, body: map[string]any{}}), 201)
	e.expect("пуш", e.do(req{method: "POST", path: "/me/devices", token: o.Token, body: map[string]string{"token": "ExponentPushToken[" + uniq() + "]", "platform": "ios"}}), 200)
	e.expect("удаление владельца", e.do(req{method: "DELETE", path: "/auth/me", token: o.Token, body: map[string]string{"password": o.Password}}), 204)

	var tables []string
	e.db.Raw(`SELECT table_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND column_name = 'organization_id'`).Scan(&tables)
	if len(tables) < 10 {
		t.Fatalf("таблиц с organization_id: %d — подозрительно мало", len(tables))
	}
	for _, tbl := range tables {
		var n int64
		e.db.Raw("SELECT count(*) FROM "+tbl+" WHERE organization_id = ?", o.Org).Scan(&n)
		if n != 0 {
			t.Errorf("после удаления дома в %s осталось строк: %d", tbl, n)
		}
	}
	var devices int64
	e.db.Raw("SELECT count(*) FROM push_devices WHERE user_id = ?", o.UserID).Scan(&devices)
	if devices != 0 {
		t.Errorf("остались пуш-устройства владельца: %d", devices)
	}
}
