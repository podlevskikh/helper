package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	netmail "net/mail"
	"os"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/mail"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/notify"

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

// DefaultPublicAppURL — адрес веб-версии на проде, если ничего не задано.
// Когда подключим свой домен — PUBLIC_APP_URL=https://helperday.app/app (переменная или запечатанный секрет).
const DefaultPublicAppURL = "https://helper-production-12b0.up.railway.app/app"

// webOrigin — куда ведут ссылки в письмах и приглашениях.
// Только из доверенных источников: WEB_ORIGIN (первый адрес), PUBLIC_APP_URL, константа прода.
// Заголовки Host / X-Forwarded-* атакующий подставляет сам — по ним строим ссылку только для
// локального адреса (localhost), чтобы работали локальные проверки.
func webOrigin(c *gin.Context) string {
	if o := strings.TrimSpace(strings.Split(os.Getenv("WEB_ORIGIN"), ",")[0]); o != "" {
		return strings.TrimRight(o, "/")
	}
	if u := strings.TrimSpace(os.Getenv("PUBLIC_APP_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	if c != nil && isLocalHost(c.Request.Host) {
		return "http://" + c.Request.Host + WebAppPrefix
	}
	return DefaultPublicAppURL
}

func isLocalHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.Trim(strings.ToLower(h), "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

// --- POST /orgs/:orgId/invites ---
// Требует Auth + OrgContext + RoleOwner-or-Admin.

type createInviteRequest struct {
	Email string      `json:"email"` // опционально
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

	inviteURL := fmt.Sprintf("%s/invite?token=%s", webOrigin(c), token)
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
	Name     string `json:"name"`
	Email    string `json:"email"` // нужен, если в приглашении email не указан
	Password string `json:"password"`
	Locale   string `json:"locale"`
}

// inviteError — ответ с машинным кодом: экран приглашения показывает по нему понятный текст.
func inviteError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": msg, "code": code})
}

func (h *InviteHandler) AcceptInvite(c *gin.Context) {
	token := c.Param("token")

	var req acceptInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		inviteError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		inviteError(c, http.StatusBadRequest, "name_required", "name is required")
		return
	}
	if len([]rune(req.Password)) < 8 {
		inviteError(c, http.StatusBadRequest, "password_too_short", "password must be at least 8 characters")
		return
	}

	var invite models.Invite
	if err := h.db.Where("token = ?", token).First(&invite).Error; err != nil {
		inviteError(c, http.StatusNotFound, "invite_not_found", "invite not found")
		return
	}
	if invite.Status != models.InvitePending || time.Now().After(invite.ExpiresAt) {
		inviteError(c, http.StatusGone, "invite_used", "invite expired or already used")
		return
	}

	// Email для входа: из приглашения, а если там пусто — тот, что ввёл сам приглашённый.
	email := invite.Email
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(req.Email))
		if _, err := netmail.ParseAddress(email); err != nil || !strings.Contains(email, "@") || strings.ContainsAny(email, " <>") {
			inviteError(c, http.StatusBadRequest, "email_invalid", "a valid email is required")
			return
		}
	}

	// Уже есть аккаунт с этим email — присоединяем только с его паролем, чужой пароль не перезаписываем.
	var user models.User
	existing := h.db.Where("LOWER(email) = ?", email).First(&user).Error == nil
	if existing && auth.CheckPassword(req.Password, user.PasswordHash) != nil {
		inviteError(c, http.StatusConflict, "account_exists", "this email is already registered; use its password")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		if !existing {
			user = models.User{
				Email:        email,
				PasswordHash: hash,
				Name:         req.Name,
				Locale:       pickLocale(req.Locale),
			}
			if txErr := tx.Create(&user).Error; txErr != nil {
				return txErr
			}
		} else if strings.TrimSpace(user.Name) == "" {
			tx.Model(&user).Update("name", req.Name)
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
	notify.MemberJoined(h.db, invite.OrganizationID, user.ID)
	var org models.Organization
	h.db.First(&org, invite.OrganizationID)
	mail.SendAsync(mail.Welcome(user.Email, user.Locale, user.Name, org.Name, webOrigin(c), true))

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
