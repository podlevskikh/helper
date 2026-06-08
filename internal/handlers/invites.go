package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// InviteHandler обрабатывает создание и принятие инвайтов.
type InviteHandler struct {
	db *gorm.DB
}

func NewInviteHandler(db *gorm.DB) *InviteHandler {
	return &InviteHandler{db: db}
}

// --- helpers ---

func generateInviteToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func webOrigin() string {
	o := os.Getenv("WEB_ORIGIN")
	if o == "" {
		return "http://localhost:8081"
	}
	return o
}

// --- POST /orgs/:orgId/invites ---
// Требует Auth + OrgContext + RoleOwner-or-Admin.

type createInviteRequest struct {
	Email string      `json:"email"`                         // опционально
	Role  models.Role `json:"role" binding:"required"`
}

func (h *InviteHandler) CreateInvite(c *gin.Context) {
	var req createInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	m := middleware.MustMembership(c)
	if !middleware.Can(m, middleware.CapManageTeam) {
		c.JSON(http.StatusForbidden, gin.H{"error": "only owner/admin can invite"})
		return
	}

	// owner нельзя приглашать через инвайт
	if req.Role == models.RoleOwner {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot invite with owner role"})
		return
	}

	token, err := generateInviteToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	invite := models.Invite{
		OrganizationID: m.OrganizationID,
		Email:          email,
		Role:           req.Role,
		Token:          token,
		Status:         models.InvitePending,
		ExpiresAt:      time.Now().Add(7 * 24 * time.Hour),
		InvitedBy:      m.UserID,
	}
	if err := h.db.Create(&invite).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create invite"})
		return
	}

	inviteURL := fmt.Sprintf("%s/invite?token=%s", webOrigin(), token)
	c.JSON(http.StatusCreated, gin.H{
		"invite_url": inviteURL,
		"token":      token,
		"expires_at": invite.ExpiresAt,
		"role":       invite.Role,
		"email":      invite.Email,
	})
}

// --- GET /invites/:token (public) ---

func (h *InviteHandler) GetInvite(c *gin.Context) {
	token := c.Param("token")

	var invite models.Invite
	if err := h.db.Where("token = ?", token).First(&invite).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invite not found"})
		return
	}
	if invite.Status != models.InvitePending || time.Now().After(invite.ExpiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "invite expired or already used"})
		return
	}

	var org models.Organization
	h.db.First(&org, invite.OrganizationID)

	c.JSON(http.StatusOK, gin.H{
		"org_name":   org.Name,
		"role":       invite.Role,
		"email":      invite.Email,
		"expires_at": invite.ExpiresAt,
	})
}

// --- POST /invites/:token/accept ---

type acceptInviteRequest struct {
	Name     string `json:"name"     binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

func (h *InviteHandler) AcceptInvite(c *gin.Context) {
	token := c.Param("token")

	var req acceptInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var invite models.Invite
	if err := h.db.Where("token = ?", token).First(&invite).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invite not found"})
		return
	}
	if invite.Status != models.InvitePending || time.Now().After(invite.ExpiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "invite expired or already used"})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	var user models.User
	err = h.db.Transaction(func(tx *gorm.DB) error {
		// Ищем существующего пользователя по email инвайта (или создаём нового)
		email := invite.Email
		if email == "" {
			// инвайт без email — новый пользователь
			email = req.Name + "@invite.local" // заглушка, пользователь сможет изменить позже
		}
		if txErr := tx.Where("email = ?", email).First(&user).Error; txErr != nil {
			// Создаём нового
			user = models.User{
				Email:        email,
				PasswordHash: hash,
				Name:         strings.TrimSpace(req.Name),
				Locale:       "ru",
			}
			if txErr = tx.Create(&user).Error; txErr != nil {
				return txErr
			}
		} else {
			// Пользователь уже существует — просто обновляем имя и пароль
			tx.Model(&user).Updates(map[string]interface{}{
				"name":          strings.TrimSpace(req.Name),
				"password_hash": hash,
			})
		}

		// Проверяем, нет ли уже членства в этой орге
		var existing models.Membership
		if tx.Where("user_id = ? AND organization_id = ?", user.ID, invite.OrganizationID).First(&existing).Error == nil {
			// Уже есть — только активируем
			tx.Model(&existing).Updates(map[string]interface{}{
				"role":   invite.Role,
				"status": models.MembershipActive,
			})
		} else {
			membership := models.Membership{
				UserID:         user.ID,
				OrganizationID: invite.OrganizationID,
				Role:           invite.Role,
				Status:         models.MembershipActive,
				InvitedBy:      &invite.InvitedBy,
			}
			if txErr := tx.Create(&membership).Error; txErr != nil {
				return txErr
			}
		}

		// Помечаем инвайт как принятый
		return tx.Model(&invite).Update("status", models.InviteAccepted).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to accept invite"})
		return
	}

	// Используем authHandler helper через прямой вызов
	ah := &AuthHandler{db: h.db}
	access, refreshToken, err := ah.issueTokenPair(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue tokens"})
		return
	}
	memberships, _ := ah.loadMemberships(user.ID)
	c.JSON(http.StatusOK, authResponse{
		Access:      access,
		Refresh:     refreshToken,
		User:        &user,
		Memberships: memberships,
	})
}
