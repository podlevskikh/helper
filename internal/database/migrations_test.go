package database

import (
	"os"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/models"
)

// MIG-04: дубли дней (в том числе «полночь не UTC») сливаются в один день, задачи не теряются, индекс уникальности есть.
func TestDedupeDailySchedules(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB") == "1" {
			t.Fatal("TEST_DATABASE_URL не задан, а REQUIRE_DB=1")
		}
		t.Skip("TEST_DATABASE_URL не задан")
	}
	if err := Initialize(url); err != nil {
		t.Fatal(err)
	}
	org := models.Organization{Name: "dedupe test"}
	DB.Create(&org)
	DB.Exec("DROP INDEX IF EXISTS idx_daily_schedules_org_date")
	day := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	a := models.DailySchedule{OrganizationID: org.ID, Date: day, Generated: true}
	b := models.DailySchedule{OrganizationID: org.ID, Date: day.Add(-2 * time.Hour)} // старая «полночь по Никосии»
	DB.Create(&a)
	DB.Create(&b)
	for _, task := range []models.ScheduleTask{
		{ScheduleID: a.ID, OrganizationID: org.ID, TaskType: "meal", Title: "обед A"},
		{ScheduleID: b.ID, OrganizationID: org.ID, TaskType: "meal", Title: "обед B (дубль)"},
		{ScheduleID: b.ID, OrganizationID: org.ID, TaskType: "custom", Title: "разовая из B"},
	} {
		task := task
		if err := DB.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := DB.Transaction(dedupeDailySchedules); err != nil {
		t.Fatal(err)
	}
	var days []models.DailySchedule
	DB.Where("organization_id = ?", org.ID).Find(&days)
	if len(days) != 1 || !days[0].Date.Equal(day) {
		t.Fatalf("дни после слияния: %+v", days)
	}
	var titles []string
	DB.Model(&models.ScheduleTask{}).Where("schedule_id = ?", days[0].ID).Pluck("title", &titles)
	set := map[string]bool{}
	for _, x := range titles {
		set[x] = true
	}
	if len(titles) != 2 || !set["обед A"] || !set["разовая из B"] {
		t.Errorf("задачи после слияния: %v", titles)
	}
	dup := models.DailySchedule{OrganizationID: org.ID, Date: day}
	if DB.Create(&dup).Error == nil {
		t.Error("индекс уникальности (organization_id, date) не создан")
	}
}
