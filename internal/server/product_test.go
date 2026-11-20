package server

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/auth"
	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/scheduler"
)

// RT-00: новый дом начинает с пустого «Ритма дома»: ни приёмов пищи, ни регулярных задач, ни типов блюд —
// их заводит тот, кто ведёт дом.
func TestNewHomeStartsEmpty(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("meal")
	for _, p := range []string{"/admin/api/mealtimes", "/admin/api/routines", "/admin/api/dish-types"} {
		if list := e.jsonList(e.do(req{method: "GET", path: p, token: o.Token, org: o.Org})); len(list) != 0 {
			t.Errorf("%s у нового дома: %v", p, list)
		}
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
	// Старые приёмы пищи (у новых домов их нет — только у тех, что жили до «Ритма дома»).
	if err := database.EnsureDefaultMealTimes(e.db, o.Org); err != nil {
		t.Fatal(err)
	}
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
	// Пароль сброшен: тот, кто его знал, войти больше не может; владелец задаёт новый через «Забыли пароль?».
	e.expect("старый пароль после «это не я»", e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": o.Password}}), 401)
	setPassword(e, o.UserID, o.Password)

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

func setPassword(e *env, userID uint, pw string) {
	hash, err := auth.HashPassword(pw)
	if err != nil {
		e.t.Fatal(err)
	}
	e.db.Model(&models.User{}).Where("id = ?", userID).Update("password_hash", hash)
}

// AUTH-EMAIL-07: незавершённая смена почты не переживает смену и сброс пароля (иначе тот, кто знал
// старый пароль, подтвердил бы её из своего ящика и забрал аккаунт).
func TestEmailChangeCanceledByPasswordChange(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("emailpw")
	e.expect("запрос", e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": "atk-" + uniq() + "@test.local"}}), 202)
	confirm, cancel := setEmailTokens(e, o.UserID)
	newPw := "new-pass-" + uniq()
	e.expect("смена пароля", e.do(req{method: "POST", path: "/auth/password", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_password": newPw}}), 200)
	e.expect("подтверждение после смены пароля", e.do(req{method: "POST", path: "/auth/email/confirm", body: map[string]string{"token": confirm}}), 410)
	e.expect("отмена после смены пароля", e.do(req{method: "POST", path: "/auth/email/cancel", body: map[string]string{"token": cancel}}), 410)

	// То же через «Забыли пароль?»: смену почты гасит и сброс.
	w := e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": newPw}})
	tok, _ := e.json(w)["access"].(string)
	e.expect("запрос 2", e.do(req{method: "POST", path: "/auth/email", token: tok,
		body: map[string]string{"current_password": newPw, "new_email": "atk2-" + uniq() + "@test.local"}}), 202)
	confirm2, _ := setEmailTokens(e, o.UserID)
	raw := "reset-" + uniq()
	e.db.Create(&models.PasswordReset{UserID: o.UserID, TokenHash: auth.HashToken(raw), ExpiresAt: time.Now().Add(time.Hour)})
	e.expect("сброс пароля", e.do(req{method: "POST", path: "/auth/password/reset", body: map[string]string{"token": raw, "password": "reset-pass-" + uniq()}}), 200)
	e.expect("подтверждение после сброса", e.do(req{method: "POST", path: "/auth/email/confirm", body: map[string]string{"token": confirm2}}), 410)
}

// AUTH-EMAIL-08: смену подтвердили быстрее, чем владелец увидел уведомление, — ссылка «вернуть почту»
// из письма на старый адрес: почта возвращается, все входы завершены, пароль сброшен, ссылка одноразовая.
func TestEmailChangeRevert(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("emailrev")
	atk := "atk-" + uniq() + "@test.local"
	e.expect("запрос", e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": atk}}), 202)
	confirm, _ := setEmailTokens(e, o.UserID)
	e.expect("подтверждение", e.do(req{method: "POST", path: "/auth/email/confirm", body: map[string]string{"token": confirm}}), 200)
	w := e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": atk, "password": o.Password}})
	e.expect("вход по новой почте", w, 200)
	atkTok, _ := e.json(w)["access"].(string)

	revert := "revert-" + uniq()
	e.db.Model(&models.EmailChange{}).Where("user_id = ? AND confirmed_at IS NOT NULL", o.UserID).
		Update("revert_token_hash", auth.HashToken(revert))
	w = e.do(req{method: "POST", path: "/auth/email/check", body: map[string]string{"token": revert, "kind": "revert"}})
	e.expect("проверка ссылки отката", w, 200)
	if e.json(w)["old_email"] != o.Email {
		t.Errorf("проверка отката: %v", e.json(w))
	}
	e.expect("откат", e.do(req{method: "POST", path: "/auth/email/revert", body: map[string]string{"token": revert}}), 200)
	e.expect("повторный откат", e.do(req{method: "POST", path: "/auth/email/revert", body: map[string]string{"token": revert}}), 410)
	e.expect("сессии захватчика", e.do(req{method: "GET", path: "/auth/me", token: atkTok}), 401)
	e.expect("вход по почте захватчика", e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": atk, "password": o.Password}}), 401)
	e.expect("старый пароль сброшен", e.do(req{method: "POST", path: "/auth/login", body: map[string]string{"email": o.Email, "password": o.Password}}), 401)
	var u models.User
	e.db.First(&u, o.UserID)
	if u.Email != o.Email {
		t.Errorf("почта не вернулась: %s", u.Email)
	}
}

// AUTH-EMAIL-09: «отправить ещё раз» — не больше 3 раз на запрос, срок ссылки не продлевается.
func TestEmailChangeResendLimits(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("emailresend")
	e.expect("запрос", e.do(req{method: "POST", path: "/auth/email", token: o.Token,
		body: map[string]string{"current_password": o.Password, "new_email": "victim-" + uniq() + "@test.local"}}), 202)
	var before models.EmailChange
	e.db.Where("user_id = ?", o.UserID).First(&before)
	codes := []int{}
	for i := 0; i < 5; i++ {
		e.db.Model(&models.EmailChange{}).Where("id = ?", before.ID).Update("sent_at", time.Now().Add(-2*time.Minute))
		codes = append(codes, e.do(req{method: "POST", path: "/auth/email/resend", token: o.Token}).Code)
	}
	if fmt.Sprint(codes) != "[200 200 200 429 429]" {
		t.Errorf("повторы: %v, ждали 3 раза 200, потом 429", codes)
	}
	var after models.EmailChange
	e.db.First(&after, before.ID)
	if !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Errorf("срок ссылки продлён: %v → %v", before.ExpiresAt, after.ExpiresAt)
	}
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

// REC-03: помощник правит текст рецепта (состав, как готовить); для кого блюдо, «готовим сейчас» и приёмы пищи
// остаются как были; в рецепте видно, кто правил последним. Создать и удалить рецепт помощник не может.
func TestHelperEditsRecipeText(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("recedit")
	h := e.member(o, "helper", "recedit")
	// Новый дом без приёмов пищи — заводим один (старые приёмы пищи ещё живут у старых клиентов).
	e.expect("приём пищи", e.do(req{method: "POST", path: "/admin/api/mealtimes", token: o.Token, org: o.Org,
		body: map[string]any{"name": "breakfast", "default_time": "08:30", "family_member": "all", "active": true}}), 201)
	meals := e.jsonList(e.do(req{method: "GET", path: "/admin/api/mealtimes", token: o.Token, org: o.Org}))
	w := e.do(req{method: "POST", path: "/admin/api/recipes", token: o.Token, org: o.Org, body: map[string]any{
		"name": "Сырники", "ingredients": "Творог — 150 г", "instructions": "Смешать", "family_member": "adult",
		"is_active": true, "servings": 1, "tags": "завтрак", "meal_time_ids": []any{meals[0]["id"]}}})
	e.expect("создание", w, 201)
	path := fmt.Sprintf("/admin/api/recipes/%.0f", e.json(w)["id"])

	w = e.do(req{method: "PUT", path: path, token: h.Token, org: h.Org, body: map[string]any{
		"name": "Сырники", "ingredients": "Творог — 150 г\nМука — 1 ст. л.", "instructions": "Смешать, жарить 3 мин с каждой стороны",
		"family_member": "baby", "is_active": false, "servings": 1, "tags": "завтрак", "meal_time_ids": []any{}}})
	e.expect("правка помощником", w, 200)
	r := e.json(w)
	if r["ingredients"] != "Творог — 150 г\nМука — 1 ст. л." || r["instructions"] != "Смешать, жарить 3 мин с каждой стороны" {
		t.Errorf("текст не сохранился: %v", r)
	}
	if r["family_member"] != "adult" || r["is_active"] != true {
		t.Errorf("помощник поменял, для кого блюдо или «готовим сейчас»: %v %v", r["family_member"], r["is_active"])
	}
	if mt, _ := r["meal_times"].([]any); len(mt) != 1 {
		t.Errorf("помощник поменял приёмы пищи: %v", r["meal_times"])
	}
	if r["updated_by_name"] != "helper recedit" {
		t.Errorf("кто правил: %v", r["updated_by_name"])
	}

	e.expect("создание помощником", e.do(req{method: "POST", path: "/admin/api/recipes", token: h.Token, org: h.Org,
		body: map[string]any{"name": "x"}}), 403)
	e.expect("удаление помощником", e.do(req{method: "DELETE", path: path, token: h.Token, org: h.Org}), 403)
}

// SHOP-04: список под меню — продукты рецептов (на 1 порцию) × порции, сложены, по отделам;
// вода не покупается, соль — «проверьте»; прошедшие приёмы не считаются; помощник меняет «берём»;
// наниматель заменил блюдо — продукты нового сами дописались в список, у старых пометка «убрано».
func TestShoppingFromMenu(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("shopmenu")
	h := e.member(o, "helper", "shopmenu")
	mk := func(name, ingr string) uint {
		w := e.do(req{method: "POST", path: "/admin/api/recipes", token: o.Token, org: o.Org, body: map[string]any{
			"name": name, "ingredients": ingr, "family_member": "all", "is_active": true, "servings": 1}})
		e.expect("рецепт "+name, w, 201)
		return uint(e.json(w)["id"].(float64))
	}
	borsch := mk("Борщ тест", "Говядина на кости — 100 г\nСвёкла — 70 г\nВода — 400 мл\nСоль\nСметана — 1 ст. л.")
	chicken := mk("Курица тест", "Курица (бёдра) — 250 г\nКартофель — 2 шт\nСоль")

	today := clock.OrgToday(e.db, o.Org)
	task := func(day time.Time, recipe uint, done bool) models.ScheduleTask {
		s, err := models.EnsureDailySchedule(e.db, o.Org, day, false)
		if err != nil {
			t.Fatal(err)
		}
		tk := models.ScheduleTask{OrganizationID: o.Org, ScheduleID: s.ID, TaskType: "meal", Title: "Обед - all",
			Time: "13:00", Completed: done}
		e.db.Create(&tk)
		var r models.Recipe
		e.db.First(&r, recipe)
		_ = e.db.Model(&tk).Association("Recipes").Append(&r)
		return tk
	}
	task(today, chicken, true) // уже сделано — не покупаем
	lunch := task(today.AddDate(0, 0, 1), borsch, false)

	e.expect("0 дней", e.do(req{method: "POST", path: "/helper/api/shopping-lists/from-menu", token: h.Token, org: h.Org, body: map[string]any{"days": 0}}), 400)
	e.expect("8 дней", e.do(req{method: "GET", path: "/helper/api/shopping-lists/menu?days=8", token: h.Token, org: h.Org}), 400)

	w := e.do(req{method: "GET", path: "/helper/api/shopping-lists/menu?days=3", token: h.Token, org: h.Org})
	e.expect("меню", w, 200)
	if days, _ := e.json(w)["days"].([]any); len(days) != 3 {
		t.Fatalf("дней в меню: %v", e.json(w)["days"])
	}

	w = e.do(req{method: "POST", path: "/helper/api/shopping-lists/from-menu", token: h.Token, org: h.Org, body: map[string]any{"days": 3}})
	e.expect("список из меню", w, 201)
	l := e.json(w)
	if l["status"] != "draft" || l["menu_from"] != today.Format("2006-01-02") || l["menu_pantry"] != "Соль" {
		t.Errorf("список: статус %v, с %v, есть дома %q", l["status"], l["menu_from"], l["menu_pantry"])
	}
	listID := l["id"].(float64)
	byName := func(m map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		items, _ := m["items"].([]any)
		for _, x := range items {
			it := x.(map[string]any)
			out[it["item"].(string)] = it
		}
		return out
	}
	items := byName(l)
	if len(items) != 3 || items["Вода"] != nil || items["Курица (бёдра)"] != nil {
		t.Fatalf("пункты: %v", items)
	}
	beet := items["Свёкла"]
	if beet["need_amount"] != 150.0 || beet["quantity"] != "150 г" || beet["category"] != "produce" {
		t.Errorf("свёкла (70 г × 2 → 150 г, овощи): %v", beet)
	}
	if items["Сметана"]["quantity"] != "2 ст. л." || items["Говядина на кости"]["category"] != "meat" {
		t.Errorf("сметана / говядина: %v %v", items["Сметана"], items["Говядина на кости"])
	}

	w = e.do(req{method: "PATCH", path: fmt.Sprintf("/helper/api/shopping-lists/%.0f/items/%.0f", listID, beet["id"]),
		token: h.Token, org: h.Org, body: map[string]any{"buy_amount": 100}})
	e.expect("берём меньше", w, 200)
	if e.json(w)["quantity"] != "100 г" {
		t.Errorf("после правки: %v", e.json(w)["quantity"])
	}

	e.expect("убрать борщ", e.do(req{method: "DELETE", path: fmt.Sprintf("/admin/api/tasks/%d/recipes/%d", lunch.ID, borsch), token: o.Token, org: o.Org}), 200)
	e.expect("поставить курицу", e.do(req{method: "POST", path: fmt.Sprintf("/admin/api/tasks/%d/recipes", lunch.ID), token: o.Token, org: o.Org,
		body: map[string]any{"recipe_id": chicken}}), 200)

	items = byName(e.json(e.do(req{method: "GET", path: fmt.Sprintf("/helper/api/shopping-lists/%.0f", listID), token: h.Token, org: h.Org})))
	if ch := items["Курица (бёдра)"]; ch == nil || ch["quantity"] != "500 г" {
		t.Errorf("курица не дописалась сама: %v", ch)
	}
	if items["Свёкла"]["quantity"] != "100 г" || !strings.Contains(items["Свёкла"]["sources"].(string), `"removed":true`) {
		t.Errorf("свёкла после замены: %v", items["Свёкла"])
	}

	// Так меняет блюдо приложение («Меню на завтра → Другое»): PUT задачи с recipe_ids.
	e.expect("вернуть борщ", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/tasks/%d", lunch.ID), token: o.Token, org: o.Org,
		body: map[string]any{"time": "13:00", "end_time": "", "title": "Обед - all", "description": "", "recipe_ids": []uint{borsch}}}), 200)
	items = byName(e.json(e.do(req{method: "GET", path: fmt.Sprintf("/helper/api/shopping-lists/%.0f", listID), token: h.Token, org: h.Org})))
	if items["Свёкла"]["quantity"] != "250 г" {
		t.Errorf("свёкла после возврата борща (100 + 150): %v", items["Свёкла"]["quantity"])
	}
	if !strings.Contains(items["Курица (бёдра)"]["sources"].(string), `"removed":true`) {
		t.Errorf("курица после замены на борщ: %v", items["Курица (бёдра)"])
	}
}
