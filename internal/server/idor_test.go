package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/models"
)

// SEC-04: каждый маршрут с :id, вызванный из дома B с id объектов дома A, отвечает ровно 404,
// и данные дома A не меняются. SEC-07: то же для списков покупок и их пунктов.
func TestIDORAllRoutesWithID(t *testing.T) {
	e := newEnv(t, 1000)
	a := e.owner("idorA")
	helperA := e.member(a, "helper", "idorA")
	b := e.owner("idorB")

	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 3)
	recipe := models.Recipe{OrganizationID: a.Org, Name: "секретный рецепт"}
	e.db.Create(&recipe)
	comment := models.RecipeComment{OrganizationID: a.Org, RecipeID: recipe.ID, Comment: "коммент"}
	e.db.Create(&comment)
	meal := models.MealTime{OrganizationID: a.Org, Name: "lunch", DefaultTime: "13:00"}
	e.db.Create(&meal)
	zone := models.CleaningZone{OrganizationID: a.Org, Name: "детская", FrequencyPerWeek: 2}
	e.db.Create(&zone)
	child := models.ChildcareSchedule{OrganizationID: a.Org, Date: day, StartTime: "10:00", EndTime: "11:00"}
	e.db.Create(&child)
	sched, _ := models.EnsureDailySchedule(e.db, a.Org, day, false)
	task := models.ScheduleTask{ScheduleID: sched.ID, OrganizationID: a.Org, TaskType: "custom", Title: "код сигнализации"}
	e.db.Create(&task)
	rec := models.RecurringTask{OrganizationID: a.Org, Title: "повтор", Weekdays: "1", StartDate: day, Active: true}
	e.db.Create(&rec)
	var cat models.TaskCategory
	e.db.Where("organization_id = ?", a.Org).First(&cat)
	list := models.ShoppingList{OrganizationID: a.Org, ListDate: day.Format("2006-01-02"), Status: "draft", CreatedByUserID: helperA.UserID}
	e.db.Create(&list)
	item := models.ShoppingListItem{OrganizationID: a.Org, ListID: &list.ID, Item: "молоко"}
	e.db.Create(&item)
	ov := models.StaffDayOverride{OrganizationID: a.Org, UserID: helperA.UserID, Kind: "day_off", StartDate: day, EndDate: day}
	e.db.Create(&ov)

	idFor := func(path string) string {
		switch {
		case strings.Contains(path, "/comments/:id"):
			return fmt.Sprint(comment.ID)
		case strings.Contains(path, "/recipes/:id"):
			return fmt.Sprint(recipe.ID)
		case strings.Contains(path, "/mealtimes/:id"):
			return fmt.Sprint(meal.ID)
		case strings.Contains(path, "/zones/:id"):
			return fmt.Sprint(zone.ID)
		case strings.Contains(path, "/childcare/:id"):
			return fmt.Sprint(child.ID)
		case strings.Contains(path, "/recurring-tasks/:id"):
			return fmt.Sprint(rec.ID)
		case strings.Contains(path, "/task-categories/:id"):
			return fmt.Sprint(cat.ID)
		case strings.Contains(path, "/shopping-lists/:id"):
			return fmt.Sprint(list.ID)
		case strings.Contains(path, "/day-overrides/:id"):
			return fmt.Sprint(ov.ID)
		default: // tasks/:id, custom-tasks/:id
			return fmt.Sprint(task.ID)
		}
	}
	snapshot := func() string {
		var parts []string
		for _, tbl := range []string{"recipes", "recipe_comments", "meal_times", "cleaning_zones", "childcare_schedules",
			"schedule_tasks", "recurring_tasks", "task_categories", "shopping_lists", "shopping_list_items", "staff_day_overrides"} {
			var h string
			e.db.Raw(fmt.Sprintf("SELECT coalesce(md5(string_agg(t::text, ',' ORDER BY t.id)), '') FROM %s t WHERE t.organization_id = ?", tbl), a.Org).Scan(&h)
			parts = append(parts, tbl+"="+h)
		}
		return strings.Join(parts, ";")
	}
	before := snapshot()

	tested := 0
	for _, rt := range e.r.Routes() {
		p := rt.Path
		if !strings.Contains(p, ":id") && !strings.Contains(p, ":userId") {
			continue
		}
		path := strings.NewReplacer(":orgId", fmt.Sprint(b.Org), ":userId", fmt.Sprint(helperA.UserID),
			":recipe_id", fmt.Sprint(recipe.ID), ":zone_id", fmt.Sprint(zone.ID), ":itemId", fmt.Sprint(item.ID),
			":id", idFor(p)).Replace(p)
		body := map[string]any{"title": "взлом", "name": "взлом", "item": "взлом", "recipe_id": recipe.ID, "zone_id": zone.ID,
			"kind": "day_off", "start_date": "2026-12-01", "end_date": "2026-12-01", "days_off": []int{1}}
		w := e.do(req{method: rt.Method, path: path, token: b.Token, org: b.Org, body: body})
		tested++
		if w.Code != 404 {
			t.Errorf("%s %s из чужого дома: %d, ждали 404 (%.120s)", rt.Method, rt.Path, w.Code, w.Body.String())
		}
	}
	if tested < 30 {
		t.Fatalf("проверено маршрутов с id: %d — мало", tested)
	}
	if after := snapshot(); after != before {
		t.Errorf("данные дома A изменились из дома B:\nдо:    %s\nпосле: %s", before, after)
	}

	// ROLE-06: помощник не смотрит календарь другого сотрудника.
	h2 := e.member(a, "helper", "idorA2")
	e.expect("помощник: чужой календарь", e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/calendar?user_id=%d", a.Org, h2.UserID), token: helperA.Token, org: a.Org}), 403)
}
