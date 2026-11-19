package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Ограничение попыток на входе, регистрации, восстановлении пароля и приглашениях —
// чтобы пароль нельзя было подбирать перебором. Счётчики в памяти (один экземпляр сервера).

type window struct {
	start time.Time
	count int
}

type limiter struct {
	mu      sync.Mutex
	max     int
	per     time.Duration
	buckets map[string]*window
}

// RateLimitOptions: Max попыток за Per с одного адреса. OnlyFailures — считать только
// неудачные ответы (4xx), чтобы успешные входы не съедали лимит.
type RateLimitOptions struct {
	Max          int
	Per          time.Duration
	OnlyFailures bool
}

// clientKey — адрес клиента. За прокси Railway настоящий адрес — последний в X-Forwarded-For
// (левые части клиент может подставить сам).
func clientKey(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	return c.RemoteIP()
}

func RateLimit(opt RateLimitOptions) gin.HandlerFunc {
	l := &limiter{max: opt.Max, per: opt.Per, buckets: map[string]*window{}}
	go func() { // старые записи — не копим
		for range time.Tick(opt.Per) {
			l.mu.Lock()
			for k, w := range l.buckets {
				if time.Since(w.start) > opt.Per {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		key := clientKey(c)
		now := time.Now()

		l.mu.Lock()
		w := l.buckets[key]
		if w == nil || now.Sub(w.start) > l.per {
			w = &window{start: now}
			l.buckets[key] = w
		}
		if w.count >= l.max {
			retry := int(l.per.Seconds() - now.Sub(w.start).Seconds())
			if retry < 1 {
				retry = 1
			}
			l.mu.Unlock()
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "too many attempts",
				"code":        "rate_limited",
				"retry_after": retry,
			})
			return
		}
		if !opt.OnlyFailures {
			w.count++
		}
		l.mu.Unlock()

		c.Next()

		if opt.OnlyFailures && c.Writer.Status() >= 400 && c.Writer.Status() < 500 {
			l.mu.Lock()
			if cur := l.buckets[key]; cur != nil {
				cur.count++
			}
			l.mu.Unlock()
		}
	}
}
