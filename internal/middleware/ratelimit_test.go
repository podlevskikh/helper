package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// SEC-17: параллельные запросы не обходят лимит неудачных попыток (раньше неудача засчитывалась
// только после ответа, и пока шла проверка пароля, проходили все).
func TestRateLimitConcurrentFailures(t *testing.T) {
	t.Setenv("RATE_LIMIT_SCALE", "")
	gin.SetMode(gin.TestMode)
	var reached atomic.Int64
	r := gin.New()
	r.POST("/login", RateLimit(RateLimitOptions{Max: 10, Per: time.Hour, OnlyFailures: true}), func(c *gin.Context) {
		reached.Add(1)
		time.Sleep(50 * time.Millisecond) // как bcrypt
		c.Status(http.StatusUnauthorized)
	})
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/login", nil)
			req.RemoteAddr = "203.0.113.7:1234"
			r.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()
	if n := reached.Load(); n != 10 {
		t.Fatalf("до проверки пароля дошло %d запросов, ждали 10", n)
	}
}

// Успешные ответы при OnlyFailures лимит не съедают.
func TestRateLimitSuccessRefunded(t *testing.T) {
	t.Setenv("RATE_LIMIT_SCALE", "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", RateLimit(RateLimitOptions{Max: 3, Per: time.Hour, OnlyFailures: true}), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/login", nil)
		req.RemoteAddr = "203.0.113.8:1234"
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("успешный вход №%d: %d", i+1, w.Code)
		}
	}
}

// IPv6: один клиент с сетью /64 — один счётчик.
func TestRateLimitIPv6Per64(t *testing.T) {
	t.Setenv("RATE_LIMIT_SCALE", "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", RateLimit(RateLimitOptions{Max: 3, Per: time.Hour, OnlyFailures: true}), func(c *gin.Context) {
		c.Status(http.StatusUnauthorized)
	})
	codes := []int{}
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/login", nil)
		req.RemoteAddr = "[2001:db8:1:2::" + string(rune('a'+i)) + "]:1234"
		r.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[3] != http.StatusTooManyRequests {
		t.Fatalf("разные адреса одной /64 не делят лимит: %v", codes)
	}
}
