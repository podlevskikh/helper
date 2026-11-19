package observability

import (
	"fmt"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
)

// Секреты в адресе: токен приглашения в пути (/invites/<token>), токены ссылок из писем в строке
// запроса (/app/reset-password?token=…, /app/invite?token=…). В логи и Sentry они не попадают.
var inviteToken = regexp.MustCompile(`(/invites/)[^/?#]+`)

// RedactPath — путь без строки запроса и с токеном приглашения, заменённым на :token.
func RedactPath(path string) string {
	for i, ch := range path {
		if ch == '?' || ch == '#' {
			path = path[:i]
			break
		}
	}
	return inviteToken.ReplaceAllString(path, "${1}:token")
}

// Logger — журнал запросов как у gin.Default, но без строки запроса и токенов в пути.
func Logger() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(p gin.LogFormatterParams) string {
		return fmt.Sprintf("[GIN] %s | %3d | %13v | %15s | %-7s %s\n%s",
			p.TimeStamp.Format(time.RFC3339), p.StatusCode, p.Latency, p.ClientIP, p.Method,
			RedactPath(p.Request.URL.Path), p.ErrorMessage)
	})
}
