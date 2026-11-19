package server

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/auth"
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

// AUTH-EMAIL-01…05: смена почты с подтверждением.
// Ссылки из писем тест не читает: подставляет в запрос свой токен (в базе хранится только хеш).
func setEmailTokens(e *env, userID uint) (confirm, cancel string) {
	confirm, cancel = "confirm-"+uniq(), "cancel-"+uniq()
	var ec models.EmailChange
	e.db.Where("user_id = ? AND confirmed_at IS NULL AND canceled_at IS NULL", userID).Order("id DESC").First(&ec)
	e.db.Model(&ec).Updates(map[string]any{"token_hash": auth.HashToken(confirm), "cancel_token_hash": auth.HashToken(cancel)})
	return
}

func TestEmailChangeFlow(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("email")
	second := e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": o.Password}})
	e.expect("второе устройство", second, 200)
	secondRefresh, _ := e.json(second)["refresh"].(string)
	e.db.Create(&models.PasswordReset{UserID: o.UserID, TokenHash: "h-" + uniq(), ExpiresAt: time.Now().Add(time.Hour)})
	post := func(name, path string, body any, token string, code int) map[string]any {
		w := e.do(req{method: "POST", path: path, token: token, body: body})
		e.expect(name, w, code)
		return e.json(w)
	}

	post("неверный пароль", "/auth/email", map[string]string{"current_password": "nope", "new_email": "x-" + uniq() + "@test.local"}, o.Token, 403)
	post("не почта", "/auth/email", map[string]string{"current_password": o.Password, "new_email": "not-an-email"}, o.Token, 400)
	post("та же почта", "/auth/email", map[string]string{"current_password": o.Password, "new_email": strings.ToUpper(o.Email)}, o.Token, 400)
	e.expect("без входа", e.do(req{method: "POST", path: "/auth/email", body: map[string]string{"current_password": o.Password, "new_email": "y@test.local"}}), 401)

	first := "First-" + uniq() + "@Test.local"
	r := post("запрос", "/auth/email", map[string]string{"current_password": o.Password, "new_email": " " + first + " "}, o.Token, 202)
	if p, _ := r["pending"].(map[string]any); p == nil || p["email"] != strings.ToLower(first) {
		t.Fatalf("ожидает подтверждения: %v", r)
	}
	oldConfirm, oldCancel := setEmailTokens(e, o.UserID)

	// Новый запрос гасит прежний.
	newEmail := "new-" + uniq() + "@test.local"
	post("второй запрос", "/auth/email", map[string]string{"current_password": o.Password, "new_email": newEmail}, o.Token, 202)
	post("ссылка первого запроса не работает", "/auth/email/confirm", map[string]string{"token": oldConfirm}, "", 410)
	post("отмена первого тоже", "/auth/email/cancel", map[string]string{"token": oldCancel}, "", 410)
	if g := e.json(e.do(req{method: "GET", path: "/auth/email", token: o.Token})); g["pending"].(map[string]any)["email"] != newEmail {
		t.Errorf("ожидает подтверждения: %v", g)
	}
	// До подтверждения почта прежняя.
	e.expect("вход со старой до подтверждения", e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": o.Password}}), 200)
	e.expect("отправить ещё раз сразу", e.do(req{method: "POST", path: "/auth/email/resend", token: o.Token}), 429)

	confirm, _ := setEmailTokens(e, o.UserID)
	post("проверка ссылки", "/auth/email/check", map[string]string{"token": confirm}, "", 200)
	post("проверка мусора", "/auth/email/check", map[string]string{"token": "garbage"}, "", 410)
	if got := post("подтверждение без входа", "/auth/email/confirm", map[string]string{"token": confirm}, "", 200)["email"]; got != newEmail {
		t.Errorf("почта в ответе: %v", got)
	}
	post("повторное подтверждение", "/auth/email/confirm", map[string]string{"token": confirm}, "", 410)

	e.expect("вход по новой", e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": newEmail, "password": o.Password}}), 200)
	e.expect("вход по старой", e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": o.Password}}), 401)
	e.expect("старый токен после смены", e.do(req{method: "GET", path: "/auth/me", token: o.Token}), 401)
	e.expect("refresh другого устройства", e.do(req{method: "POST", path: "/auth/refresh", body: map[string]string{"refresh": secondRefresh}}), 401)
	var resets int64
	e.db.Model(&models.PasswordReset{}).Where("user_id = ?", o.UserID).Count(&resets)
	if resets != 0 {
		t.Errorf("остались ссылки сброса на старую почту: %d", resets)
	}
}

// AUTH-EMAIL-03: «это не я» со старого адреса — смена отменена, все входы завершены; отмена из профиля.
func TestEmailChangeCancel(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("emailcancel")
	target := "steal-" + uniq() + "@test.local"
	e.expect("запрос", e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": target}}), 202)
	confirm, cancel := setEmailTokens(e, o.UserID)
	e.expect("проверка ссылки отмены", e.do(req{method: "POST", path: "/auth/email/check", body: map[string]string{"token": cancel, "kind": "cancel"}}), 200)
	e.expect("это не я", e.do(req{method: "POST", path: "/auth/email/cancel", body: map[string]string{"token": cancel}}), 200)
	e.expect("подтвердить после отмены", e.do(req{method: "POST", path: "/auth/email/confirm", body: map[string]string{"token": confirm}}), 410)
	e.expect("сессии завершены", e.do(req{method: "GET", path: "/auth/me", token: o.Token}), 401)
	e.expect("вход по старой почте", e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": o.Password}}), 200)

	// Отмена из профиля.
	w := e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": o.Password}})
	tok, _ := e.json(w)["access"].(string)
	e.expect("запрос 2", e.do(req{method: "POST", path: "/auth/email", token: tok,
		body: map[string]string{"current_password": o.Password, "new_email": target}}), 202)
	confirm2, _ := setEmailTokens(e, o.UserID)
	e.expect("отменить в профиле", e.do(req{method: "DELETE", path: "/auth/email", token: tok}), 204)
	if g := e.json(e.do(req{method: "GET", path: "/auth/email", token: tok})); g["pending"] != nil {
		t.Errorf("после отмены всё ещё ждёт: %v", g)
	}
	e.expect("подтвердить отменённое", e.do(req{method: "POST", path: "/auth/email/confirm", body: map[string]string{"token": confirm2}}), 410)
}

// AUTH-EMAIL-04: занятый адрес — ответ на запрос такой же, как на свободный (не раскрывает);
// подтвердить нельзя; адрес, занятый уже после запроса, — понятная ошибка при подтверждении.
func TestEmailChangeTaken(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("emailtaken")
	other := e.owner("emailtaken2")
	free := e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": "free-" + uniq() + "@test.local"}})
	busy := e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": other.Email}})
	if free.Code != busy.Code || free.Code != 202 {
		t.Fatalf("занятый и свободный адрес отвечают по-разному: %d / %d", free.Code, busy.Code)
	}
	confirm, _ := setEmailTokens(e, o.UserID)
	e.expect("подтверждение занятого", e.do(req{method: "POST", path: "/auth/email/confirm", body: map[string]string{"token": confirm}}), 409)

	// Адрес заняли после запроса.
	later := "later-" + uniq() + "@test.local"
	e.expect("запрос на свободный", e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": later}}), 202)
	confirm2, _ := setEmailTokens(e, o.UserID)
	e.expect("кто-то зарегистрировался", e.do(req{method: "POST", path: "/auth/register",
		body: map[string]string{"email": later, "password": "pass-" + uniq(), "org_name": "x", "locale": "ru"}}), 201)
	w := e.do(req{method: "POST", path: "/auth/email/confirm", body: map[string]string{"token": confirm2}})
	e.expect("подтверждение занятого позже", w, 409)
	if e.json(w)["code"] != "email_taken" {
		t.Errorf("код ошибки: %v", e.json(w))
	}
	e.expect("почта не изменилась", e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": o.Password}}), 200)
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
