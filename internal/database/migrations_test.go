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

// MIG-05: в основном дне только задачи ребёнка, в дубле — еда (выполнена, с выбранным блюдом), уборка и
// выполненная копия повторяющейся задачи. Ничего не теряется: еда и уборка переезжают, отметки и блюдо целы.
func TestDedupeKeepsOtherTypesAndCompletion(t *testing.T) {
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
	org := models.Organization{Name: "dedupe types"}
	DB.Create(&org)
	DB.Exec("DROP INDEX IF EXISTS idx_daily_schedules_org_date")
	day := time.Date(2026, 2, 10, 0, 0, 0, 0, time.UTC)
	keep := models.DailySchedule{OrganizationID: org.ID, Date: day}
	drop := models.DailySchedule{OrganizationID: org.ID, Date: day.Add(-2 * time.Hour), Generated: true}
	DB.Create(&keep)
	DB.Create(&drop)
	rec := models.RecurringTask{OrganizationID: org.ID, Title: "мусор"}
	if err := DB.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	recipe := models.Recipe{OrganizationID: org.ID, Name: "суп"}
	DB.Create(&recipe)
	done := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	mk := func(task models.ScheduleTask) models.ScheduleTask {
		task.OrganizationID = org.ID
		if err := DB.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		return task
	}
	mk(models.ScheduleTask{ScheduleID: keep.ID, TaskType: "childcare", Title: "прогулка"})
	keepRec := mk(models.ScheduleTask{ScheduleID: keep.ID, TaskType: "custom", Title: "мусор", RecurringTaskID: &rec.ID})
	lunch := mk(models.ScheduleTask{ScheduleID: drop.ID, TaskType: "meal", Title: "обед", Completed: true, CompletedAt: &done})
	mk(models.ScheduleTask{ScheduleID: drop.ID, TaskType: "cleaning", Title: "кухня"})
	mk(models.ScheduleTask{ScheduleID: drop.ID, TaskType: "custom", Title: "мусор", RecurringTaskID: &rec.ID, Completed: true, CompletedAt: &done})
	DB.Exec("INSERT INTO meal_recipes (schedule_task_id, recipe_id) VALUES (?, ?)", lunch.ID, recipe.ID)

	if err := DB.Transaction(dedupeDailySchedules); err != nil {
		t.Fatal(err)
	}
	var days []models.DailySchedule
	DB.Where("organization_id = ?", org.ID).Find(&days)
	if len(days) != 1 || days[0].ID != keep.ID || !days[0].Generated {
		t.Fatalf("дни после слияния: %+v", days)
	}
	var tasks []models.ScheduleTask
	DB.Where("schedule_id = ?", keep.ID).Find(&tasks)
	got := map[string]models.ScheduleTask{}
	for _, x := range tasks {
		got[x.TaskType+"/"+x.Title] = x
	}
	if len(tasks) != 4 {
		t.Fatalf("задачи после слияния (%d): %+v", len(tasks), got)
	}
	if l, ok := got["meal/обед"]; !ok || !l.Completed {
		t.Errorf("обед потерян или без отметки: %+v", got["meal/обед"])
	}
	if _, ok := got["cleaning/кухня"]; !ok {
		t.Errorf("уборка потеряна")
	}
	if r := got["custom/мусор"]; r.ID != keepRec.ID || !r.Completed {
		t.Errorf("повторяющаяся: отметка «сделано» из дубля потеряна: %+v", r)
	}
	var recipes int64
	DB.Raw("SELECT count(*) FROM meal_recipes WHERE schedule_task_id = ? AND recipe_id = ?", got["meal/обед"].ID, recipe.ID).Scan(&recipes)
	if recipes != 1 {
		t.Errorf("выбранное блюдо обеда потеряно")
	}
}
