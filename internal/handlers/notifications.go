package handlers

import (
	"net/http"
	"os"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/notify"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// NotificationsHandler — устройства для пушей и настройки уведомлений.
type NotificationsHandler struct {
	db *gorm.DB
}

func NewNotificationsHandler(db *gorm.DB) *NotificationsHandler {
	return &NotificationsHandler{db: db}
}

type registerDeviceRequest struct {
	Token      string `json:"token"`
	Platform   string `json:"platform"`
	DeviceName string `json:"device_name"`
	Locale     string `json:"locale"`
	Timezone   string `json:"timezone"`
	AppVersion string `json:"app_version"`
}

// RegisterDevice — POST /me/devices. Токен один на устройство: если им уже
// пользовался другой аккаунт, устройство переходит к текущему.
func (h *NotificationsHandler) RegisterDevice(c *gin.Context) {
	var req registerDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil || !strings.HasPrefix(req.Token, "ExponentPushToken[") && !strings.HasPrefix(req.Token, "ExpoPushToken[") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expo push token required"})
		return
	}
	if req.Platform != "ios" && req.Platform != "android" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "platform must be ios or android"})
		return
	}
	uid := currentUserID(c)
	now := time.Now()
	var d models.PushDevice
	if h.db.Where("token = ?", req.Token).First(&d).Error != nil {
		d = models.PushDevice{Token: req.Token}
	} else if d.UserID != uid && d.DisabledAt == nil && now.Sub(d.LastSeenAt) < 30*24*time.Hour {
		// Токен уже у другого активного пользователя: перехват запрещён. На общем телефоне приложение
		// при выходе отвязывает токен (DELETE /me/devices/:token), и тогда новый вход его заберёт.
		c.JSON(http.StatusConflict, gin.H{"error": "device registered to another account", "code": "device_taken"})
		return
	}
	d.UserID = uid
	d.Platform = req.Platform
	d.DeviceName = clipStr(req.DeviceName, 80)
	d.Locale = clipStr(req.Locale, 16)
	d.Timezone = clipStr(req.Timezone, 64)
	d.AppVersion = clipStr(req.AppVersion, 32)
	d.LastSeenAt = now
	d.DisabledAt = nil
	if err := h.db.Save(&d).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": d.ID})
}

// UnregisterDevice — DELETE /me/devices/:token (при выходе из аккаунта).
func (h *NotificationsHandler) UnregisterDevice(c *gin.Context) {
	h.db.Where("token = ? AND user_id = ?", c.Param("token"), currentUserID(c)).Delete(&models.PushDevice{})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GetPrefs — GET /orgs/:orgId/notification-prefs.
func (h *NotificationsHandler) GetPrefs(c *gin.Context) {
	m := middleware.MustMembership(c)
	var devices int64
	h.db.Model(&models.PushDevice{}).Where("user_id = ? AND disabled_at IS NULL", m.UserID).Count(&devices)
	c.JSON(http.StatusOK, gin.H{
		"prefs":   notify.LoadPrefs(h.db, m.UserID, m.OrganizationID),
		"devices": devices,
	})
}

// UpdatePrefs — PUT /orgs/:orgId/notification-prefs (своё, прав не нужно).
func (h *NotificationsHandler) UpdatePrefs(c *gin.Context) {
	m := middleware.MustMembership(c)
	p := notify.LoadPrefs(h.db, m.UserID, m.OrganizationID)
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if p.Disabled == nil {
		p.Disabled = []string{}
	}
	if err := p.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := notify.SavePrefs(h.db, m.UserID, m.OrganizationID, p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.GetPrefs(c)
}

// SendTest — POST /orgs/:orgId/notification-prefs/test: пробный пуш себе.
// Только вне продакшена (GIN_MODE != release) или при PUSH_TEST_ENABLED=1.
func (h *NotificationsHandler) SendTest(c *gin.Context) {
	if os.Getenv("GIN_MODE") == "release" && os.Getenv("PUSH_TEST_ENABLED") != "1" {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	m := middleware.MustMembership(c)
	notify.Enqueue(h.db, notify.Item{OrgID: m.OrganizationID, UserID: m.UserID, Kind: notify.KindTest})
	c.JSON(http.StatusAccepted, gin.H{"queued": true})
}

func clipStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// EnsureDay достраивает день дома для пушей по времени: повторяющиеся задачи
// появляются в расписании, даже если сегодня приложение ещё никто не открывал.
func EnsureDay(db *gorm.DB) func(orgID uint, day time.Time) {
	return func(orgID uint, day time.Time) {
		if len(recurringFor(db, orgID, day)) == 0 {
			return
		}
		from, to := day.Add(-12*time.Hour), day.Add(12*time.Hour)
		var schedule models.DailySchedule
		if db.Where("organization_id = ? AND date >= ? AND date < ?", orgID, from, to).First(&schedule).Error != nil {
			var err error
			if schedule, err = models.EnsureDailySchedule(db, orgID, utcDay(day), false); err != nil {
				return
			}
		}
		materializeRecurring(db, orgID, &schedule, day)
	}
}
