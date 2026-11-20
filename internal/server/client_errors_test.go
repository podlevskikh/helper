package server

import (
	"strings"
	"testing"
)

// OBS-04: экран приложения упал — отчёт принимается только со входом, пустой и огромный — отклоняются.
func TestClientErrors(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("clerr")
	body := map[string]any{"message": "TypeError: undefined is not an object", "stack": "at DayList", "route": "/task/123",
		"platform": "ios", "app_version": "1.0.0"}
	e.expect("отчёт со входом", e.do(req{method: "POST", path: "/me/client-errors", token: o.Token, body: body}), 204)
	e.expect("без входа", e.do(req{method: "POST", path: "/me/client-errors", body: body}), 401)
	e.expect("без текста", e.do(req{method: "POST", path: "/me/client-errors", token: o.Token, body: map[string]any{"stack": "x"}}), 400)
	e.expect("больше 16 КБ", e.do(req{method: "POST", path: "/me/client-errors", token: o.Token,
		body: map[string]any{"message": "x", "stack": strings.Repeat("a", 20000)}}), 400)
}
