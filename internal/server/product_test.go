package server

import (
	"fmt"
	"testing"

	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"
)

// MEAL-01: у нового дома сразу есть завтрак, обед и ужин — рецепты попадают в меню без ручной настройки.
func TestNewHomeHasDefaultMealTimes(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("meal")
	list := e.jsonList(e.do(req{method: "GET", path: "/admin/api/mealtimes", token: o.Token, org: o.Org}))
	names := map[string]bool{}
	for _, m := range list {
		names[fmt.Sprint(m["name"])] = true
	}
	if len(list) != 3 || !names["breakfast"] || !names["lunch"] || !names["dinner"] {
		t.Errorf("приёмы пищи нового дома: %v", list)
	}
}

// TASK-05: разовую задачу можно перенести на другой день, повторяющуюся — нельзя.
func TestMoveTaskToAnotherDay(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("move")
	today := clock.OrgToday(e.db, o.Org)
	w := e.do(req{method: "POST", path: "/admin/api/custom-tasks", token: o.Token, org: o.Org,
		body: map[string]any{"date": today.Format("2006-01-02"), "title": "перенести"}})
	e.expect("задача", w, 201)
	id := e.json(w)["id"]
	to := today.AddDate(0, 0, 2).Format("2006-01-02")
	e.expect("перенос", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/tasks/%.0f", id), token: o.Token, org: o.Org,
		body: map[string]any{"title": "перенести", "date": to}}), 200)
	if !has(titles(e, e.json(e.do(req{method: "GET", path: "/helper/api/schedule/date/" + to, token: o.Token, org: o.Org}))), "перенести") {
		t.Error("задача не появилась в новом дне")
	}
	if has(titles(e, e.json(e.do(req{method: "GET", path: "/helper/api/schedule/today", token: o.Token, org: o.Org}))), "перенести") {
		t.Error("задача осталась в старом дне")
	}
}

// INV-06: список ожидающих приглашений и отзыв ссылки.
func TestListAndRevokeInvites(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("invl")
	w := e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org,
		body: map[string]string{"email": "wait-" + uniq() + "@test.local", "role": "helper"}})
	e.expect("приглашение", w, 201)
	token := e.json(w)["token"]
	list := e.jsonList(e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org}))
	if len(list) != 1 || list[0]["invite_url"] == nil {
		t.Fatalf("список приглашений: %v", list)
	}
	e.expect("отзыв", e.do(req{method: "DELETE", path: fmt.Sprintf("/orgs/%d/invites/%.0f", o.Org, list[0]["id"]), token: o.Token, org: o.Org}), 204)
	e.expect("отозванное не принять", e.do(req{method: "POST", path: fmt.Sprintf("/invites/%v/accept", token),
		body: map[string]string{"name": "x", "password": "pass-" + uniq(), "email": "wait@test.local"}}), 410)
	if l := e.jsonList(e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org})); len(l) != 0 {
		t.Errorf("отозванное в списке: %v", l)
	}
}

// PWD-05: ссылку сброса пароля можно проверить до ввода нового пароля.
func TestResetTokenCheck(t *testing.T) {
	e := newEnv(t, 1000)
	e.expect("неизвестная ссылка", e.do(req{method: "POST", path: "/auth/password/reset/check", body: map[string]string{"token": "deadbeef"}}), 410)
}

// ZONE-02 / ZONE-03: уборка только в рабочие дни дома; после смены выходных ближайшие дни пересобираются.
func TestCleaningOnlyOnWorkdays(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("zone")
	h := e.member(o, "helper", "zone")
	e.expect("зона", e.do(req{method: "POST", path: "/admin/api/zones", token: o.Token, org: o.Org,
		body: map[string]any{"name": "кухня", "frequency_per_week": 7}}), 201)
	// Выходные помощницы — суббота и воскресенье.
	e.expect("выходные", e.do(req{method: "PATCH", path: fmt.Sprintf("/orgs/%d/members/%d/schedule-settings", o.Org, h.UserID), token: o.Token, org: o.Org,
		body: map[string]any{"days_off": []int{6, 7}}}), 200)
	zones := e.jsonList(e.do(req{method: "GET", path: "/admin/api/zones", token: o.Token, org: o.Org}))
	if len(zones) != 1 || fmt.Sprint(zones[0]["days"]) != "[1 2 3 4 5]" {
		t.Errorf("дни зоны: %v", zones)
	}
	today := clock.OrgToday(e.db, o.Org)
	for i := 0; i < 7; i++ {
		day := today.AddDate(0, 0, i)
		var n int64
		e.db.Model(&models.ScheduleTask{}).Joins("JOIN daily_schedules ds ON ds.id = schedule_tasks.schedule_id").
			Where("ds.organization_id = ? AND ds.date = ? AND schedule_tasks.task_type = 'cleaning'", o.Org, day).Count(&n)
		iso := int(day.Weekday())
		weekend := iso == 0 || iso == 6
		if weekend && n != 0 {
			t.Errorf("%s (выходной): уборок %d", day.Format("2006-01-02 Mon"), n)
		}
		if !weekend && n != 1 {
			t.Errorf("%s (рабочий): уборок %d, ждали 1", day.Format("2006-01-02 Mon"), n)
		}
	}
}
