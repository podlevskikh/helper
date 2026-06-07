package middleware

import (
	"net/http"
	"strconv"

	"podlevskikh/awesomeProject/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const ContextKeyMembership = "membership"

// OrgContext читает X-Org-Id, проверяет активное Membership пользователя
// и кладёт *models.Membership в контекст. Должен стоять после Auth().
func OrgContext(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		orgIDStr := c.GetHeader("X-Org-Id")
		if orgIDStr == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "X-Org-Id header required"})
			return
		}
		orgID, err := strconv.ParseUint(orgIDStr, 10, 64)
		if err != nil || orgID == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid X-Org-Id"})
			return
		}

		userID, exists := c.Get(ContextKeyUserID)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}

		var membership models.Membership
		result := db.Where(
			"user_id = ? AND organization_id = ? AND status = ?",
			userID, uint(orgID), models.MembershipActive,
		).First(&membership)
		if result.Error != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this organization"})
			return
		}

		c.Set(ContextKeyMembership, &membership)
		c.Next()
	}
}

// MustMembership возвращает Membership из контекста (паникует если не установлен —
// значит OrgContext не был применён к маршруту).
func MustMembership(c *gin.Context) *models.Membership {
	m, _ := c.Get(ContextKeyMembership)
	return m.(*models.Membership)
}
