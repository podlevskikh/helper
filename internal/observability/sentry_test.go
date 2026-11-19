package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/getsentry/sentry-go"
)

// OBS-01: в Sentry не уходят тело, строка запроса, токены, cookie, IP и пользователь.
func TestScrubRemovesPersonalData(t *testing.T) {
	ev := &sentry.Event{
		Request: &sentry.Request{
			URL:         "https://x/auth/password/reset",
			Data:        `{"password":"secret"}`,
			QueryString: "token=abc",
			Cookies:     "sid=1",
			Headers: map[string]string{
				"Authorization": "Bearer t", "Cookie": "sid=1", "X-Forwarded-For": "1.2.3.4",
				"User-Agent": "ua", "X-App-Version": "1.0.0",
			},
			Env: map[string]string{"REMOTE_ADDR": "1.2.3.4"},
		},
		User: sentry.User{Email: "a@b.c", IPAddress: "1.2.3.4"},
	}
	out := Scrub(ev, nil)
	r := out.Request
	if r.Data != "" || r.QueryString != "" || r.Cookies != "" || r.Env != nil {
		t.Fatalf("остались данные запроса: %+v", r)
	}
	for _, h := range []string{"Authorization", "Cookie", "X-Forwarded-For"} {
		if _, ok := r.Headers[h]; ok {
			t.Fatalf("остался заголовок %s", h)
		}
	}
	if r.Headers["User-Agent"] != "ua" || r.Headers["X-App-Version"] != "1.0.0" {
		t.Fatalf("убраны безопасные заголовки: %v", r.Headers)
	}
	if out.User.Email != "" || out.User.IPAddress != "" {
		t.Fatalf("остался пользователь: %+v", out.User)
	}
}

func TestDisabledWithoutDSN(t *testing.T) {
	t.Setenv("SENTRY_DSN", "")
	Init("test")()
	if Middleware() != nil {
		t.Fatal("без SENTRY_DSN middleware не должен подключаться")
	}
}

type memTransport struct{ events []*sentry.Event }

func (m *memTransport) Flush(time.Duration) bool              { return true }
func (m *memTransport) FlushWithContext(context.Context) bool { return true }
func (m *memTransport) Configure(sentry.ClientOptions)        {}
func (m *memTransport) SendEvent(e *sentry.Event)             { m.events = append(m.events, e) }
func (m *memTransport) Close()                                {}

// OBS-02: ответ 5xx и паника уходят в Sentry с шаблоном маршрута, без тела, токена и строки запроса.
func TestMiddlewareReports5xxAndPanics(t *testing.T) {
	tr := &memTransport{}
	if err := initWith(sentry.ClientOptions{Dsn: "https://k@o0.ingest.sentry.io/1", Transport: tr, Release: "abc"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { enabled = false; _ = sentry.Init(sentry.ClientOptions{}) })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(Middleware()...)
	r.POST("/items/:id", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })
	r.GET("/boom", func(c *gin.Context) { panic("boom") })
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, req := range []*http.Request{
		httptest.NewRequest("POST", "/items/7?token=secret", strings.NewReader(`{"password":"p"}`)),
		httptest.NewRequest("GET", "/boom", nil),
		httptest.NewRequest("GET", "/ok", nil),
	} {
		req.Header.Set("Authorization", "Bearer secret")
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	sentry.Flush(time.Second)
	if len(tr.events) < 2 {
		t.Fatalf("ждали событие 5xx и панику, пришло %d", len(tr.events))
	}
	var saw500 bool
	for _, e := range tr.events {
		if e.Message == "500 POST /items/:id" {
			saw500 = true
		}
		if e.Release != "abc" {
			t.Fatalf("релиз %q", e.Release)
		}
		if e.Request != nil && (e.Request.Data != "" || e.Request.QueryString != "" || e.Request.Headers["Authorization"] != "") {
			t.Fatalf("в событии остались данные запроса: %+v", e.Request)
		}
		if strings.Contains(e.Request.URL, "secret") {
			t.Fatalf("токен в URL: %s", e.Request.URL)
		}
	}
	if !saw500 {
		t.Fatalf("нет события «500 POST /items/:id»: %+v", tr.events)
	}
}

// OBS-03: токены ссылок не попадают ни в журнал запросов, ни в Sentry.
func TestRedactPath(t *testing.T) {
	for in, want := range map[string]string{
		"/invites/abc123/accept":           "/invites/:token/accept",
		"/invites/abc123":                  "/invites/:token",
		"/app/reset-password?token=secret": "/app/reset-password",
		"/app/invite?token=secret#x":       "/app/invite",
		"/orgs/5/invites/7":                "/orgs/5/invites/:token",
		"/admin/api/tasks/12":              "/admin/api/tasks/12",
		"https://h/invites/tok/accept?a=b": "https://h/invites/:token/accept",
	} {
		if got := RedactPath(in); got != want {
			t.Errorf("RedactPath(%q) = %q, ждали %q", in, got, want)
		}
	}
	ev := Scrub(&sentry.Event{Request: &sentry.Request{URL: "https://h/invites/secret-token/accept"}}, nil)
	if strings.Contains(ev.Request.URL, "secret") {
		t.Fatalf("токен в URL события: %s", ev.Request.URL)
	}
}

func TestLoggerRedacts(t *testing.T) {
	var buf strings.Builder
	gin.DefaultWriter = &buf
	t.Cleanup(func() { gin.DefaultWriter = os.Stdout })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Logger())
	r.GET("/invites/:token", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/app/reset-password", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/invites/secret-invite", nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/app/reset-password?token=secret-reset", nil))
	if strings.Contains(buf.String(), "secret") || !strings.Contains(buf.String(), "/invites/:token") {
		t.Fatalf("в журнале токен или нет записи: %q", buf.String())
	}
}
