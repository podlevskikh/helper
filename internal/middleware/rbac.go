package middleware

import (
	"net/http"

	"podlevskikh/awesomeProject/internal/models"

	"github.com/gin-gonic/gin"
)

// Capability — действие, которое нужно авторизовать.
type Capability string

const (
	CapViewSchedule   Capability = "view_schedule"
	CapManageSchedule Capability = "manage_schedule"
	CapViewRecipes    Capability = "view_recipes"
	CapManageRecipes  Capability = "manage_recipes"
	CapViewShopping   Capability = "view_shopping"
	CapManageShopping Capability = "manage_shopping"
	CapManageTeam     Capability = "manage_team"
	CapManageSettings Capability = "manage_settings"
	CapManageBilling  Capability = "manage_billing"
)

// roleCapabilities — фиксированная матрица прав. Расширяемо до гранулярных позже.
var roleCapabilities = map[models.Role][]Capability{
	models.RoleOwner: {
		CapViewSchedule, CapManageSchedule,
		CapViewRecipes, CapManageRecipes,
		CapViewShopping, CapManageShopping,
		CapManageTeam, CapManageSettings, CapManageBilling,
	},
	models.RoleAdmin: {
		CapViewSchedule, CapManageSchedule,
		CapViewRecipes, CapManageRecipes,
		CapViewShopping, CapManageShopping,
		CapManageTeam,
	},
	models.RoleManager: {
		CapViewSchedule, CapManageSchedule,
		CapViewRecipes,
		CapViewShopping, CapManageShopping,
	},
	models.RoleHelper: {
		CapViewSchedule,
		CapViewRecipes,
		CapViewShopping, CapManageShopping,
	},
}

// Can проверяет, есть ли у membership нужная capability.
func Can(m *models.Membership, cap Capability) bool {
	caps, ok := roleCapabilities[m.Role]
	if !ok {
		return false
	}
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}

// Require — middleware: если нет capability → 403. Применять после OrgContext.
func Require(cap Capability) gin.HandlerFunc {
	return func(c *gin.Context) {
		m := MustMembership(c)
		if !Can(m, cap) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}
		c.Next()
	}
}
