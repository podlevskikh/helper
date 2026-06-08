package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AuthHandler обрабатывает аутентификацию и управление сессиями.
type AuthHandler struct {
	db *gorm.DB
}

func NewAuthHandler(db *gorm.DB) *AuthHandler {
	return &AuthHandler{db: db}
}

// --- DTO ---

type registerRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	OrgName  string `json:"org_name" binding:"required"`
}

type loginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	Refresh string `json:"refresh" binding:"required"`
}

type logoutRequest struct {
	Refresh string `json:"refresh" binding:"required"`
}

type membershipView struct {
	ID             uint         `json:"id"`
	OrganizationID uint         `json:"organization_id"`
	OrgName        string       `json:"org_name"`
	Role           models.Role  `json:"role"`
	Status         models.MembershipStatus `json:"status"`
}

type authResponse struct {
	Access      string           `json:"access"`
	Refresh     string           `json:"refresh"`
	User        *models.User     `json:"user"`
	Memberships []membershipView `json:"memberships"`
}

// --- helpers ---

func (h *AuthHandler) issueTokenPair(userID uint) (accessToken, rawRefresh string, err error) {
	accessToken, err = auth.GenerateAccessToken(userID)
	if err != nil {
		return
	}
	var tokenHash string
	var expiresAt time.Time
	rawRefresh, tokenHash, expiresAt, err = auth.GenerateRefreshToken()
	if err != nil {
		return
	}
	rt := models.RefreshToken{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}
	err = h.db.Create(&rt).Error
	return
}

func (h *AuthHandler) loadMemberships(userID uint) ([]membershipView, error) {
	var memberships []models.Membership
	if err := h.db.Where("user_id = ? AND status = ?", userID, models.MembershipActive).Find(&memberships).Error; err != nil {
		return nil, err
	}
	views := make([]membershipView, 0, len(memberships))
	for _, m := range memberships {
		var org models.Organization
		h.db.First(&org, m.OrganizationID)
		views = append(views, membershipView{
			ID:             m.ID,
			OrganizationID: m.OrganizationID,
			OrgName:        org.Name,
			Role:           m.Role,
			Status:         m.Status,
		})
	}
	return views, nil
}

// --- handlers ---

// Register godoc
// POST /auth/register
// Body: {email, password, org_name}
// Создаёт User + Organization + owner Membership. Возвращает токены.
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// Проверяем дубликат email
	var existing models.User
	if err := h.db.Where("email = ?", req.Email).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	user := models.User{Email: req.Email, PasswordHash: hash, Name: req.Email, Locale: "ru"}
	org := models.Organization{Name: strings.TrimSpace(req.OrgName)}

	// Транзакция: user + org + membership
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		if err := tx.Create(&org).Error; err != nil {
			return err
		}
		tx.Model(&org).Update("owner_user_id", user.ID)
		membership := models.Membership{
			UserID:         user.ID,
			OrganizationID: org.ID,
			Role:           models.RoleOwner,
			Status:         models.MembershipActive,
		}
		return tx.Create(&membership).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "registration failed"})
		return
	}

	access, refresh, err := h.issueTokenPair(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue tokens"})
		return
	}
	memberships, _ := h.loadMemberships(user.ID)
	c.JSON(http.StatusCreated, authResponse{Access: access, Refresh: refresh, User: &user, Memberships: memberships})
}

// Login godoc
// POST /auth/login
// Body: {email, password}
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	var user models.User
	if err := h.db.Where("email = ?", req.Email).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err := auth.CheckPassword(req.Password, user.PasswordHash); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	access, refresh, err := h.issueTokenPair(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue tokens"})
		return
	}
	memberships, _ := h.loadMemberships(user.ID)
	c.JSON(http.StatusOK, authResponse{Access: access, Refresh: refresh, User: &user, Memberships: memberships})
}

// Refresh godoc
// POST /auth/refresh
// Body: {refresh} — ротация: старый токен отзывается, выдаётся новая пара.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hash := auth.HashToken(req.Refresh)
	var rt models.RefreshToken
	err := h.db.Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", hash, time.Now()).First(&rt).Error
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
		return
	}

	// Отзываем старый токен
	now := time.Now()
	h.db.Model(&rt).Update("revoked_at", &now)

	access, refresh, err := h.issueTokenPair(rt.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue tokens"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"access": access, "refresh": refresh})
}

// Logout godoc
// POST /auth/logout
// Body: {refresh} — отзывает refresh-токен. Возвращает 204.
func (h *AuthHandler) Logout(c *gin.Context) {
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hash := auth.HashToken(req.Refresh)
	now := time.Now()
	h.db.Model(&models.RefreshToken{}).
		Where("token_hash = ? AND revoked_at IS NULL", hash).
		Update("revoked_at", &now)

	c.Status(http.StatusNoContent)
}

// Me godoc
// GET /auth/me — требует Auth middleware
func (h *AuthHandler) Me(c *gin.Context) {
	userID, _ := c.Get(middleware.ContextKeyUserID)

	var user models.User
	if err := h.db.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		}
		return
	}

	memberships, _ := h.loadMemberships(user.ID)
	c.JSON(http.StatusOK, gin.H{"user": user, "memberships": memberships})
}
