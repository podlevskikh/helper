package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// API-03: старый клиент получает 426 update_required, новый и веб (без заголовка) — работают.
func TestAppVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MIN_APP_VERSION", "1.2.0")
	r := gin.New()
	r.Use(AppVersion())
	r.GET("/x", func(c *gin.Context) { c.Status(200) })
	for v, want := range map[string]int{"": 200, "1.2.0": 200, "1.10.0": 200, "1.1.9": http.StatusUpgradeRequired, "0.9": http.StatusUpgradeRequired} {
		req := httptest.NewRequest("GET", "/x", nil)
		if v != "" {
			req.Header.Set("X-App-Version", v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("версия %q: %d, ждали %d", v, w.Code, want)
		}
	}
}
