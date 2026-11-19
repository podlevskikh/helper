package middleware

import (
	"net"
	"net/http"
	"os"
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

// clientKey — адрес клиента для лимитов.
//   - За прокси платформы (Railway: напрямую к контейнеру не подключиться) — последний адрес в
//     X-Forwarded-For: его дописывает сам прокси, а левые части подставляет кто угодно.
//   - Если соединение пришло из частной сети (свой reverse proxy) — так же.
//   - Иначе (прямое подключение, локально) X-Forwarded-For игнорируется: верим только адресу соединения.
//
// IPv6 считается по сети /64: у одного клиента обычно целая /64, и иначе каждый адрес — новый счётчик.
func clientKey(c *gin.Context) string {
	peer := c.RemoteIP()
	if behindPlatformProxy() || isPrivateIP(peer) {
		if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ipKey(ip)
			}
		}
	}
	return ipKey(peer)
}

func ipKey(s string) string {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() != nil {
		return s
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// behindPlatformProxy — сервер работает за прокси платформы (Railway задаёт свои переменные) или TRUST_PROXY=1.
func behindPlatformProxy() bool {
	return os.Getenv("TRUST_PROXY") == "1" || os.Getenv("RAILWAY_ENVIRONMENT_ID") != "" ||
		os.Getenv("RAILWAY_PROJECT_ID") != "" || os.Getenv("RAILWAY_ENVIRONMENT") != ""
}

func isPrivateIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && !ip.IsLoopback() && ip.IsPrivate()
}

// rateLimitScale — множитель лимитов только для локальных прогонов смоука и тестов
// (RATE_LIMIT_SCALE=100). На проде переменная не задаётся.
func rateLimitScale() int {
	if n, err := strconv.Atoi(os.Getenv("RATE_LIMIT_SCALE")); err == nil && n > 1 {
		return n
	}
	return 1
}

func RateLimit(opt RateLimitOptions) gin.HandlerFunc {
	l := &limiter{max: opt.Max * rateLimitScale(), per: opt.Per, buckets: map[string]*window{}}
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
		// Попытка засчитывается сразу, до обработчика: иначе параллельные запросы (пока идёт bcrypt)
		// проходят все. При OnlyFailures успешный ответ попытку возвращает.
		w.count++
		l.mu.Unlock()

		c.Next()

		if st := c.Writer.Status(); opt.OnlyFailures && (st < 400 || st >= 500) {
			l.mu.Lock()
			if l.buckets[key] == w && w.count > 0 {
				w.count--
			}
			l.mu.Unlock()
		}
	}
}
