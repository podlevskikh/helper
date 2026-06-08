package handlers

import (
	"errors"
	"net/http"

	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// OrgHandler — эндпоинты уровня организации (участники, настройки и т.п.).
type OrgHandler struct {
	db *gorm.DB
}

func NewOrgHandler(db *gorm.DB) *OrgHandler {
	return &OrgHandler{db: db}
}

// MemberView — то, что отдаём наружу (без лишних полей).
type MemberView struct {
	ID             uint                   `json:"id"`
	UserID         uint                   `json:"user_id"`
	OrganizationID uint                   `json:"organization_id"`
	Role           models.Role            `json:"role"`
	Status         models.MembershipStatus `json:"status"`
	InvitedBy      *uint                  `json:"invited_by,omitempty"`
	Name           string                 `json:"name"`
	Email          string                 `json:"email"`
	AvatarURL      string                 `json:"avatar_url,omitempty"`
	CreatedAt      string                 `json:"created_at"`
}

// GetMembers возвращает всех участников организации.
// GET /orgs/:orgId/members  (authMw + orgMw + Require(CapManageTeam))
func (h *OrgHandler) GetMembers(c *gin.Context) {
	m := middleware.MustMembership(c)
	orgID := m.OrganizationID

	var memberships []models.Membership
	if err := h.db.
		Where("organization_id = ? AND status != ?", orgID, models.MembershipDisabled).
		Order("created_at ASC").
		Find(&memberships).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Собираем user_id-ы и загружаем пользователей одним запросом
	userIDs := make([]uint, len(memberships))
	for i, mb := range memberships {
		userIDs[i] = mb.UserID
	}

	var users []models.User
	if len(userIDs) > 0 {
		if err := h.db.Where("id IN ?", userIDs).Find(&users).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	// Индексируем пользователей по ID
	userMap := make(map[uint]models.User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}

	// Формируем ответ
	result := make([]MemberView, 0, len(memberships))
	for _, mb := range memberships {
		u := userMap[mb.UserID]
		result = append(result, MemberView{
			ID:             mb.ID,
			UserID:         mb.UserID,
			OrganizationID: mb.OrganizationID,
			Role:           mb.Role,
			Status:         mb.Status,
			InvitedBy:      mb.InvitedBy,
			Name:           u.Name,
			Email:          u.Email,
			AvatarURL:      u.AvatarURL,
			CreatedAt:      mb.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	c.JSON(http.StatusOK, result)
}

// ── TaskCategory CRUD ────────────────────────────────────────────────────────

// GetTaskCategories возвращает все категории задач организации.
// GET /orgs/:orgId/task-categories
func (h *OrgHandler) GetTaskCategories(c *gin.Context) {
	m := middleware.MustMembership(c)
	var cats []models.TaskCategory
	if err := h.db.
		Where("organization_id = ?", m.OrganizationID).
		Order("sort_order ASC, id ASC").
		Find(&cats).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cats)
}

// CreateTaskCategory создаёт новую пользовательскую категорию.
// POST /orgs/:orgId/task-categories  (CapManageSettings)
func (h *OrgHandler) CreateTaskCategory(c *gin.Context) {
	m := middleware.MustMembership(c)
	var input struct {
		Name      string `json:"name" binding:"required"`
		Icon      string `json:"icon"`
		Color     string `json:"color"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cat := models.TaskCategory{
		OrganizationID: m.OrganizationID,
		Name:           input.Name,
		Icon:           input.Icon,
		Color:          input.Color,
		IsDefault:      false,
		SortOrder:      input.SortOrder,
	}
	if err := h.db.Create(&cat).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, cat)
}

// UpdateTaskCategory обновляет категорию.
// PUT /orgs/:orgId/task-categories/:id  (CapManageSettings)
func (h *OrgHandler) UpdateTaskCategory(c *gin.Context) {
	m := middleware.MustMembership(c)
	id := c.Param("id")

	var cat models.TaskCategory
	if err := h.db.Where("id = ? AND organization_id = ?", id, m.OrganizationID).First(&cat).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
		return
	}

	var input struct {
		Name      string `json:"name"`
		Icon      string `json:"icon"`
		Color     string `json:"color"`
		SortOrder *int   `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Name != "" {
		cat.Name = input.Name
	}
	cat.Icon = input.Icon
	cat.Color = input.Color
	if input.SortOrder != nil {
		cat.SortOrder = *input.SortOrder
	}

	if err := h.db.Save(&cat).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cat)
}

// DeleteTaskCategory удаляет пользовательскую категорию (is_default=false).
// DELETE /orgs/:orgId/task-categories/:id  (CapManageSettings)
func (h *OrgHandler) DeleteTaskCategory(c *gin.Context) {
	m := middleware.MustMembership(c)
	id := c.Param("id")

	var cat models.TaskCategory
	if err := h.db.Where("id = ? AND organization_id = ?", id, m.OrganizationID).First(&cat).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	if cat.IsDefault {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot delete system category"})
		return
	}

	// Обнуляем task_category_id у задач этой категории
	h.db.Exec("UPDATE schedule_tasks SET task_category_id = NULL WHERE task_category_id = ? AND organization_id = ?", cat.ID, m.OrganizationID)

	if err := h.db.Delete(&cat).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}
