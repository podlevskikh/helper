package middleware

import (
	"net/http"
	"strings"

	"podlevskikh/awesomeProject/internal/auth"

	"github.com/gin-gonic/gin"
)

const ContextKeyUserID = "user_id"

// Auth проверяет Bearer-токен и кладёт user_id (uint) в контекст.
// Возвращает 401 если токен отсутствует, невалиден или истёк.
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid Authorization header"})
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")
		claims, err := auth.ValidateAccessToken(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set(ContextKeyUserID, claims.UserID)
		c.Next()
	}
}
