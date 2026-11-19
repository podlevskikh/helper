package middleware

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// AppVersion: клиент присылает X-App-Version (например, "1.2.0"). Если задан MIN_APP_VERSION и версия
// клиента ниже — 426 с кодом update_required: старое нативное приложение не ломается молча на новом API,
// а просит обновиться. Веб-версия всегда свежая (раздаётся этим же сервером).
func AppVersion() gin.HandlerFunc {
	return func(c *gin.Context) {
		min := strings.TrimSpace(os.Getenv("MIN_APP_VERSION"))
		v := strings.TrimSpace(c.GetHeader("X-App-Version"))
		if min != "" && v != "" && compareVersions(v, min) < 0 {
			c.AbortWithStatusJSON(http.StatusUpgradeRequired, gin.H{
				"error": "app update required", "code": "update_required", "min_version": min,
			})
			return
		}
		c.Next()
	}
}

// compareVersions сравнивает "1.2.10" и "1.10.0" по числам: -1, 0, 1.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
