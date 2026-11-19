package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var startedAt = time.Now().UTC()

// commitSHA — коммит, из которого собран сервер. Railway при выкатке из GitHub задаёт
// RAILWAY_GIT_COMMIT_SHA; GIT_SHA — для других окружений и локально.
// CommitSHA — коммит, из которого собран сервер (для /version и релиза в Sentry).
func CommitSHA() string { return commitSHA() }

func commitSHA() string {
	for _, k := range []string{"RAILWAY_GIT_COMMIT_SHA", "GIT_SHA"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "unknown"
}

// webappInfo — webapp/build-info.json: из какого коммита фронта собрана веб-версия.
func webappInfo() map[string]any {
	var info map[string]any
	if b, err := os.ReadFile("webapp/build-info.json"); err == nil {
		_ = json.Unmarshal(b, &info)
	}
	return info
}

// registerHealth: /healthz — жив ли сервер и база (для healthcheck Railway и мониторинга),
// /version — какой коммит отвечает (смоук после выкатки ждёт нужный).
func registerHealth(router *gin.Engine, db *gorm.DB) {
	router.GET("/healthz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		sqlDB, err := db.DB()
		if err == nil {
			err = sqlDB.PingContext(ctx)
		}
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db_unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/version", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{
			"sha":        commitSHA(),
			"started_at": startedAt.Format(time.RFC3339),
			"webapp":     webappInfo(),
		})
	})
}
