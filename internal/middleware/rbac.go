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
	// CapManageRoutines — «Ритм дома»: создавать и менять регулярные задачи (договорённость — только админы).
	CapManageRoutines Capability = "manage_routines"
	CapViewRecipes    Capability = "view_recipes"
	CapManageRecipes  Capability = "manage_recipes"
	// CapEditRecipes — править текст рецепта (состав, как готовить, время). Есть у всех: кто готовит,
	// тот и уточняет рецепт. Создать, удалить и поменять, в какие приёмы пищи он идёт, — CapManageRecipes.
	CapEditRecipes    Capability = "edit_recipes"
	CapViewShopping   Capability = "view_shopping"
	CapManageShopping Capability = "manage_shopping"
	CapManageTeam     Capability = "manage_team"
	CapManageSettings Capability = "manage_settings"
	CapManageBilling  Capability = "manage_billing"
	// CapOwnHome — только основной админ (owner): передать роль другому админу, удалить дом.
	CapOwnHome Capability = "own_home"
)

// roleCapabilities — фиксированная матрица прав. Расширяемо до гранулярных позже.
var roleCapabilities = map[models.Role][]Capability{
	models.RoleOwner: {
		CapViewSchedule, CapManageSchedule, CapManageRoutines,
		CapViewRecipes, CapEditRecipes, CapManageRecipes,
		CapViewShopping, CapManageShopping,
		CapManageTeam, CapManageSettings, CapManageBilling, CapOwnHome,
	},
	models.RoleAdmin: {
		CapViewSchedule, CapManageSchedule, CapManageRoutines,
		CapViewRecipes, CapEditRecipes, CapManageRecipes,
		CapViewShopping, CapManageShopping,
		CapManageTeam, CapManageSettings,
	},
	models.RoleManager: {
		CapViewSchedule, CapManageSchedule,
		CapViewRecipes, CapEditRecipes,
		CapViewShopping, CapManageShopping,
	},
	models.RoleHelper: {
		CapViewSchedule,
		CapViewRecipes, CapEditRecipes,
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
