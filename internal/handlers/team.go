package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
)

// TeamHandler — команда дома: роль и имя человека, убрать из команды, покинуть команду,
// передать роль основного админа (owner) другому админу.
//
// Правила (docs/brand/voice.md, роли):
//   - основной админ один; его нельзя убрать, понизить или пригласить — только передать роль;
//   - свою роль не меняют и себя не убирают: для этого «Покинуть команду»;
//   - основной админ покинуть команду не может — сначала передаёт роль или удаляет дом.
type TeamHandler struct {
	db *gorm.DB
}

func NewTeamHandler(db *gorm.DB) *TeamHandler {
	return &TeamHandler{db: db}
}

const maxTeamName = 80

// cleanTeamName — имя человека в команде: без лишних пробелов, не длиннее maxTeamName символов,
// без управляющих и невидимых символов (NUL ломает запрос к базе, смена направления текста и
// символы нулевой ширины позволяют подделать, как имя выглядит у других).
func cleanTeamName(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return s, utf8.RuneCountInString(s) <= maxTeamName
}

// activeMember — активное членство userId в доме orgID; иначе 404.
func (h *TeamHandler) activeMember(c *gin.Context, orgID uint) (*models.Membership, bool) {
	uid, err := strconv.ParseUint(c.Param("userId"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return nil, false
	}
	var t models.Membership
	if err := h.db.Where("user_id = ? AND organization_id = ? AND status = ?", uint(uid), orgID, models.MembershipActive).
		First(&t).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return nil, false
	}
	return &t, true
}

// UpdateMember — PATCH /orgs/:orgId/members/:userId {role?, name?} (manage_team).
func (h *TeamHandler) UpdateMember(c *gin.Context) {
	me := middleware.MustMembership(c)
	var body struct {
		Role *models.Role `json:"role"`
		Name *string      `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || (body.Role == nil && body.Name == nil) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role or name is required"})
		return
	}
	t, ok := h.activeMember(c, me.OrganizationID)
	if !ok {
		return
	}
	updates := map[string]any{}
	if body.Role != nil && *body.Role != t.Role {
		switch {
		case !models.IsInvitableRole(*body.Role):
			c.JSON(http.StatusBadRequest, gin.H{"error": "role must be admin, manager or helper", "code": "bad_role"})
			return
		case t.Role == models.RoleOwner:
			c.JSON(http.StatusForbidden, gin.H{"error": "the main admin's role is changed only by transfer", "code": "owner_role"})
			return
		case t.UserID == me.UserID:
			c.JSON(http.StatusBadRequest, gin.H{"error": "you can't change your own role", "code": "own_role"})
			return
		}
		updates["role"] = *body.Role
	}
	if body.Name != nil {
		name, ok := cleanTeamName(*body.Name)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name is too long", "code": "name_too_long"})
			return
		}
		updates["display_name"] = name
	}
	if len(updates) > 0 {
		// Роль могла поменяться между чтением и записью (передача роли основного админа, выход) —
		// пишем, только если человек всё ещё активен и не стал основным админом.
		q := h.db.Model(&models.Membership{}).Where("id = ? AND status = ?", t.ID, models.MembershipActive)
		if _, ok := updates["role"]; ok {
			q = q.Where("role <> ?", models.RoleOwner)
		}
		res := q.Updates(updates)
		if res.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
			return
		}
		if res.RowsAffected != 1 {
			c.JSON(http.StatusConflict, gin.H{"error": "the team has changed, reload", "code": "team_changed"})
			return
		}
	}
	h.db.First(t, t.ID)
	var u models.User
	h.db.First(&u, t.UserID)
	c.JSON(http.StatusOK, memberView(*t, u))
}

// RemoveMember — DELETE /orgs/:orgId/members/:userId (manage_team): убрать человека из команды.
// Аккаунт и сделанные задачи остаются; вернуть можно новой ссылкой.
func (h *TeamHandler) RemoveMember(c *gin.Context) {
	me := middleware.MustMembership(c)
	t, ok := h.activeMember(c, me.OrganizationID)
	if !ok {
		return
	}
	if t.Role == models.RoleOwner {
		c.JSON(http.StatusForbidden, gin.H{"error": "the main admin can't be removed", "code": "owner_role"})
		return
	}
	if t.UserID == me.UserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "use leave to leave the team", "code": "use_leave"})
		return
	}
	if !h.deactivate(c, t) {
		return
	}
	c.Status(http.StatusNoContent)
}

// deactivate — deactivateMember в транзакции с ответом об ошибке; false — ответ уже отправлен.
func (h *TeamHandler) deactivate(c *gin.Context, m *models.Membership) bool {
	err := h.db.Transaction(func(tx *gorm.DB) error { return deactivateMember(tx, m) })
	if errors.Is(err, errTeamChanged) {
		c.JSON(http.StatusConflict, gin.H{"error": "the team has changed, reload", "code": "team_changed"})
		return false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return false
	}
	return true
}

// Leave — POST /orgs/:orgId/leave: покинуть команду (все, кроме основного админа).
func (h *TeamHandler) Leave(c *gin.Context) {
	me := middleware.MustMembership(c)
	if me.Role == models.RoleOwner {
		c.JSON(http.StatusConflict, gin.H{"error": "the main admin must transfer the role or delete the home", "code": "owner_cannot_leave"})
		return
	}
	if !h.deactivate(c, me) {
		return
	}
	c.Status(http.StatusNoContent)
}

var errTeamChanged = errors.New("membership changed concurrently")

// deactivateMember — человек больше не в команде: членство выключено (по новой ссылке вернётся),
// его ссылки-приглашения отозваны, регулярные задачи и несделанные задачи с сегодняшнего дня
// остаются без исполнителя. Основного админа так не выключить — даже если он стал им только что.
func deactivateMember(tx *gorm.DB, m *models.Membership) error {
	res := tx.Model(&models.Membership{}).
		Where("id = ? AND status = ? AND role <> ?", m.ID, models.MembershipActive, models.RoleOwner).
		Update("status", models.MembershipDisabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errTeamChanged
	}
	// Ссылки, которые он создал, больше не работают — иначе мог бы вернуться по своей же ссылке.
	if err := tx.Model(&models.Invite{}).
		Where("organization_id = ? AND invited_by = ? AND status = ?", m.OrganizationID, m.UserID, models.InvitePending).
		Update("status", models.InviteRevoked).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.RecurringTask{}).
		Where("organization_id = ? AND assigned_to_user_id = ?", m.OrganizationID, m.UserID).
		Update("assigned_to_user_id", nil).Error; err != nil {
		return err
	}
	// Даты дней хранятся то с UTC-, то с местной полуночью — берём со вчерашнего дня с запасом.
	since := time.Now().AddDate(0, 0, -1)
	days := tx.Model(&models.DailySchedule{}).Select("id").Where("organization_id = ? AND date >= ?", m.OrganizationID, since)
	return tx.Model(&models.ScheduleTask{}).
		Where("organization_id = ? AND assigned_to_user_id = ? AND completed = ? AND schedule_id IN (?)", m.OrganizationID, m.UserID, false, days).
		Update("assigned_to_user_id", nil).Error
}

// TransferOwnership — POST /orgs/:orgId/transfer-ownership {user_id, password} (own_home):
// основной админ передаёт роль другому админу и сам становится админом.
func (h *TeamHandler) TransferOwnership(c *gin.Context) {
	me := middleware.MustMembership(c)
	var body struct {
		UserID   uint   `json:"user_id" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id and password are required"})
		return
	}
	var user models.User
	if err := h.db.First(&user, me.UserID).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
		return
	}
	if auth.CheckPassword(body.Password, user.PasswordHash) != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "wrong password", "code": "wrong_password"})
		return
	}
	err := h.db.Transaction(func(tx *gorm.DB) error { return transferOwnership(tx, me.OrganizationID, me.UserID, body.UserID) })
	if errors.Is(err, errNotAdmin) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the new main admin must be an admin of this home", "code": "successor_not_admin"})
		return
	}
	if errors.Is(err, errTeamChanged) {
		c.JSON(http.StatusConflict, gin.H{"error": "the team has changed, reload", "code": "team_changed"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.Status(http.StatusNoContent)
}

var errNotAdmin = errors.New("successor is not an active admin")

// transferOwnership — to (активный админ) становится основным админом, from — админом.
// Сначала понижаем from: две одновременные передачи не дадут двух основных админов —
// вторая не найдёт from основным админом и откатится. Вызывать внутри транзакции.
func transferOwnership(tx *gorm.DB, orgID, from, to uint) error {
	if from == to {
		return errNotAdmin
	}
	res := tx.Model(&models.Membership{}).
		Where("organization_id = ? AND user_id = ? AND status = ? AND role = ?", orgID, from, models.MembershipActive, models.RoleOwner).
		Update("role", models.RoleAdmin)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errTeamChanged
	}
	res = tx.Model(&models.Membership{}).
		Where("organization_id = ? AND user_id = ? AND status = ? AND role = ?", orgID, to, models.MembershipActive, models.RoleAdmin).
		Update("role", models.RoleOwner)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errNotAdmin
	}
	return nil
}
