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

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
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
		Message:    cut(req.Message, 500),
		Stack:      cut(req.Stack, 4000),
		Route:      observability.RedactPath(cut(req.Route, 200)),
		Platform:   platform,
		AppVersion: cut(req.AppVersion, 32),
	}
	log.Printf("client error [%s %s] %s: %s", e.Platform, e.AppVersion, e.Route, e.Message)
	observability.CaptureClientError(e)
	c.Status(http.StatusNoContent)
}
