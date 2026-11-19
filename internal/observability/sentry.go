// Package observability — отправка ошибок сервера в Sentry. Включается только переменной SENTRY_DSN
// (в Railway или запечатанной в secrets.sealed.json — scripts/seal-sentry.sh). Без неё ничего не делает.
//
// Что уходит: паники обработчиков и ответы 5xx (метод, шаблон маршрута, код, коммит).
// Что не уходит: тела запросов, строка запроса (в ней бывают токены ссылок), Authorization, Cookie,
// IP-адреса и почты пользователей.
package observability

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
)

// Init включает Sentry, если задан SENTRY_DSN. Возвращает функцию, которую надо вызвать перед выходом.
func Init(release string) func() {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return func() {}
	}
	env := os.Getenv("RAILWAY_ENVIRONMENT_NAME")
	if env == "" {
		env = "local"
	}
	if err := initWith(sentry.ClientOptions{Dsn: dsn, Release: release, Environment: env}); err != nil {
		log.Printf("sentry: не включён: %v", err)
		return func() {}
	}
	log.Printf("sentry: включён (окружение %s)", env)
	return func() { sentry.Flush(2 * time.Second) }
}

var enabled bool

// initWith — общие настройки (без личных данных, со стеком); транспорт подменяется в тестах.
func initWith(o sentry.ClientOptions) error {
	o.SendDefaultPII = false
	o.AttachStacktrace = true
	o.BeforeSend = Scrub
	if err := sentry.Init(o); err != nil {
		return err
	}
	enabled = true
	return nil
}

// Enabled — Sentry инициализирован (SENTRY_DSN задан и принят).
func Enabled() bool { return enabled }

// Scrub убирает из события всё, что может содержать личные данные или секреты.
func Scrub(ev *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if ev.Request != nil {
		ev.Request.Data = ""
		ev.Request.QueryString = ""
		ev.Request.Cookies = ""
		ev.Request.Env = nil
		for k := range ev.Request.Headers {
			switch k {
			case "User-Agent", "X-App-Version", "X-App-Platform", "Content-Type":
			default:
				delete(ev.Request.Headers, k)
			}
		}
	}
	ev.User = sentry.User{}
	return ev
}

// Middleware — паники (сообщаются и пробрасываются дальше в gin.Recovery) и ответы 5xx.
// Пусто, если Sentry не включён.
func Middleware() []gin.HandlerFunc {
	if !Enabled() {
		return nil
	}
	return []gin.HandlerFunc{
		sentrygin.New(sentrygin.Options{Repanic: true, WaitForDelivery: false}),
		func(c *gin.Context) {
			c.Next()
			if c.Writer.Status() < 500 {
				return
			}
			hub := sentrygin.GetHubFromContext(c)
			if hub == nil {
				hub = sentry.CurrentHub()
			}
			route := c.FullPath()
			if route == "" {
				route = "(нет маршрута)"
			}
			hub.WithScope(func(s *sentry.Scope) {
				s.SetTag("route", route)
				s.SetTag("status", fmt.Sprint(c.Writer.Status()))
				if len(c.Errors) > 0 {
					s.SetContext("gin", sentry.Context{"errors": c.Errors.String()})
				}
				hub.CaptureMessage(fmt.Sprintf("%d %s %s", c.Writer.Status(), c.Request.Method, route))
			})
		},
	}
}
