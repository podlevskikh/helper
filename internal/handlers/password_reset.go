package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/mail"
	"podlevskikh/awesomeProject/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const passwordResetTTL = time.Hour

// ForgotPassword godoc
// POST /auth/password/forgot  {email}
// Всегда 200 — по ответу нельзя узнать, есть ли такой адрес. Если есть — уходит письмо со ссылкой.
const forgotMinDuration = 300 * time.Millisecond

// padDuration досыпает до min с момента start (вызывать через defer в начале обработчика).
func padDuration(start time.Time, min time.Duration) {
	if d := min - time.Since(start); d > 0 {
		time.Sleep(d)
	}
}

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ok := gin.H{"ok": true, "mail_configured": mail.Configured()}
	// Ответ не раньше чем через forgotMinDuration — и для существующей почты (запись в базу), и для
	// несуществующей: по времени ответа нельзя узнать, зарегистрирован ли адрес.
	defer padDuration(time.Now(), forgotMinDuration)
	var user models.User
	if err := h.db.Where("email = ?", strings.ToLower(strings.TrimSpace(req.Email))).First(&user).Error; err != nil {
		c.JSON(http.StatusOK, ok)
		return
	}
	// Не чаще раза в минуту на пользователя — чтобы ящик не завалили письмами.
	var recent int64
	h.db.Model(&models.PasswordReset{}).Where("user_id = ? AND created_at > ?", user.ID, time.Now().Add(-time.Minute)).Count(&recent)
	if recent > 0 {
		c.JSON(http.StatusOK, ok)
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	raw := hex.EncodeToString(b)
	reset := models.PasswordReset{UserID: user.ID, TokenHash: auth.HashToken(raw), ExpiresAt: time.Now().Add(passwordResetTTL)}
	if err := h.db.Create(&reset).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	link := webOrigin(c) + "/reset-password?token=" + url.QueryEscape(raw)
	mail.SendAsync(mail.PasswordReset(user.Email, user.Locale, link))
	c.JSON(http.StatusOK, ok)
}

// ResetPassword godoc
// POST /auth/password/reset  {token, password}
// Ставит новый пароль, гасит ссылку и все старые сессии, сразу входит.
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req struct {
		Token    string `json:"token"    binding:"required"`
		Password string `json:"password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var reset models.PasswordReset
	if err := h.db.Where("token_hash = ?", auth.HashToken(strings.TrimSpace(req.Token))).First(&reset).Error; err != nil ||
		reset.UsedAt != nil || time.Now().After(reset.ExpiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "link expired or already used"})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash error"})
		return
	}
	now := time.Now()
	err = h.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.PasswordReset{}).Where("id = ? AND used_at IS NULL", reset.ID).Update("used_at", now)
		if res.Error != nil || res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound // ссылку успели использовать параллельно
		}
		if err := tx.Model(&models.User{}).Where("id = ?", reset.UserID).Updates(map[string]interface{}{
			"password_hash": hash, "token_version": gorm.Expr("token_version + 1")}).Error; err != nil {
			return err
		}
		// остальные ссылки этого пользователя тоже гасим
		tx.Model(&models.PasswordReset{}).Where("user_id = ? AND used_at IS NULL", reset.UserID).Update("used_at", now)
		// и незавершённую смену почты — её мог запросить тот, кто знал старый пароль
		if err := CancelPendingEmailChanges(tx, reset.UserID, now); err != nil {
			return err
		}
		return tx.Model(&models.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", reset.UserID).Update("revoked_at", now).Error
	})
	if err != nil {
		c.JSON(http.StatusGone, gin.H{"error": "link expired or already used"})
		return
	}
	var user models.User
	h.db.First(&user, reset.UserID)
	mail.SendAsync(mail.PasswordChanged(user.Email, user.Locale, webOrigin(c)))

	access, refresh, err := h.issueTokenPair(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	memberships, _ := h.loadMemberships(user.ID)
	c.JSON(http.StatusOK, authResponse{Access: access, Refresh: refresh, User: &user, Memberships: memberships})
}

// CheckResetToken — POST /auth/password/reset/check {token}: действует ли ссылка (до ввода нового пароля).
func (h *AuthHandler) CheckResetToken(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var reset models.PasswordReset
	if err := h.db.Where("token_hash = ?", auth.HashToken(strings.TrimSpace(req.Token))).First(&reset).Error; err != nil ||
		reset.UsedAt != nil || time.Now().After(reset.ExpiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "link expired or already used"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
