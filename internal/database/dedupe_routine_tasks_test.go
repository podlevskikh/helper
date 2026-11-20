package database

import (
	"os"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/models"
)

// MIG-06: копии задачи регулярной задачи в одном дне сводятся к одной — остаётся выполненная.
func TestDedupeRoutineTasks(t *testing.T) {
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
	day := time.Date(2030, 1, 7, 0, 0, 0, 0, time.UTC)
	s := models.DailySchedule{OrganizationID: org.ID, Date: day}
	if err := DB.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	rid := uint(900000 + org.ID)
	mk := func(tm string, done bool) models.ScheduleTask {
		task := models.ScheduleTask{OrganizationID: org.ID, ScheduleID: s.ID, TaskType: "custom", Title: "Полить цветы",
			Time: tm, RoutineID: &rid, Completed: done}
		if err := DB.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		return task
	}
	mk("09:00", false)
	done := mk("09:00", true)
	mk("09:00", false)
	other := mk("18:00", false)                                         // другое время — не копия
	DB.Create(&models.TaskDish{TaskID: done.ID + 1000000, Position: 1}) // чужая строка не мешает

	if err := DB.Transaction(dedupeRoutineTasks); err != nil {
		t.Fatal(err)
	}
	var left []models.ScheduleTask
	DB.Where("schedule_id = ?", s.ID).Order("time").Find(&left)
	if len(left) != 2 || left[0].ID != done.ID || left[1].ID != other.ID {
		t.Fatalf("осталось %d задач (%v), ждали выполненную %d и %d", len(left), left, done.ID, other.ID)
	}
	if err := DB.Transaction(dedupeRoutineTasks); err != nil { // повторный запуск ничего не ломает
		t.Fatal(err)
	}
}
