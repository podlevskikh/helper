package database

import (
	"os"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"
)

// RT-12: перенос в «Ритм дома»: приём пищи → «Еда» с типом блюда и 2 порциями, рецепт получает тип,
// зона → «Уборка» в свои дни, повторяющаяся → один в один, распорядок ребёнка → по дням недели.
// Уже собранный день не дублируется: его задачи привязываются к регулярным.
func TestMigrateRoutinesFromLegacy(t *testing.T) {
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
	old := clock.Now
	clock.Now = func() time.Time { return time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC) } // понедельник
	defer func() { clock.Now = old }()
	today := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)

	owner := models.User{Email: "mig-owner-" + time.Now().Format("150405.000000") + "@test.local", Name: "Анна"}
	DB.Create(&owner)
	helper := models.User{Email: "mig-helper-" + time.Now().Format("150405.000000") + "@test.local", Name: "Мария"}
	DB.Create(&helper)
	org := models.Organization{Name: "migrate test", OwnerUserID: owner.ID}
	DB.Create(&org)
	DB.Create(&models.Membership{OrganizationID: org.ID, UserID: owner.ID, Role: models.RoleOwner, Status: models.MembershipActive})
	DB.Create(&models.Membership{OrganizationID: org.ID, UserID: helper.ID, Role: models.RoleHelper, Status: models.MembershipActive})

	lunch := models.MealTime{OrganizationID: org.ID, Name: "lunch", DefaultTime: "13:00", FamilyMember: "all", Active: true}
	DB.Create(&lunch)
	recipe := models.Recipe{OrganizationID: org.ID, Name: "Борщ", IsActive: true}
	DB.Create(&recipe)
	DB.Exec("INSERT INTO recipe_meal_times (recipe_id, meal_time_id) VALUES (?, ?)", recipe.ID, lunch.ID)
	zone := models.CleaningZone{OrganizationID: org.ID, Name: "Кухня", FrequencyPerWeek: 3, Priority: "high"}
	DB.Create(&zone)
	hid := helper.ID
	rec := models.RecurringTask{OrganizationID: org.ID, Title: "Полить цветы", Weekdays: "3,6", Time: "10:00",
		StartDate: today, Active: true, AssignedToUserID: &hid}
	DB.Create(&rec)
	for _, d := range []int{-7, -5, 0} { // пн, ср, пн
		DB.Create(&models.ChildcareSchedule{OrganizationID: org.ID, Date: today.AddDate(0, 0, d), StartTime: "09:30", EndTime: "11:00", Notes: "Прогулка"})
	}
	// Уже собранный сегодняшний день со старым обедом.
	sched, _ := models.EnsureDailySchedule(DB, org.ID, today, true)
	rid := recipe.ID
	oldLunch := models.ScheduleTask{OrganizationID: org.ID, ScheduleID: sched.ID, TaskType: "meal", Title: "lunch - all",
		Time: "13:00", RecipeID: &rid}
	DB.Create(&oldLunch)

	if err := DB.Transaction(migrateRoutinesFromLegacy); err != nil {
		t.Fatal(err)
	}

	var types []models.DishType
	DB.Where("organization_id = ?", org.ID).Find(&types)
	if len(types) != 1 || types[0].Name != "Обед" {
		t.Fatalf("типы блюд: %+v", types)
	}
	DB.First(&recipe, recipe.ID)
	if recipe.DishTypeID == nil || *recipe.DishTypeID != types[0].ID {
		t.Errorf("тип у рецепта: %v", recipe.DishTypeID)
	}
	var list []models.Routine
	DB.Preload("Dishes").Where("organization_id = ?", org.ID).Order("id").Find(&list)
	if len(list) != 4 {
		t.Fatalf("регулярных задач: %d (%+v)", len(list), list)
	}
	byTitle := map[string]models.Routine{}
	for _, r := range list {
		byTitle[r.Title] = r
		if r.AssignedToUserID != helper.ID || r.CreatedBy != owner.ID {
			t.Errorf("%s: исполнитель %d, поставил %d", r.Title, r.AssignedToUserID, r.CreatedBy)
		}
	}
	if r := byTitle["Обед"]; r.Kind != models.RoutineFood || len(r.Dishes) != 1 || r.Dishes[0].Servings != 2 || r.Times != `["13:00"]` {
		t.Errorf("обед: %+v", r)
	}
	if r := byTitle["Кухня"]; r.Kind != models.RoutineClean || r.Priority != "high" || len(r.Weekdays) == 0 {
		t.Errorf("кухня: %+v", r)
	}
	if r := byTitle["Полить цветы"]; r.Weekdays != "3,6" || r.TimeMode != models.TimeExact {
		t.Errorf("цветы: %+v", r)
	}
	if r := byTitle["Прогулка"]; r.Kind != models.RoutineKid || r.Weekdays != "1,3" || r.Times != `["09:30","11:00"]` {
		t.Errorf("прогулка: %+v", r)
	}
	DB.First(&oldLunch, oldLunch.ID)
	if oldLunch.RoutineID == nil || *oldLunch.RoutineID != byTitle["Обед"].ID || oldLunch.Title != "Обед" {
		t.Errorf("старый обед не привязан: %+v", oldLunch)
	}
	if oldLunch.AssignedToUserID == nil {
		t.Errorf("у перенесённого обеда нет исполнителя: %+v", oldLunch)
	}
	var migrated int64
	DB.Model(&models.MealTime{}).Where("id = ? AND migrated_routine_id IS NOT NULL", lunch.ID).Count(&migrated)
	if migrated != 1 {
		t.Errorf("приём пищи не помечен перенесённым")
	}
}
