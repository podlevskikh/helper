package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/scheduler"
)

// Интеграционные тесты на настоящем роутере (с правами, лимитами, заголовками).
// Нужна база: TEST_DATABASE_URL=postgres://… (scripts/check.sh создаёт временную сам).
// REQUIRE_DB=1 — без базы тест падает, а не пропускается (так в check.sh и CI).

type env struct {
	t  *testing.T
	r  *gin.Engine
	db *gorm.DB
}

type acct struct {
	Token    string
	Refresh  string
	UserID   uint
	Org      uint
	Email    string
	Password string
}

func requireDB(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB") == "1" {
			t.Fatal("TEST_DATABASE_URL не задан, а REQUIRE_DB=1")
		}
		t.Skip("TEST_DATABASE_URL не задан")
	}
	return url
}

// newEnv поднимает роутер на тестовой базе. scale — множитель лимитов попыток (1 — как на проде).
func newEnv(t *testing.T, scale int) *env {
	t.Helper()
	url := requireDB(t)
	t.Setenv("SMTP_USER", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("WEB_ORIGIN", "")
	t.Setenv("PUBLIC_APP_URL", "")
	t.Setenv("RATE_LIMIT_SCALE", fmt.Sprint(scale))
	t.Chdir("../..") // шаблоны web*/templates и webapp/ — от корня репозитория
	if err := database.Initialize(url); err != nil {
		t.Fatal(err)
	}
	// Каждый тест открывает свой пул — закрываем его, иначе сотня тестов упирается в max_connections Postgres.
	db := database.DB
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	gin.SetMode(gin.TestMode)
	return &env{t: t, db: database.DB, r: NewRouter(Deps{DB: database.DB, Sched: scheduler.NewScheduler(database.DB)})}
}

func uniq() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type req struct {
	method, path, token string
	org                 uint
	body                any
	headers             map[string]string
	remote              string
}

func (e *env) do(r req) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf *bytes.Reader
	if r.body != nil {
		b, _ := json.Marshal(r.body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	hr := httptest.NewRequest(r.method, r.path, buf)
	hr.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		hr.Header.Set("Authorization", "Bearer "+r.token)
	}
	if r.org != 0 {
		hr.Header.Set("X-Org-Id", fmt.Sprint(r.org))
	}
	for k, v := range r.headers {
		if strings.EqualFold(k, "Host") {
			hr.Host = v
			continue
		}
		hr.Header.Set(k, v)
	}
	if r.remote != "" {
		hr.RemoteAddr = r.remote
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, hr)
	return w
}

func (e *env) json(w *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

func (e *env) expect(name string, w *httptest.ResponseRecorder, code int) {
	e.t.Helper()
	if w.Code != code {
		e.t.Errorf("%s: код %d, ждали %d; тело: %.300s", name, w.Code, code, w.Body.String())
	}
}

func authAcct(e *env, m map[string]any, email, pw string) acct {
	a := acct{Email: email, Password: pw}
	a.Token, _ = m["access"].(string)
	a.Refresh, _ = m["refresh"].(string)
	if u, ok := m["user"].(map[string]any); ok {
		a.UserID = uint(u["id"].(float64))
	}
	if ms, ok := m["memberships"].([]any); ok {
		for _, x := range ms {
			mm := x.(map[string]any)
			if mm["status"] == "active" {
				a.Org = uint(mm["organization_id"].(float64))
			}
		}
	}
	return a
}

// owner регистрирует нанимателя с новым домом.
func (e *env) owner(tag string) acct {
	e.t.Helper()
	email := "own-" + tag + "-" + uniq() + "@test.local"
	pw := "owner-pass-" + uniq()
	w := e.do(req{method: "POST", path: "/auth/register", body: map[string]string{
		"email": email, "password": pw, "org_name": "Дом " + tag, "locale": "ru"}})
	if w.Code != 201 {
		e.t.Fatalf("регистрация %s: %d %s", tag, w.Code, w.Body.String())
	}
	return authAcct(e, e.json(w), email, pw)
}

// member приглашает человека в дом владельца с ролью role и принимает приглашение.
func (e *env) member(o acct, role, tag string) acct {
	e.t.Helper()
	email := role + "-" + tag + "-" + uniq() + "@test.local"
	pw := "member-pass-" + uniq()
	w := e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org,
		body: map[string]string{"email": email, "role": role}})
	if w.Code != 201 && w.Code != 200 {
		e.t.Fatalf("приглашение %s: %d %s", role, w.Code, w.Body.String())
	}
	token, _ := e.json(w)["token"].(string)
	w = e.do(req{method: "POST", path: "/invites/" + token + "/accept", body: map[string]string{
		"name": role + " " + tag, "password": pw, "email": email}})
	if w.Code != 200 {
		e.t.Fatalf("принятие %s: %d %s", role, w.Code, w.Body.String())
	}
	a := authAcct(e, e.json(w), email, pw)
	a.Org = o.Org
	return a
}

func (e *env) jsonList(w *httptest.ResponseRecorder) []map[string]any {
	var l []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &l)
	return l
}
