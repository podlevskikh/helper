package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/models"
)

// DeleteMe — DELETE /auth/me {password, successor_id?, delete_home?}: удалить свой аккаунт навсегда
// (App Store, Guideline 5.1.1(v)). Пароль спрашиваем ещё раз, чтобы удаление не случилось по ошибке.
//
// Основной админ решает, что будет с домом, где есть кто-то ещё:
//   - successor_id — передать роль этому админу, дом остаётся;
//   - delete_home  — удалить дом целиком (у остальных пропадает доступ, аккаунты остаются);
//   - ни того ни другого — 409 home_decision_required со списком админов, кому можно передать.
//
// Дом, где больше никого нет, удаляется вместе с аккаунтом. Остальные просто уходят из команды:
// их задачи остаются без исполнителя.
func (h *AuthHandler) DeleteMe(c *gin.Context) {
	user, ok := h.currentUser(c)
	if !ok {
		return
	}
	var body struct {
		Password    string `json:"password" binding:"required"`
		SuccessorID uint   `json:"successor_id"`
		DeleteHome  bool   `json:"delete_home"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}
	if err := auth.CheckPassword(body.Password, user.PasswordHash); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "wrong password"})
		return
	}
	var pending []gin.H
	err := h.db.Transaction(func(tx *gorm.DB) error {
		homes, err := homesNeedingDecision(tx, user.ID)
		if err != nil {
			return err
		}
		for _, home := range homes {
			switch {
			case body.DeleteHome:
				// дом удалит DeleteAccount: пользователь в нём всё ещё основной админ
			case body.SuccessorID != 0 && home.hasAdmin(body.SuccessorID):
				if err := transferOwnership(tx, home.OrgID, user.ID, body.SuccessorID); err != nil {
					return err
				}
			default:
				pending = append(pending, home.view())
			}
		}
		if len(pending) > 0 {
			return errDecisionRequired
		}
		return DeleteAccount(tx, user.ID)
	})
	if errors.Is(err, errTeamChanged) || errors.Is(err, errNotAdmin) {
		c.JSON(http.StatusConflict, gin.H{"error": "the team has changed, reload", "code": "team_changed"})
		return
	}
	if errors.Is(err, errDecisionRequired) {
		c.JSON(http.StatusConflict, gin.H{"error": "decide what happens to the home", "code": "home_decision_required", "homes": pending})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.Status(http.StatusNoContent)
}

var errDecisionRequired = errors.New("home decision required")

type homeDecision struct {
	OrgID   uint
	OrgName string
	Admins  []models.Membership
	Others  int64 // сколько ещё людей в команде
	names   map[uint]string
}

func (d homeDecision) hasAdmin(userID uint) bool {
	for _, a := range d.Admins {
		if a.UserID == userID {
			return true
		}
	}
	return false
}

func (d homeDecision) view() gin.H {
	admins := make([]gin.H, 0, len(d.Admins))
	for _, a := range d.Admins {
		admins = append(admins, gin.H{"user_id": a.UserID, "name": d.names[a.UserID]})
	}
	return gin.H{"org_id": d.OrgID, "org_name": d.OrgName, "admins": admins, "members": d.Others}
}

// homesNeedingDecision — дома, где userID основной админ и в команде есть кто-то ещё.
func homesNeedingDecision(tx *gorm.DB, userID uint) ([]homeDecision, error) {
	var owned []models.Membership
	if err := tx.Where("user_id = ? AND role = ? AND status = ?", userID, models.RoleOwner, models.MembershipActive).Find(&owned).Error; err != nil {
		return nil, err
	}
	var out []homeDecision
	for _, o := range owned {
		d := homeDecision{OrgID: o.OrganizationID, names: map[uint]string{}}
		active := tx.Model(&models.Membership{}).Where("organization_id = ? AND status = ? AND user_id <> ?", o.OrganizationID, models.MembershipActive, userID)
		if err := active.Count(&d.Others).Error; err != nil {
			return nil, err
		}
		if d.Others == 0 {
			continue
		}
		if err := tx.Where("organization_id = ? AND status = ? AND role = ? AND user_id <> ?", o.OrganizationID, models.MembershipActive, models.RoleAdmin, userID).
			Order("created_at ASC").Find(&d.Admins).Error; err != nil {
			return nil, err
		}
		for _, a := range d.Admins {
			var u models.User
			tx.First(&u, a.UserID)
			d.names[a.UserID] = memberView(a, u).Name
		}
		var org models.Organization
		tx.First(&org, o.OrganizationID)
		d.OrgName = org.Name
		out = append(out, d)
	}
	return out, nil
}

// DeleteAccount удаляет пользователя и всё, что ему принадлежит. Вызывать внутри транзакции.
func DeleteAccount(tx *gorm.DB, userID uint) error {
	// Дома, которыми владеет пользователь, удаляются целиком — если в доме нет другого владельца.
	// Владельца определяем по членству с ролью owner — так же, как права в middleware.
	var owned []uint
	err := tx.Model(&models.Membership{}).
		Where("user_id = ? AND role = ?", userID, models.RoleOwner).
		Where("NOT EXISTS (SELECT 1 FROM memberships o WHERE o.organization_id = memberships.organization_id AND o.role = ? AND o.user_id <> ?)", models.RoleOwner, userID).
		Pluck("organization_id", &owned).Error
	if err != nil {
		return err
	}
	for _, orgID := range owned {
		if err := deleteOrganization(tx, orgID); err != nil {
			return err
		}
	}

	// В чужих домах: убрать имя из задач и списков, удалить личное.
	steps := []func() error{
		func() error {
			return tx.Model(&models.ScheduleTask{}).Where("assigned_to_user_id = ?", userID).Update("assigned_to_user_id", nil).Error
		},
		func() error {
			return tx.Model(&models.RecurringTask{}).Where("assigned_to_user_id = ?", userID).Update("assigned_to_user_id", nil).Error
		},
		func() error {
			return tx.Model(&models.ShoppingListItem{}).Where("added_by_user_id = ?", userID).Update("added_by_user_id", nil).Error
		},
		func() error {
			return tx.Model(&models.ShoppingList{}).Where("closed_by_user_id = ?", userID).Update("closed_by_user_id", nil).Error
		},
		func() error {
			return tx.Model(&models.ShoppingList{}).Where("created_by_user_id = ?", userID).Update("created_by_user_id", 0).Error
		},
		func() error {
			return tx.Model(&models.Membership{}).Where("invited_by = ?", userID).Update("invited_by", nil).Error
		},
		func() error { return tx.Where("user_id = ?", userID).Delete(&models.StaffDayOverride{}).Error },
		func() error {
			return tx.Where("notification_id IN (?)", tx.Model(&models.Notification{}).Select("id").Where("user_id = ?", userID)).
				Delete(&models.PushTicket{}).Error
		},
		func() error {
			return tx.Where("device_id IN (?)", tx.Model(&models.PushDevice{}).Select("id").Where("user_id = ?", userID)).
				Delete(&models.PushTicket{}).Error
		},
		func() error { return tx.Where("user_id = ?", userID).Delete(&models.Notification{}).Error },
		func() error { return tx.Where("user_id = ?", userID).Delete(&models.NotificationPref{}).Error },
		func() error { return tx.Where("user_id = ?", userID).Delete(&models.PushDevice{}).Error },
		func() error {
			return tx.Where("invited_by = ? AND status = ?", userID, models.InvitePending).Delete(&models.Invite{}).Error
		},
		func() error { return tx.Where("user_id = ?", userID).Delete(&models.Membership{}).Error },
		func() error { return tx.Where("user_id = ?", userID).Delete(&models.RefreshToken{}).Error },
		func() error { return tx.Where("user_id = ?", userID).Delete(&models.PasswordReset{}).Error },
		func() error { return tx.Where("user_id = ?", userID).Delete(&models.EmailChange{}).Error },
		func() error { return tx.Delete(&models.User{}, userID).Error },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// deleteOrganization удаляет дом и все его данные. Порядок важен: сначала связи и дочерние
// таблицы, потом то, на что они ссылаются (задачи → рецепты, зоны, категории).
func deleteOrganization(tx *gorm.DB, orgID uint) error {
	inOrg := func(model any) *gorm.DB {
		return tx.Model(model).Select("id").Where("organization_id = ?", orgID)
	}
	tasks := inOrg(&models.ScheduleTask{})
	recipes := inOrg(&models.Recipe{})
	mealTimes := inOrg(&models.MealTime{})
	zones := inOrg(&models.CleaningZone{})

	// Таблицы связей many2many (без моделей).
	joins := []struct {
		table, where string
		arg          *gorm.DB
	}{
		{"meal_recipes", "schedule_task_id IN (?)", tasks},
		{"meal_recipes", "recipe_id IN (?)", recipes},
		{"task_zones", "schedule_task_id IN (?)", tasks},
		{"task_zones", "cleaning_zone_id IN (?)", zones},
		{"recipe_meal_times", "recipe_id IN (?)", recipes},
		{"recipe_meal_times", "meal_time_id IN (?)", mealTimes},
	}
	for _, j := range joins {
		if err := tx.Exec("DELETE FROM "+j.table+" WHERE "+j.where, j.arg).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("recurring_task_id IN (?)", inOrg(&models.RecurringTask{})).Delete(&models.RecurringTaskSkip{}).Error; err != nil {
		return err
	}
	if err := tx.Where("notification_id IN (?)", inOrg(&models.Notification{})).Delete(&models.PushTicket{}).Error; err != nil {
		return err
	}

	scoped := []any{
		&models.Notification{},
		&models.NotificationPref{},
		&models.StaffDayOverride{},
		&models.RecipeComment{},
		&models.ShoppingListItem{},
		&models.ShoppingList{},
		&models.ScheduleTask{},
		&models.DailySchedule{},
		&models.RecurringTask{},
		&models.TaskCategory{},
		&models.ChildcareSchedule{},
		&models.CleaningZone{},
		&models.MealTime{},
		&models.Recipe{},
		&models.Settings{},
		&models.Invite{},
		&models.Membership{},
	}
	for _, m := range scoped {
		if err := tx.Where("organization_id = ?", orgID).Delete(m).Error; err != nil {
			return err
		}
	}
	return tx.Delete(&models.Organization{}, orgID).Error
}
