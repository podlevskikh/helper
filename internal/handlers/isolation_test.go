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

	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/scheduler"
)

// Дома не видят и не меняют чужие данные по id, а планировщик дома 1 не трогает дни других домов.
// Нужна отдельная база: TEST_DATABASE_URL=... go test ./internal/handlers -run Isolation
func TestOrgIsolation(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	t.Setenv("SMTP_HOST", "")
	if err := database.Initialize(url); err != nil {
		t.Fatal(err)
	}
	db := database.DB
	gin.SetMode(gin.TestMode)
	admin := NewAdminHandler(db)
	r := gin.New()
	r.POST("/auth/register", NewAuthHandler(db).Register)
	api := r.Group("/admin/api", middleware.Auth(), middleware.OrgContext(db))
	api.GET("/mealtimes/:id", admin.GetMealTime)
	api.PUT("/mealtimes/:id", admin.UpdateMealTime)
	api.DELETE("/mealtimes/:id", admin.DeleteMealTime)
	api.GET("/tasks/:id", admin.GetTask)
	api.PUT("/tasks/:id", admin.UpdateTask)
	api.GET("/childcare/:id", admin.GetChildcareSchedule)
	api.POST("/recipes/:id/comments", admin.CreateRecipeComment)

	suffix := fmt.Sprint(time.Now().UnixNano())
	call := func(method, path, token string, orgID uint, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Org-Id", fmt.Sprint(orgID))
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Данные дома 1 (его ведёт планировщик) и работающий в нём помощник.
	mkUser(t, db, "org1-helper-"+suffix, scheduler.OrgID)
	day := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(time.Now().UnixNano()%3000))
	meal := models.MealTime{OrganizationID: scheduler.OrgID, Name: "Breakfast " + suffix, DefaultTime: "08:00", FamilyMember: "adult", Active: true}
	recipe := models.Recipe{OrganizationID: scheduler.OrgID, Name: "Каша " + suffix}
	must(t, db.Create(&meal).Error, db.Create(&recipe).Error)
	sched1 := models.DailySchedule{OrganizationID: scheduler.OrgID, Date: day.AddDate(0, 0, -1)}
	must(t, db.Create(&sched1).Error)
	task1 := models.ScheduleTask{OrganizationID: scheduler.OrgID, ScheduleID: sched1.ID, TaskType: "custom", Title: "Секрет"}
	cc := models.ChildcareSchedule{OrganizationID: scheduler.OrgID, Date: day, StartTime: "10:00", EndTime: "11:00"}
	must(t, db.Create(&task1).Error, db.Create(&cc).Error)

	// Другой дом.
	w := call("POST", "/auth/register", "", 0, map[string]string{
		"email": "iso-" + suffix + "@example.test", "password": "owner-pass-1", "org_name": "Other " + suffix,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	var reg struct {
		Access string      `json:"access"`
		User   models.User `json:"user"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &reg)
	var orgB uint
	db.Model(&models.Membership{}).Where("user_id = ?", reg.User.ID).Pluck("organization_id", &orgB)

	for _, tc := range []struct{ method, path string }{
		{"GET", fmt.Sprintf("/admin/api/mealtimes/%d", meal.ID)},
		{"PUT", fmt.Sprintf("/admin/api/mealtimes/%d", meal.ID)},
		{"GET", fmt.Sprintf("/admin/api/tasks/%d", task1.ID)},
		{"PUT", fmt.Sprintf("/admin/api/tasks/%d", task1.ID)},
		{"GET", fmt.Sprintf("/admin/api/childcare/%d", cc.ID)},
		{"POST", fmt.Sprintf("/admin/api/recipes/%d/comments", recipe.ID)},
	} {
		if w := call(tc.method, tc.path, reg.Access, orgB, map[string]any{"title": "x", "comment": "x", "name": "x"}); w.Code != http.StatusNotFound {
			t.Errorf("%s %s из чужого дома: %d %s, ждали 404", tc.method, tc.path, w.Code, w.Body)
		}
	}
	call("DELETE", fmt.Sprintf("/admin/api/mealtimes/%d", meal.ID), reg.Access, orgB, nil)
	if count(db, &models.MealTime{}, "id = ?", meal.ID) != 1 {
		t.Errorf("чужой дом удалил приём пищи")
	}
	var after models.ScheduleTask
	db.First(&after, task1.ID)
	if after.Title != "Секрет" {
		t.Errorf("чужой дом изменил задачу: %q", after.Title)
	}

	// День другого дома без дня дома 1: планировщик собирает свой день, а чужой не трогает.
	schedB := models.DailySchedule{OrganizationID: orgB, Date: day}
	must(t, db.Create(&schedB).Error)
	if err := scheduler.NewScheduler(db).GenerateScheduleForDate(day); err != nil {
		t.Fatal(err)
	}
	if n := count(db, &models.ScheduleTask{}, "schedule_id = ?", schedB.ID); n != 0 {
		t.Errorf("в день другого дома попало %d задач дома 1", n)
	}
	db.First(&schedB, schedB.ID)
	if schedB.Generated {
		t.Errorf("день другого дома помечен собранным")
	}
	if count(db, &models.DailySchedule{}, "organization_id = ? AND date = ?", scheduler.OrgID, day) != 1 {
		t.Errorf("планировщик не собрал день дома 1")
	}
}
