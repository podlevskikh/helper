package handlers

import (
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
