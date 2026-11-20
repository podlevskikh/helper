package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/mail"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
)

// Языки интерфейса, которые есть в приложении (LOCALES в src/i18n/index.ts фронта).
var supportedLocales = map[string]bool{
	"en": true, "ru": true, "es": true, "fil": true, "id": true, "el": true, "uk": true, "ne": true, "si": true,
}

// defaultLocale — язык интерфейса по умолчанию: английский.
const defaultLocale = "en"

// pickLocale — язык, который клиент прислал при регистрации (уже выбран на устройстве),
// иначе английский.
func pickLocale(l string) string {
	if supportedLocales[l] {
		return l
	}
	return defaultLocale
}

func (h *AuthHandler) currentUser(c *gin.Context) (*models.User, bool) {
	userID, _ := c.Get(middleware.ContextKeyUserID)
	var user models.User
	if err := h.db.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		}
		return nil, false
	}
	return &user, true
}

// UpdateMe — PATCH /auth/me {name?, locale?}: имя и язык интерфейса.
func (h *AuthHandler) UpdateMe(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var body struct {
		Name   *string `json:"name"`
		Locale *string `json:"locale"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]any{}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" || utf8.RuneCountInString(name) > 60 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name must be 1..60 characters"})
			return
		}
		updates["name"] = name
	}
	if body.Locale != nil {
		if !supportedLocales[*body.Locale] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported locale"})
			return
		}
		updates["locale"] = *body.Locale
	}
	if len(updates) > 0 {
		if err := h.db.Model(user).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
	}
	memberships, _ := h.loadMemberships(user.ID)
	c.JSON(http.StatusOK, gin.H{"user": user, "memberships": memberships})
}

// ChangePassword — POST /auth/password {current_password, new_password}.
// Все остальные сессии пользователя выходят; текущая получает новую пару токенов.
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var body struct {
		Current string `json:"current_password" binding:"required"`
		New     string `json:"new_password"     binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new password must be at least 8 characters"})
		return
	}
	if err := auth.CheckPassword(body.Current, user.PasswordHash); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "wrong current password"})
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash error"})
		return
	}
	now := time.Now()
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(user).Updates(map[string]interface{}{
			"password_hash": hash, "token_version": gorm.Expr("token_version + 1")}).Error; err != nil {
			return err
		}
		// Незавершённую смену почты гасим: её мог запросить тот, кто знал старый пароль.
		if err := CancelPendingEmailChanges(tx, user.ID, now); err != nil {
			return err
		}
		return tx.Model(&models.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", user.ID).
			Update("revoked_at", now).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	access, refresh, err := h.issueTokenPair(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	mail.SendAsync(mail.PasswordChanged(user.Email, user.Locale, webOrigin(c)))
	c.JSON(http.StatusOK, gin.H{"access": access, "refresh": refresh})
}
