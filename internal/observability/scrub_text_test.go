package observability

import "testing"

// OBS-04: отчёт об ошибке приложения — без почты, в одну строку, экран без чисел.
func TestScrubTextHelpers(t *testing.T) {
	if got := ScrubText(`API 409: {"error":"anna.k@example.com уже в доме"}`); got != `API 409: {"error":"<email> уже в доме"}` {
		t.Errorf("ScrubText: %q", got)
	}
	if got := OneLine("ошибка\n[GIN] поддельная строка\r"); got != "ошибка [GIN] поддельная строка " {
		t.Errorf("OneLine: %q", got)
	}
	if got := RouteTemplate("/task/123/edit"); got != "/task/:id/edit" {
		t.Errorf("RouteTemplate: %q", got)
	}
}
