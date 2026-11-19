package handlers

import (
	"net/http"

	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// checkTaskRefs: категория и исполнитель задачи должны быть из того же дома.
// Иначе задача ссылалась бы на чужую категорию или чужого человека. Пишет 400 и возвращает false.
func checkTaskRefs(c *gin.Context, db *gorm.DB, orgID uint, categoryID, assigneeID *uint) bool {
	if categoryID != nil {
		var n int64
		db.Model(&models.TaskCategory{}).Where("id = ? AND organization_id = ?", *categoryID, orgID).Count(&n)
		if n == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown task_category_id", "code": "bad_reference"})
			return false
		}
	}
	if assigneeID != nil {
		var n int64
		db.Model(&models.Membership{}).
			Where("user_id = ? AND organization_id = ? AND status = ?", *assigneeID, orgID, models.MembershipActive).Count(&n)
		if n == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown assigned_to_user_id", "code": "bad_reference"})
			return false
		}
	}
	return true
}

// hiddenFromMember: помощник видит и отмечает только общие задачи и свои — личные задачи нанимателя
// или другого сотрудника для него как будто не существуют (404). Тем, кто ведёт расписание, видно всё.
func hiddenFromMember(c *gin.Context, task *models.ScheduleTask) bool {
	m := middleware.MustMembership(c)
	return !middleware.Can(m, middleware.CapManageSchedule) &&
		task.AssignedToUserID != nil && *task.AssignedToUserID != m.UserID
}
