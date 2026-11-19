package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/mail"
	"podlevskikh/awesomeProject/internal/models"
)

// Смена почты для входа — как в обычных приложениях:
//  1. POST /auth/email {current_password, new_email} — повторный ввод пароля; на новый адрес — ссылка
//     подтверждения (24 часа, одноразовая), на старый — уведомление со ссылкой «это не я — отменить».
//     Ответ одинаковый, занят адрес или нет: если занят, на него уходит письмо «уже используется»
//     вместо ссылки, и подтвердить такую смену нельзя.
//  2. До подтверждения почта не меняется: GET /auth/email — что ждёт подтверждения,
//     POST /auth/email/resend — отправить ещё раз, DELETE /auth/email — отменить.
//  3. POST /auth/email/confirm {token} — почта меняется, все сессии завершаются, ссылки сброса
//     пароля гаснут, на старый адрес — «почта изменена». Работает и без входа.
//  4. POST /auth/email/cancel {token} — по ссылке со старого адреса: запрос отменяется, все сессии
//     завершаются, пароль сбрасывается (его, похоже, знает кто-то ещё) — новый задают через «Забыли пароль?».
//  5. POST /auth/email/revert {token} — ссылка в письме «почта изменена» на старый адрес (14 дней):
//     почта возвращается, все сессии завершаются, пароль сбрасывается. Спасает, если смену подтвердили
//     быстрее, чем владелец увидел уведомление.
// Незавершённая смена привязана к версии токена: смена или сброс пароля, «это не я» гасят её.
// Токены — 256 бит, в базе только SHA-256.

const (
	emailChangeTTL = 24 * time.Hour
	emailRevertTTL = 14 * 24 * time.Hour
	// emailResendsPerRequest — «отправить ещё раз» на один запрос; emailSendsPerDay — писем со ссылкой
	// на новые адреса в сутки на пользователя (запросы + повторы): через нас нельзя засыпать чужой ящик.
	emailResendsPerRequest = 3
	emailSendsPerDay       = 10
	emailResendPeriod      = time.Minute
	// emailRequestsPerHour — запросов смены почты на пользователя в час.
	emailRequestsPerHour = 5
)

var errEmailChangeGone = errors.New("email change gone")

func newToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	return raw, auth.HashToken(raw), nil
}

// validEmail — адрес в нижнем регистре без пробелов, или "" если это не почта.
func validEmail(s string) string {
	email := strings.ToLower(strings.TrimSpace(s))
	if a, err := netmail.ParseAddress(email); err != nil || a.Address != email || len(email) > 254 || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return ""
	}
	return email
}

// pendingEmailChange — действующий запрос пользователя (не подтверждён, не отменён, не истёк).
func pendingEmailChange(db *gorm.DB, userID uint) (*models.EmailChange, bool) {
	var ec models.EmailChange
	err := db.Where("user_id = ? AND confirmed_at IS NULL AND canceled_at IS NULL AND expires_at > ?", userID, time.Now()).
		Where("token_version = (SELECT token_version FROM users WHERE users.id = email_changes.user_id)").
		Order("id DESC").First(&ec).Error
	return &ec, err == nil
}

// CancelPendingEmailChanges гасит незавершённые смены почты пользователя — вызывается при смене и
// сбросе пароля (иначе тот, кто знал старый пароль, подтвердил бы смену и забрал аккаунт).
func CancelPendingEmailChanges(tx *gorm.DB, userID uint, now time.Time) error {
	return tx.Model(&models.EmailChange{}).
		Where("user_id = ? AND confirmed_at IS NULL AND canceled_at IS NULL", userID).
		Update("canceled_at", now).Error
}

// sentToday — писем со ссылкой на новые адреса за сутки.
func sentToday(db *gorm.DB, userID uint) int64 {
	var n int64
	db.Model(&models.EmailChange{}).Select("COALESCE(SUM(send_count), 0)").
		Where("user_id = ? AND created_at > ?", userID, time.Now().Add(-24*time.Hour)).Scan(&n)
	return n
}

// unusablePasswordHash — пароль, которым нельзя войти: новый задают через «Забыли пароль?».
func unusablePasswordHash() (string, error) {
	raw, _, err := newToken()
	if err != nil {
		return "", err
	}
	return auth.HashPassword(raw)
}

func pendingView(ec *models.EmailChange) gin.H {
	return gin.H{"pending": gin.H{"email": ec.NewEmail, "expires_at": ec.ExpiresAt, "sent_at": ec.SentAt}, "mail_configured": mail.Configured()}
}

// sendEmailChangeMails — письмо на новый адрес (ссылка или «уже используется») и, если cancelRaw задан,
// уведомление на старый.
func (h *AuthHandler) sendEmailChangeMails(c *gin.Context, user *models.User, ec *models.EmailChange, raw, cancelRaw string) {
	origin := webOrigin(c)
	var taken int64
	h.db.Model(&models.User{}).Where("LOWER(email) = ? AND id <> ?", ec.NewEmail, user.ID).Count(&taken)
	if taken > 0 {
		mail.SendAsync(mail.EmailChangeTaken(ec.NewEmail, user.Locale, origin))
	} else {
		mail.SendAsync(mail.EmailChangeConfirm(ec.NewEmail, user.Locale, origin+"/confirm-email?token="+url.QueryEscape(raw)))
	}
	if cancelRaw != "" {
		mail.SendAsync(mail.EmailChangeRequested(user.Email, user.Locale, mail.MaskEmail(ec.NewEmail),
			origin+"/cancel-email?token="+url.QueryEscape(cancelRaw)))
	}
}

// RequestEmailChange — POST /auth/email {current_password, new_email}.
func (h *AuthHandler) RequestEmailChange(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var body struct {
		Current string `json:"current_password" binding:"required"`
		Email   string `json:"new_email"        binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "code": "bad_request"})
		return
	}
	email := validEmail(body.Email)
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email", "code": "email_invalid"})
		return
	}
	if err := auth.CheckPassword(body.Current, user.PasswordHash); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "wrong current password", "code": "wrong_password"})
		return
	}
	if email == user.Email {
		c.JSON(http.StatusBadRequest, gin.H{"error": "this is already your email", "code": "same_email"})
		return
	}
	if prev, ok := pendingEmailChange(h.db, user.ID); ok && prev.NewEmail == email && time.Since(prev.SentAt) < emailResendPeriod {
		c.JSON(http.StatusAccepted, pendingView(prev)) // двойное нажатие — второе письмо не шлём
		return
	}
	// Не больше 5 запросов в час на пользователя — чтобы через нас не засыпали письмами чужой адрес.
	var recent int64
	h.db.Model(&models.EmailChange{}).Where("user_id = ? AND created_at > ?", user.ID, time.Now().Add(-time.Hour)).Count(&recent)
	if recent >= emailRequestsPerHour || sentToday(h.db, user.ID) >= emailSendsPerDay {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many email change requests", "code": "too_many"})
		return
	}
	raw, hash, err1 := newToken()
	cancelRaw, cancelHash, err2 := newToken()
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	now := time.Now()
	ec := models.EmailChange{UserID: user.ID, NewEmail: email, TokenHash: hash, CancelTokenHash: cancelHash,
		ExpiresAt: now.Add(emailChangeTTL), SentAt: now, TokenVersion: user.TokenVersion, SendCount: 1}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		// Новый запрос гасит прежний.
		if err := CancelPendingEmailChanges(tx, user.ID, now); err != nil {
			return err
		}
		return tx.Create(&ec).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	h.sendEmailChangeMails(c, user, &ec, raw, cancelRaw)
	c.JSON(http.StatusAccepted, pendingView(&ec))
}

// GetEmailChange — GET /auth/email: что ждёт подтверждения.
func (h *AuthHandler) GetEmailChange(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	if ec, ok := pendingEmailChange(h.db, user.ID); ok {
		c.JSON(http.StatusOK, pendingView(ec))
		return
	}
	c.JSON(http.StatusOK, gin.H{"pending": nil, "mail_configured": mail.Configured()})
}

// ResendEmailChange — POST /auth/email/resend: новая ссылка на новый адрес (прежняя перестаёт работать),
// не чаще раза в минуту и не больше emailResendsPerRequest раз; срок ссылки не продлевается.
func (h *AuthHandler) ResendEmailChange(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	ec, ok := pendingEmailChange(h.db, user.ID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "no pending email change", "code": "no_pending"})
		return
	}
	if wait := emailResendPeriod - time.Since(ec.SentAt); wait > 0 {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too soon", "code": "too_soon", "retry_after": int(wait.Seconds()) + 1})
		return
	}
	if ec.SendCount > emailResendsPerRequest || sentToday(h.db, user.ID) >= emailSendsPerDay {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many emails", "code": "too_many"})
		return
	}
	raw, hash, err := newToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	now := time.Now()
	if err := h.db.Model(ec).Updates(map[string]any{"token_hash": hash, "sent_at": now, "send_count": gorm.Expr("send_count + 1")}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	ec.SentAt = now
	h.sendEmailChangeMails(c, user, ec, raw, "")
	c.JSON(http.StatusOK, pendingView(ec))
}

// CancelOwnEmailChange — DELETE /auth/email: отменить свой запрос из профиля.
func (h *AuthHandler) CancelOwnEmailChange(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	_ = CancelPendingEmailChanges(h.db, user.ID, time.Now())
	c.Status(http.StatusNoContent)
}

type emailTokenBody struct {
	Token string `json:"token" binding:"required"`
}

// findEmailChange — действующий запрос по токену подтверждения или отмены.
// Незавершённая смена действует, только пока версия токена пользователя та же, что при запросе.
func (h *AuthHandler) findEmailChange(db *gorm.DB, column, raw string) (*models.EmailChange, bool) {
	var ec models.EmailChange
	err := db.Where(column+" = ? AND confirmed_at IS NULL AND canceled_at IS NULL AND expires_at > ?", auth.HashToken(raw), time.Now()).
		Where("token_version = (SELECT token_version FROM users WHERE users.id = email_changes.user_id)").
		First(&ec).Error
	return &ec, err == nil
}

// findRevert — подтверждённая смена, которую ещё можно откатить по ссылке со старого адреса.
func (h *AuthHandler) findRevert(db *gorm.DB, raw string) (*models.EmailChange, bool) {
	var ec models.EmailChange
	err := db.Where("revert_token_hash = ? AND reverted_at IS NULL AND revert_expires_at > ?", auth.HashToken(raw), time.Now()).
		First(&ec).Error
	return &ec, err == nil
}

// CheckEmailChange — POST /auth/email/check {token, kind: confirm|cancel}: проверка ссылки до действий.
func (h *AuthHandler) CheckEmailChange(c *gin.Context) {
	var body struct {
		Token string `json:"token" binding:"required"`
		Kind  string `json:"kind"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token is required"})
		return
	}
	if body.Kind == "revert" {
		ec, ok := h.findRevert(h.db, body.Token)
		if !ok {
			c.JSON(http.StatusGone, gin.H{"error": "link expired or already used", "code": "link_used"})
			return
		}
		// Ссылку открыл владелец старого адреса — ему показываем его адрес и новый частично.
		c.JSON(http.StatusOK, gin.H{"old_email": ec.OldEmail, "new_email": mail.MaskEmail(ec.NewEmail)})
		return
	}
	column := "token_hash"
	if body.Kind == "cancel" {
		column = "cancel_token_hash"
	}
	ec, ok := h.findEmailChange(h.db, column, body.Token)
	if !ok {
		c.JSON(http.StatusGone, gin.H{"error": "link expired or already used", "code": "link_used"})
		return
	}
	var taken int64
	h.db.Model(&models.User{}).Where("LOWER(email) = ? AND id <> ?", ec.NewEmail, ec.UserID).Count(&taken)
	if column == "token_hash" && taken > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered", "code": "email_taken"})
		return
	}
	// Ссылку подтверждения открыл владелец нового ящика — адрес показываем целиком; ссылку отмены —
	// владелец старого, ему новый адрес только частично (как в письме).
	shown := ec.NewEmail
	if column == "cancel_token_hash" {
		shown = mail.MaskEmail(ec.NewEmail)
	}
	c.JSON(http.StatusOK, gin.H{"new_email": shown, "expires_at": ec.ExpiresAt})
}

// endSessions — все входы пользователя завершаются (версия токена + отзыв refresh-токенов).
func endSessions(tx *gorm.DB, userID uint, now time.Time) error {
	if err := tx.Model(&models.User{}).Where("id = ?", userID).Update("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
		return err
	}
	return tx.Model(&models.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error
}

// ConfirmEmailChange — POST /auth/email/confirm {token}.
func (h *AuthHandler) ConfirmEmailChange(c *gin.Context) {
	var body emailTokenBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token is required"})
		return
	}
	var user models.User
	var ec *models.EmailChange
	var oldEmail string
	errTaken := errors.New("taken")
	revertRaw, revertHash, err := newToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		var ok bool
		if ec, ok = h.findEmailChange(tx, "token_hash", body.Token); !ok {
			return errEmailChangeGone
		}
		now := time.Now()
		// Ссылку «забираем» условным UPDATE: два одновременных перехода не пройдут.
		claim := tx.Model(&models.EmailChange{}).Where("id = ? AND confirmed_at IS NULL AND canceled_at IS NULL", ec.ID).
			Update("confirmed_at", now)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return errEmailChangeGone
		}
		var taken int64
		tx.Model(&models.User{}).Where("LOWER(email) = ? AND id <> ?", ec.NewEmail, ec.UserID).Count(&taken)
		if taken > 0 {
			return errTaken
		}
		if err := tx.First(&user, ec.UserID).Error; err != nil {
			return errEmailChangeGone
		}
		oldEmail = user.Email
		if err := tx.Model(&user).Update("email", ec.NewEmail).Error; err != nil {
			return errTaken // уникальный индекс: адрес заняли в эту же секунду
		}
		revertUntil := now.Add(emailRevertTTL)
		if err := tx.Model(&models.EmailChange{}).Where("id = ?", ec.ID).Updates(map[string]any{
			"old_email": oldEmail, "revert_token_hash": revertHash, "revert_expires_at": revertUntil}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND used_at IS NULL", user.ID).Delete(&models.PasswordReset{}).Error; err != nil {
			return err
		}
		return endSessions(tx, user.ID, now)
	})
	switch {
	case errors.Is(err, errEmailChangeGone):
		c.JSON(http.StatusGone, gin.H{"error": "link expired or already used", "code": "link_used"})
		return
	case errors.Is(err, errTaken):
		// Запрос больше не действует: адрес занят.
		h.db.Model(&models.EmailChange{}).Where("id = ?", ec.ID).Update("canceled_at", time.Now())
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered", "code": "email_taken"})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	h.db.Model(&models.EmailChange{}).Where("user_id = ? AND id <> ? AND confirmed_at IS NULL AND canceled_at IS NULL", user.ID, ec.ID).
		Update("canceled_at", time.Now())
	mail.SendAsync(mail.EmailChanged(oldEmail, user.Locale, mail.MaskEmail(ec.NewEmail),
		webOrigin(c)+"/revert-email?token="+url.QueryEscape(revertRaw)))
	c.JSON(http.StatusOK, gin.H{"email": ec.NewEmail})
}

// CancelEmailChange — POST /auth/email/cancel {token}: «это не я» из письма на старый адрес.
func (h *AuthHandler) CancelEmailChange(c *gin.Context) {
	var body emailTokenBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token is required"})
		return
	}
	unusable, err := unusablePasswordHash()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash error"})
		return
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		ec, ok := h.findEmailChange(tx, "cancel_token_hash", body.Token)
		if !ok {
			return errEmailChangeGone
		}
		now := time.Now()
		claim := tx.Model(&models.EmailChange{}).Where("id = ? AND confirmed_at IS NULL AND canceled_at IS NULL", ec.ID).
			Update("canceled_at", now)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return errEmailChangeGone
		}
		// Запрос сделал кто-то, кто знает пароль: пароль сбрасываем, новый — через «Забыли пароль?»
		// (письмо придёт на прежнюю, то есть свою почту).
		if err := tx.Model(&models.User{}).Where("id = ?", ec.UserID).Update("password_hash", unusable).Error; err != nil {
			return err
		}
		return endSessions(tx, ec.UserID, now)
	})
	if errors.Is(err, errEmailChangeGone) {
		c.JSON(http.StatusGone, gin.H{"error": "link expired or already used", "code": "link_used"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RevertEmailChange — POST /auth/email/revert {token}: «это не я» из письма «почта изменена».
// Почта возвращается, все сессии завершаются, пароль сбрасывается, все прочие ссылки отката гаснут.
func (h *AuthHandler) RevertEmailChange(c *gin.Context) {
	var body emailTokenBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token is required"})
		return
	}
	unusable, err := unusablePasswordHash()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash error"})
		return
	}
	errTaken := errors.New("taken")
	var restored string
	err = h.db.Transaction(func(tx *gorm.DB) error {
		ec, ok := h.findRevert(tx, body.Token)
		if !ok {
			return errEmailChangeGone
		}
		now := time.Now()
		claim := tx.Model(&models.EmailChange{}).Where("id = ? AND reverted_at IS NULL", ec.ID).Update("reverted_at", now)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return errEmailChangeGone
		}
		var taken int64
		tx.Model(&models.User{}).Where("LOWER(email) = ? AND id <> ?", ec.OldEmail, ec.UserID).Count(&taken)
		if taken > 0 {
			return errTaken
		}
		if err := tx.Model(&models.User{}).Where("id = ?", ec.UserID).
			Updates(map[string]any{"email": ec.OldEmail, "password_hash": unusable}).Error; err != nil {
			return errTaken
		}
		// Остальные ссылки отката и незавершённые смены этого пользователя больше не действуют.
		if err := tx.Model(&models.EmailChange{}).Where("user_id = ? AND reverted_at IS NULL AND revert_token_hash IS NOT NULL", ec.UserID).
			Update("revert_expires_at", now).Error; err != nil {
			return err
		}
		if err := CancelPendingEmailChanges(tx, ec.UserID, now); err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND used_at IS NULL", ec.UserID).Delete(&models.PasswordReset{}).Error; err != nil {
			return err
		}
		restored = ec.OldEmail
		return endSessions(tx, ec.UserID, now)
	})
	switch {
	case errors.Is(err, errEmailChangeGone):
		c.JSON(http.StatusGone, gin.H{"error": "link expired or already used", "code": "link_used"})
	case errors.Is(err, errTaken):
		c.JSON(http.StatusConflict, gin.H{"error": "the old email is now used by another account", "code": "email_taken"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
	default:
		c.JSON(http.StatusOK, gin.H{"email": restored})
	}
}
