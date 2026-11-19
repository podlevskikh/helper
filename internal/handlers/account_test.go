package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
)

// Интеграционный тест: нужна отдельная пустая база Postgres, например
//
//	createdb helper_account_deletion
//	TEST_DATABASE_URL=postgres://helper:helper_password@localhost:5432/helper_account_deletion?sslmode=disable go test ./internal/handlers -run DeleteMe
//
// Без TEST_DATABASE_URL тест пропускается.
func TestDeleteMe(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB") == "1" {
			t.Fatal("TEST_DATABASE_URL не задан, а REQUIRE_DB=1")
		}
		t.Skip("TEST_DATABASE_URL не задан")
	}
	t.Setenv("SMTP_HOST", "") // письма не отправляем
	if err := database.Initialize(url); err != nil {
		t.Fatal(err)
	}
	db := database.DB
	gin.SetMode(gin.TestMode)
	h := NewAuthHandler(db)
	r := gin.New()
	r.POST("/auth/register", h.Register)
	r.DELETE("/auth/me", middleware.Auth(), h.DeleteMe)

	suffix := fmt.Sprint(time.Now().UnixNano())
	call := func(method, path, token string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Наниматель регистрируется и получает свой дом.
	w := call("POST", "/auth/register", "", map[string]string{
		"email": "owner-" + suffix + "@example.test", "password": "owner-pass-1", "org_name": "Test home " + suffix,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	var reg struct {
		Access string      `json:"access"`
		User   models.User `json:"user"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &reg)
	var orgID uint
	db.Model(&models.Membership{}).Where("user_id = ?", reg.User.ID).Pluck("organization_id", &orgID)

	// Два сотрудника в этом доме.
	helper := mkUser(t, db, "helper-"+suffix, orgID)
	other := mkUser(t, db, "other-"+suffix, orgID)

	// Данные дома: рецепт, зона, задача на сотрудника (со связями), повторяющаяся задача,
	// список покупок, особый день, устройство для пушей.
	recipe := models.Recipe{OrganizationID: orgID, Name: "Борщ"}
	zone := models.CleaningZone{OrganizationID: orgID, Name: "Кухня"}
	must(t, db.Create(&recipe).Error, db.Create(&zone).Error)
	day := models.DailySchedule{OrganizationID: orgID, Date: time.Now()}
	must(t, db.Create(&day).Error)
	task := models.ScheduleTask{OrganizationID: orgID, ScheduleID: day.ID, TaskType: "custom", Title: "Обед", AssignedToUserID: &helper.ID}
	must(t, db.Create(&task).Error)
	must(t,
		db.Model(&task).Association("Recipes").Append(&recipe),
		db.Model(&task).Association("Zones").Append(&zone),
		db.Create(&models.RecurringTask{OrganizationID: orgID, Title: "Пыль", Weekdays: "1", StartDate: time.Now(), AssignedToUserID: &helper.ID}).Error,
		db.Create(&models.StaffDayOverride{OrganizationID: orgID, UserID: helper.ID, Kind: "day_off", StartDate: time.Now(), EndDate: time.Now()}).Error,
		db.Create(&models.PushDevice{UserID: helper.ID, Token: "ExponentPushToken[" + suffix + "]", LastSeenAt: time.Now()}).Error,
	)
	list := models.ShoppingList{OrganizationID: orgID, ListDate: "2026-11-19", CreatedByUserID: helper.ID}
	must(t, db.Create(&list).Error)
	must(t, db.Create(&models.ShoppingListItem{OrganizationID: orgID, Item: "Молоко", ListID: &list.ID, AddedByUserID: &helper.ID}).Error)

	helperToken, _ := auth.GenerateAccessToken(helper.ID, 0)

	t.Run("wrong password", func(t *testing.T) {
		if w := call("DELETE", "/auth/me", helperToken, map[string]string{"password": "nope"}); w.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d", w.Code)
		}
		if count(db, &models.User{}, "id = ?", helper.ID) != 1 {
			t.Fatal("user deleted with wrong password")
		}
	})

	t.Run("helper", func(t *testing.T) {
		if w := call("DELETE", "/auth/me", helperToken, map[string]string{"password": "secret-pass"}); w.Code != http.StatusNoContent {
			t.Fatalf("want 204, got %d %s", w.Code, w.Body)
		}
		for name, n := range map[string]int64{
			"user":        count(db, &models.User{}, "id = ?", helper.ID),
			"membership":  count(db, &models.Membership{}, "user_id = ?", helper.ID),
			"override":    count(db, &models.StaffDayOverride{}, "user_id = ?", helper.ID),
			"push device": count(db, &models.PushDevice{}, "user_id = ?", helper.ID),
			"assigned":    count(db, &models.ScheduleTask{}, "assigned_to_user_id = ?", helper.ID),
		} {
			if n != 0 {
				t.Errorf("%s: осталось %d", name, n)
			}
		}
		// Данные дома остаются у нанимателя.
		if count(db, &models.ScheduleTask{}, "id = ?", task.ID) != 1 || count(db, &models.ShoppingListItem{}, "list_id = ?", list.ID) != 1 {
			t.Error("данные дома удалились вместе с сотрудником")
		}
	})

	t.Run("owner", func(t *testing.T) {
		if w := call("DELETE", "/auth/me", reg.Access, map[string]string{"password": "owner-pass-1"}); w.Code != http.StatusNoContent {
			t.Fatalf("want 204, got %d %s", w.Code, w.Body)
		}
		for name, n := range map[string]int64{
			"owner":       count(db, &models.User{}, "id = ?", reg.User.ID),
			"org":         count(db, &models.Organization{}, "id = ?", orgID),
			"memberships": count(db, &models.Membership{}, "organization_id = ?", orgID),
			"tasks":       count(db, &models.ScheduleTask{}, "organization_id = ?", orgID),
			"schedules":   count(db, &models.DailySchedule{}, "organization_id = ?", orgID),
			"recurring":   count(db, &models.RecurringTask{}, "organization_id = ?", orgID),
			"recipes":     count(db, &models.Recipe{}, "organization_id = ?", orgID),
			"zones":       count(db, &models.CleaningZone{}, "organization_id = ?", orgID),
			"categories":  count(db, &models.TaskCategory{}, "organization_id = ?", orgID),
			"lists":       count(db, &models.ShoppingList{}, "organization_id = ?", orgID),
			"items":       count(db, &models.ShoppingListItem{}, "organization_id = ?", orgID),
		} {
			if n != 0 {
				t.Errorf("%s: осталось %d", name, n)
			}
		}
		var links int64
		db.Raw("SELECT COUNT(*) FROM meal_recipes WHERE schedule_task_id = ?", task.ID).Scan(&links)
		if links != 0 {
			t.Errorf("meal_recipes: осталось %d", links)
		}
		// Второй сотрудник остаётся, но без дома.
		if count(db, &models.User{}, "id = ?", other.ID) != 1 {
			t.Error("аккаунт сотрудника удалился вместе с домом нанимателя")
		}
	})
}

func mkUser(t *testing.T, db *gorm.DB, name string, orgID uint) models.User {
	t.Helper()
	hash, _ := auth.HashPassword("secret-pass")
	u := models.User{Email: name + "@example.test", PasswordHash: hash, Name: name}
	must(t, db.Create(&u).Error)
	must(t, db.Create(&models.Membership{UserID: u.ID, OrganizationID: orgID, Role: models.RoleHelper, Status: models.MembershipActive}).Error)
	return u
}

func must(t *testing.T, errs ...error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func count(db *gorm.DB, model any, where string, args ...any) int64 {
	var n int64
	db.Model(model).Where(where, args...).Count(&n)
	return n
}
