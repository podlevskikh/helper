package handlers

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"podlevskikh/awesomeProject/internal/observability"
)

// Ошибки приложения (телефон и веб): экран упал — приложение показывает запасной экран и присылает
// сюда коротко что случилось. Сервер пишет в лог и отправляет в Sentry (тот же SENTRY_DSN, что у
// сервера): отдельный ключ и нативная библиотека в приложении не нужны. Личных данных нет:
// ни тела запросов, ни пользователя, только текст ошибки, стек и адрес экрана без чисел.

type clientErrorRequest struct {
	Message    string `json:"message"`
	Stack      string `json:"stack"`
	Route      string `json:"route"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
}

// cut — не больше n символов (по буквам, не по байтам).
func cut(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n])
	}
	return string(r)
}

// ReportClientError — POST /me/client-errors. 204; 400 — нет текста ошибки или тело больше 16 КБ.
func ReportClientError(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req clientErrorRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message required (body up to 16 KB)"})
		return
	}
	platform := cut(req.Platform, 16)
	switch platform {
	case "ios", "android", "web":
	default:
		platform = "other"
	}
	e := observability.ClientError{
		Message:    observability.OneLine(observability.ScrubText(cut(req.Message, 500))),
		Stack:      observability.ScrubText(cut(req.Stack, 4000)),
		Route:      observability.OneLine(observability.RouteTemplate(cut(req.Route, 200))),
		Platform:   platform,
		AppVersion: observability.OneLine(cut(req.AppVersion, 32)),
	}
	log.Printf("client error [%s %s] %q: %q", e.Platform, e.AppVersion, e.Route, e.Message)
	observability.CaptureClientError(e)
	c.Status(http.StatusNoContent)
}
