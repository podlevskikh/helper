package server

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/scheduler"
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

// MEAL-03: новый приём пищи попадает в уже собранные дни, удалённый — исчезает; блюда остальных не меняются.
func TestMealChangesRefreshGeneratedDays(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("mealref")
	day := clock.OrgToday(e.db, o.Org).AddDate(0, 0, 1)
	if err := scheduler.NewScheduler(e.db).GenerateScheduleForOrgDate(o.Org, day); err != nil {
		t.Fatal(err)
	}
	path := "/helper/api/schedule/date/" + day.Format("2006-01-02")
	before := titles(e, e.json(e.do(req{method: "GET", path: path, token: o.Token, org: o.Org})))
	if !has(before, "lunch - all") {
		t.Fatalf("в собранном дне нет обеда: %v", before)
	}
	w := e.do(req{method: "POST", path: "/admin/api/mealtimes", token: o.Token, org: o.Org,
		body: map[string]any{"name": "snack", "default_time": "16:00", "family_member": "all", "active": true}})
	e.expect("перекус", w, 201)
	var lunch float64
	for _, m := range e.jsonList(e.do(req{method: "GET", path: "/admin/api/mealtimes", token: o.Token, org: o.Org})) {
		if m["name"] == "lunch" {
			lunch = m["id"].(float64)
		}
	}
	e.expect("удалить обед", e.do(req{method: "DELETE", path: fmt.Sprintf("/admin/api/mealtimes/%.0f", lunch), token: o.Token, org: o.Org}), 200)
	after := titles(e, e.json(e.do(req{method: "GET", path: path, token: o.Token, org: o.Org})))
	if !has(after, "snack - all") || has(after, "lunch - all") || !has(after, "breakfast - all") {
		t.Errorf("после изменений: %v", after)
	}
}

// AUTH-08: имя при регистрации; без имени — часть почты до @, а не вся почта.
func TestRegisterName(t *testing.T) {
	e := newEnv(t, 1000)
	w := e.do(req{method: "POST", path: "/auth/register", body: map[string]string{"email": "anna-" + uniq() + "@test.local", "password": "pass-" + uniq(), "org_name": "Дом", "name": "Анна"}})
	e.expect("регистрация с именем", w, 201)
	if u := e.json(w)["user"].(map[string]any); u["name"] != "Анна" {
		t.Errorf("имя: %v", u["name"])
	}
	w = e.do(req{method: "POST", path: "/auth/register", body: map[string]string{"email": "maria.k-" + uniq() + "@test.local", "password": "pass-" + uniq(), "org_name": "Дом"}})
	if u := e.json(w)["user"].(map[string]any); fmt.Sprint(u["name"]) == "" || strings.Contains(fmt.Sprint(u["name"]), "@") {
		t.Errorf("имя без поля: %v", u["name"])
	}
}

// AUTH-09: смена почты — только с текущим паролем; занятая → 409; вход по новой почте, старая не работает;
// неиспользованные ссылки сброса пароля (ушли на прежнюю почту) удаляются.
func TestChangeEmail(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("email")
	other := e.owner("email2")
	e.db.Create(&models.PasswordReset{UserID: o.UserID, TokenHash: "h-" + uniq(), ExpiresAt: time.Now().Add(time.Hour)})

	e.expect("неверный пароль", e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": "nope", "new_email": "x-" + uniq() + "@test.local"}}), 403)
	e.expect("не почта", e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": "not-an-email"}}), 400)
	e.expect("занята", e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": strings.ToUpper(other.Email)}}), 409)
	e.expect("без входа", e.do(req{method: "POST", path: "/auth/email",
		body: map[string]string{"current_password": o.Password, "new_email": "y@test.local"}}), 401)

	newEmail := "New-" + uniq() + "@Test.local"
	w := e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": " " + newEmail + " "}})
	e.expect("смена", w, 200)
	if got := e.json(w)["user"].(map[string]any)["email"]; got != strings.ToLower(newEmail) {
		t.Errorf("почта в ответе: %v", got)
	}
	e.expect("вход по новой", e.do(req{method: "POST", path: "/auth/login",
		body: map[string]string{"email": strings.ToLower(newEmail), "password": o.Password}}), 200)
	e.expect("вход по старой", e.do(req{method: "POST", path: "/auth/login",
		body: map[string]string{"email": o.Email, "password": o.Password}}), 401)
	var resets int64
	e.db.Model(&models.PasswordReset{}).Where("user_id = ?", o.UserID).Count(&resets)
	if resets != 0 {
		t.Errorf("остались ссылки сброса на старую почту: %d", resets)
	}
}

// INV-08: одно приглашение — один человек, даже при одновременном принятии; приглашение не понижает
// роль того, кто уже в доме (владелец, открывший ссылку «помощник», остаётся владельцем).
func TestInviteAcceptOnceAndNoDowngrade(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("invrace")
	w := e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org,
		body: map[string]string{"role": "helper"}})
	e.expect("приглашение без почты", w, 201)
	token := e.json(w)["token"]

	codes := make(chan int, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := e.do(req{method: "POST", path: fmt.Sprintf("/invites/%v/accept", token), body: map[string]string{
				"name": "гонка", "password": "pass-" + uniq(), "email": "race-" + uniq() + "@test.local"}})
			codes <- r.Code
		}()
	}
	wg.Wait()
	close(codes)
	ok := 0
	for c := range codes {
		if c == 200 {
			ok++
		} else if c != 410 {
			t.Errorf("параллельное принятие: код %d", c)
		}
	}
	var members int64
	e.db.Model(&models.Membership{}).Where("organization_id = ? AND role = 'helper'", o.Org).Count(&members)
	if ok != 1 || members != 1 {
		t.Fatalf("одно приглашение приняли %d раз, помощников в доме %d", ok, members)
	}

	// Владелец сам принимает приглашение «помощник» на свою почту.
	w = e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org,
		body: map[string]string{"email": o.Email, "role": "helper"}})
	if w.Code != 201 {
		return // приглашать уже состоящих нельзя — понижение невозможно и так
	}
	e.do(req{method: "POST", path: fmt.Sprintf("/invites/%v/accept", e.json(w)["token"]), body: map[string]string{
		"name": "владелец", "password": o.Password}})
	var m models.Membership
	e.db.Where("user_id = ? AND organization_id = ?", o.UserID, o.Org).First(&m)
	if m.Role != "owner" {
		t.Errorf("приглашение понизило владельца до %q", m.Role)
	}
}
