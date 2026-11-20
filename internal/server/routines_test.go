package server

import (
	"fmt"
	"testing"

	"podlevskikh/awesomeProject/internal/models"
)

// «Ритм дома» — регулярные задачи (helper/docs/work/routines/PRD.md, кейсы RT-*).

type rtHome struct {
	o, h, h2 acct
}

// rtSetup: дом с двумя помощниками без праздников страны, выходной у всех — воскресенье.
func rtSetup(e *env, tag string) rtHome {
	o := e.owner(tag)
	h := e.member(o, "helper", tag)
	h2 := e.member(o, "helper", tag+"2")
	for _, uid := range []uint{h.UserID, h2.UserID} {
		e.expect("без праздников", e.do(req{method: "PATCH", path: fmt.Sprintf("/orgs/%d/members/%d/schedule-settings", o.Org, uid),
			token: o.Token, org: o.Org, body: map[string]any{"holiday_country": "-", "days_off": []int{7}}}), 200)
	}
	return rtHome{o: o, h: h, h2: h2}
}

func (e *env) dishType(o acct, name string) float64 {
	w := e.do(req{method: "POST", path: "/admin/api/dish-types", token: o.Token, org: o.Org, body: map[string]any{"name": name}})
	e.expect("тип "+name, w, 201)
	return e.json(w)["id"].(float64)
}

func (e *env) recipe(o acct, name string, typeID float64) float64 {
	w := e.do(req{method: "POST", path: "/admin/api/recipes", token: o.Token, org: o.Org,
		body: map[string]any{"name": name, "is_active": true, "dish_type_id": typeID, "servings": 1}})
	e.expect("рецепт "+name, w, 201)
	return e.json(w)["id"].(float64)
}

func (e *env) routine(o acct, body map[string]any) float64 {
	w := e.do(req{method: "POST", path: "/admin/api/routines", token: o.Token, org: o.Org, body: body})
	e.expect("регулярная задача", w, 201)
	return e.json(w)["id"].(float64)
}

// dayTasks — задачи дня глазами пользователя a.
func (e *env) dayTasks(a acct, date string) []map[string]any {
	m := e.json(e.do(req{method: "GET", path: "/helper/api/schedule/date/" + date, token: a.Token, org: a.Org}))
	out := []map[string]any{}
	if ts, ok := m["tasks"].([]any); ok {
		for _, x := range ts {
			out = append(out, x.(map[string]any))
		}
	}
	return out
}

func findTask(ts []map[string]any, title string) map[string]any {
	for _, t := range ts {
		if t["title"] == title {
			return t
		}
	}
	return nil
}

// RT-01, RT-09: обед из двух блюд по типам — блюда с порциями, обычные порции из регулярной задачи,
// супы по кругу без повторов.
func TestRoutineMealFromDishTypes(t *testing.T) {
	e := newEnv(t, 1000)
	hm := rtSetup(e, "rtmeal")
	withNow(t, "2026-11-16T06:00:00Z") // понедельник
	soup := e.dishType(hm.o, "Суп")
	main := e.dishType(hm.o, "Второе")
	for _, n := range []string{"Борщ", "Щи", "Солянка"} {
		e.recipe(hm.o, n, soup)
	}
	e.recipe(hm.o, "Плов", main)
	e.routine(hm.o, map[string]any{"kind": "food", "title": "Обед", "repeat_kind": "weekdays", "weekdays": []int{1, 2, 3, 4, 5},
		"time_mode": "exact", "times": []string{"13:00"}, "assigned_to_user_id": hm.h.UserID,
		"dishes": []map[string]any{{"dish_type_id": soup, "servings": 1}, {"dish_type_id": main, "servings": 2}}})
	var soups []float64
	for _, d := range []string{"2026-11-16", "2026-11-17", "2026-11-18", "2026-11-19", "2026-11-20", "2026-11-23"} {
		task := findTask(e.dayTasks(hm.h, d), "Обед")
		if task == nil {
			t.Fatalf("%s: нет обеда у помощника", d)
		}
		if task["usual_servings"] != float64(2) || task["servings"] != float64(2) {
			t.Errorf("%s: порции %v / обычно %v", d, task["servings"], task["usual_servings"])
		}
		dishes, _ := task["dishes"].([]any)
		if len(dishes) != 2 {
			t.Fatalf("%s: блюда %v", d, task["dishes"])
		}
		d1, d2 := dishes[0].(map[string]any), dishes[1].(map[string]any)
		if d1["dish_type"] != "Суп" || d1["servings"] != float64(1) || d2["dish_type"] != "Второе" || d2["servings"] != float64(2) {
			t.Errorf("%s: блюда %v", d, dishes)
		}
		id, _ := d1["recipe_id"].(float64)
		soups = append(soups, id)
	}
	// По кругу: супов три — в любых трёх обедах подряд каждый по разу.
	for i := 0; i+3 <= len(soups); i++ {
		w := map[float64]bool{soups[i]: true, soups[i+1]: true, soups[i+2]: true}
		if len(w) != 3 {
			t.Errorf("супы %v: в обедах %d–%d повтор", soups, i+1, i+3)
		}
	}
	if task := findTask(e.dayTasks(hm.h, "2026-11-22"), "Обед"); task != nil {
		t.Errorf("в воскресенье обеда нет по правилу, а он есть")
	}
}

// RT-02: «каждые 3 дня» с пятницы — пт, пн, чт; день недели «плывёт».
func TestRoutineEveryNDays(t *testing.T) {
	e := newEnv(t, 1000)
	hm := rtSetup(e, "rtevery")
	withNow(t, "2026-11-20T06:00:00Z") // пятница
	e.routine(hm.o, map[string]any{"kind": "pet", "title": "Лоток кота", "repeat_kind": "every_n", "every_n": 3,
		"start_date": "2026-11-20", "time_mode": "any", "assigned_to_user_id": hm.h.UserID})
	for d, want := range map[string]bool{"2026-11-20": true, "2026-11-21": false, "2026-11-23": true, "2026-11-24": false, "2026-11-26": true} {
		got := findTask(e.dayTasks(hm.o, d), "Лоток кота") != nil
		if got != want {
			t.Errorf("%s: задача есть=%v, ждали %v", d, got, want)
		}
	}
	e.expect("N меньше единицы", e.do(req{method: "POST", path: "/admin/api/routines", token: hm.o.Token, org: hm.o.Org,
		body: map[string]any{"kind": "pet", "title": "x", "repeat_kind": "every_n", "every_n": 0, "time_mode": "any",
			"assigned_to_user_id": hm.h.UserID}}), 400)
}

// RT-03, RT-04, RT-05, RT-06: помощник отдыхает — задача у того, кто поставил; передать другому, не делать;
// еду не переносим; «каждые N дней» после переноса считаются от нового дня.
func TestRoutineDayOff(t *testing.T) {
	e := newEnv(t, 1000)
	hm := rtSetup(e, "rtoff")
	withNow(t, "2026-11-16T06:00:00Z") // понедельник
	e.expect("отгул в среду", e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/members/%d/day-overrides", hm.o.Org, hm.h.UserID),
		token: hm.o.Token, org: hm.o.Org, body: map[string]any{"kind": "day_off", "start_date": "2026-11-18", "end_date": "2026-11-18"}}), 201)
	flowers := e.routine(hm.o, map[string]any{"kind": "other", "title": "Полить цветы", "repeat_kind": "weekdays",
		"weekdays": []int{1, 2, 3, 4, 5, 6}, "time_mode": "any", "assigned_to_user_id": hm.h.UserID,
		"checklist": []string{"Гостиная", "Балкон"}})
	soup := e.dishType(hm.o, "Суп")
	e.recipe(hm.o, "Борщ", soup)
	lunch := e.routine(hm.o, map[string]any{"kind": "food", "title": "Обед", "repeat_kind": "weekdays", "weekdays": []int{3},
		"time_mode": "exact", "times": []string{"13:00"}, "assigned_to_user_id": hm.h.UserID,
		"dishes": []map[string]any{{"dish_type_id": soup, "servings": 2}}})

	// RT-03: в отгул у помощника пусто, задачи — у того, кто поставил.
	if ts := e.dayTasks(hm.h, "2026-11-18"); len(ts) != 0 {
		t.Errorf("у помощника в отгул: %v", ts)
	}
	ownerTask := findTask(e.dayTasks(hm.o, "2026-11-18"), "Полить цветы")
	if ownerTask == nil || ownerTask["assigned_to_user_id"] != float64(hm.o.UserID) {
		t.Fatalf("цветы в отгул помощника должны быть на том, кто поставил: %v", ownerTask)
	}
	off := e.json(e.do(req{method: "GET", path: "/admin/api/routines/day-off?date=2026-11-18", token: hm.o.Token, org: hm.o.Org}))
	items, _ := off["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("в отгул две задачи помощника: %v", off)
	}
	working, _ := off["working"].([]any)
	if len(working) != 1 || working[0].(map[string]any)["id"] != float64(hm.h2.UserID) {
		t.Errorf("работает второй помощник: %v", working)
	}

	// RT-04: передать второму помощнику — задача у него только в этот день.
	e.expect("передать", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/routines/%.0f/day-decision", flowers), token: hm.o.Token,
		org: hm.o.Org, body: map[string]any{"date": "2026-11-18", "action": "user", "user_id": hm.h2.UserID}}), 200)
	if findTask(e.dayTasks(hm.h2, "2026-11-18"), "Полить цветы") == nil {
		t.Errorf("второй помощник не видит переданную задачу")
	}
	if t2 := findTask(e.dayTasks(hm.h2, "2026-11-19"), "Полить цветы"); t2 != nil {
		t.Errorf("в четверг задача снова у первого помощника, а у второго: %v", t2)
	}

	// RT-06: еду не переносим; «не делать» убирает обед из дня.
	e.expect("еду не переносим", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/routines/%.0f/day-decision", lunch), token: hm.o.Token,
		org: hm.o.Org, body: map[string]any{"date": "2026-11-18", "action": "move"}}), 400)
	e.expect("обед не делать", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/routines/%.0f/day-decision", lunch), token: hm.o.Token,
		org: hm.o.Org, body: map[string]any{"date": "2026-11-18", "action": "skip"}}), 200)
	if findTask(e.dayTasks(hm.o, "2026-11-18"), "Обед") != nil {
		t.Errorf("обед «не делать» остался")
	}

	// RT-05: «каждые 2 дня» с 18-го, отгул 18-го → перенос на 19-е, следующий раз — 21-е.
	cat := e.routine(hm.o, map[string]any{"kind": "pet", "title": "Лоток кота", "repeat_kind": "every_n", "every_n": 2,
		"start_date": "2026-11-18", "time_mode": "any", "assigned_to_user_id": hm.h.UserID})
	e.expect("перенести", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/routines/%.0f/day-decision", cat), token: hm.o.Token,
		org: hm.o.Org, body: map[string]any{"date": "2026-11-18", "action": "move"}}), 200)
	for d, want := range map[string]bool{"2026-11-18": false, "2026-11-19": true, "2026-11-20": false, "2026-11-21": true} {
		if got := findTask(e.dayTasks(hm.o, d), "Лоток кота") != nil; got != want {
			t.Errorf("лоток %s: есть=%v, ждали %v", d, got, want)
		}
	}
	e.expect("исполнитель работает — решение не нужно", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/routines/%.0f/day-decision", flowers),
		token: hm.o.Token, org: hm.o.Org, body: map[string]any{"date": "2026-11-19", "action": "skip"}}), 400)
}

// RT-07, RT-10: пауза убирает будущие невыполненные задачи, сделанные остаются; «Что входит» отмечает помощник.
func TestRoutinePauseAndChecklist(t *testing.T) {
	e := newEnv(t, 1000)
	hm := rtSetup(e, "rtpause")
	withNow(t, "2026-11-16T06:00:00Z")
	id := e.routine(hm.o, map[string]any{"kind": "clean", "title": "Кухня", "repeat_kind": "weekdays", "weekdays": []int{1, 2, 3, 4, 5, 6},
		"time_mode": "any", "assigned_to_user_id": hm.h.UserID, "checklist": []string{"Плита", "Пол"}})
	today := findTask(e.dayTasks(hm.h, "2026-11-16"), "Кухня")
	if today == nil {
		t.Fatal("нет уборки кухни сегодня")
	}
	tid := today["id"].(float64)
	w := e.do(req{method: "POST", path: fmt.Sprintf("/helper/api/tasks/%.0f/checklist/1", tid), token: hm.h.Token, org: hm.h.Org,
		body: map[string]any{"done": true}})
	e.expect("отметить пункт", w, 200)
	if fmt.Sprint(e.json(w)["checklist"]) != `[{"text":"Плита","done":false},{"text":"Пол","done":true}]` {
		t.Errorf("что входит: %v", e.json(w)["checklist"])
	}
	e.expect("сделано", e.do(req{method: "POST", path: fmt.Sprintf("/helper/api/tasks/%.0f/complete", tid), token: hm.h.Token, org: hm.h.Org}), 200)
	if findTask(e.dayTasks(hm.h, "2026-11-17"), "Кухня") == nil {
		t.Fatal("нет уборки во вторник")
	}
	e.expect("пауза", e.do(req{method: "PATCH", path: fmt.Sprintf("/admin/api/routines/%.0f/active", id), token: hm.o.Token, org: hm.o.Org,
		body: map[string]any{"active": false}}), 200)
	if findTask(e.dayTasks(hm.h, "2026-11-17"), "Кухня") != nil {
		t.Errorf("на паузе задача во вторник осталась")
	}
	if findTask(e.dayTasks(hm.h, "2026-11-16"), "Кухня") == nil {
		t.Errorf("сделанная сегодня задача пропала после паузы")
	}
	// RT-08: модератор ритм не меняет, помощник — тоже.
	m := e.member(hm.o, "manager", "rtpause")
	e.expect("модератор", e.do(req{method: "DELETE", path: fmt.Sprintf("/admin/api/routines/%.0f", id), token: m.Token, org: m.Org}), 403)
	var n int64
	e.db.Model(&models.Routine{}).Where("id = ?", uint(id)).Count(&n)
	if n != 1 {
		t.Errorf("регулярная задача удалена модератором")
	}
}

// RT-11: тип блюда — свой у дома; занятый тип не удалить.
func TestDishTypes(t *testing.T) {
	e := newEnv(t, 1000)
	hm := rtSetup(e, "rttypes")
	soup := e.dishType(hm.o, "Суп")
	e.expect("повтор названия", e.do(req{method: "POST", path: "/admin/api/dish-types", token: hm.o.Token, org: hm.o.Org,
		body: map[string]any{"name": "суп"}}), 409)
	r := e.recipe(hm.o, "Борщ", soup)
	e.expect("занятый тип", e.do(req{method: "DELETE", path: fmt.Sprintf("/admin/api/dish-types/%.0f", soup), token: hm.o.Token, org: hm.o.Org}), 409)
	// PUT рецепта без поля типа тип не затирает.
	e.expect("правка рецепта", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/recipes/%.0f", r), token: hm.o.Token, org: hm.o.Org,
		body: map[string]any{"name": "Борщ", "is_active": true}}), 200)
	got := e.json(e.do(req{method: "GET", path: fmt.Sprintf("/admin/api/recipes/%.0f", r), token: hm.o.Token, org: hm.o.Org}))
	if got["dish_type_id"] != soup {
		t.Errorf("тип рецепта после правки: %v", got["dish_type_id"])
	}
	other := e.owner("rttypes-other")
	e.expect("чужой тип в рецепте", e.do(req{method: "POST", path: "/admin/api/recipes", token: other.Token, org: other.Org,
		body: map[string]any{"name": "x", "dish_type_id": soup}}), 400)
}

// RT-12: день уже собран, потом помощнику дали отгул — его регулярная задача переходит к тому,
// кто её поставил; отгул сняли — задача снова у помощника.
func TestRoutineDayOffAfterDayBuilt(t *testing.T) {
	e := newEnv(t, 1000)
	hm := rtSetup(e, "rtlate")
	withNow(t, "2026-11-16T06:00:00Z") // понедельник
	e.routine(hm.o, map[string]any{"kind": "other", "title": "Полить цветы", "repeat_kind": "weekdays",
		"weekdays": []int{1, 2, 3, 4, 5, 6}, "time_mode": "any", "assigned_to_user_id": hm.h.UserID})
	who := func() any {
		task := findTask(e.dayTasks(hm.o, "2026-11-18"), "Полить цветы")
		if task == nil {
			t.Fatalf("в среду нет задачи")
		}
		return task["assigned_to_user_id"]
	}
	if got := who(); got != float64(hm.h.UserID) {
		t.Fatalf("до отгула исполнитель %v, ждали помощника %d", got, hm.h.UserID)
	}
	w := e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/members/%d/day-overrides", hm.o.Org, hm.h.UserID),
		token: hm.o.Token, org: hm.o.Org, body: map[string]any{"kind": "day_off", "start_date": "2026-11-18"}})
	e.expect("отгул в среду", w, 201)
	if got := who(); got != float64(hm.o.UserID) {
		t.Errorf("после отгула исполнитель %v, ждали поставившего %d", got, hm.o.UserID)
	}
	id := e.json(w)["id"].(float64)
	e.expect("снять отгул", e.do(req{method: "DELETE", path: fmt.Sprintf("/orgs/%d/members/%d/day-overrides/%d", hm.o.Org, hm.h.UserID, int(id)),
		token: hm.o.Token, org: hm.o.Org}), 200)
	if got := who(); got != float64(hm.h.UserID) {
		t.Errorf("после снятия отгула исполнитель %v, ждали помощника %d", got, hm.h.UserID)
	}
}

// RT-13: задачу из ритма удалили на один день — она не появляется снова, а в другие дни есть.
func TestRoutineTaskRemovedForDay(t *testing.T) {
	e := newEnv(t, 1000)
	hm := rtSetup(e, "rtrm")
	withNow(t, "2026-11-16T06:00:00Z") // понедельник
	e.routine(hm.o, map[string]any{"kind": "other", "title": "Полить цветы", "repeat_kind": "weekdays",
		"weekdays": []int{1, 2, 3, 4, 5, 6}, "time_mode": "any", "assigned_to_user_id": hm.h.UserID})
	task := findTask(e.dayTasks(hm.o, "2026-11-17"), "Полить цветы")
	if task == nil {
		t.Fatalf("во вторник нет задачи")
	}
	e.expect("убрать на вторник", e.do(req{method: "DELETE", path: fmt.Sprintf("/admin/api/custom-tasks/%d", int(task["id"].(float64))),
		token: hm.o.Token, org: hm.o.Org}), 200)
	if findTask(e.dayTasks(hm.o, "2026-11-17"), "Полить цветы") != nil {
		t.Errorf("убранная на вторник задача появилась снова")
	}
	if findTask(e.dayTasks(hm.o, "2026-11-18"), "Полить цветы") == nil {
		t.Errorf("в среду задачи нет")
	}
}

// RT-16: день собирают одновременно (два устройства, планировщик, второй сервер при выкатке) —
// задача регулярной задачи всё равно одна.
func TestRoutineConcurrentDayBuild(t *testing.T) {
	e := newEnv(t, 1000)
	hm := rtSetup(e, "rtconc")
	withNow(t, "2026-11-16T06:00:00Z") // понедельник
	e.routine(hm.o, map[string]any{"kind": "other", "title": "Проветрить", "repeat_kind": "weekdays",
		"weekdays": []int{1, 2, 3, 4, 5, 6}, "time_mode": "exact", "times": []string{"09:00", "18:00"},
		"assigned_to_user_id": hm.h.UserID})
	const date = "2026-12-02" // вне 7 дней — день ещё не собран
	const n = 20
	// Прогрев пула: у всех запросов уже есть соединение, и они правда идут одновременно.
	warm := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() { e.db.Exec("SELECT pg_sleep(0.05)"); warm <- struct{}{} }()
	}
	for i := 0; i < 10; i++ {
		<-warm
	}
	start, done := make(chan struct{}), make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			<-start // все запросы стартуют разом
			e.do(req{method: "GET", path: "/helper/api/schedule/date/" + date, token: hm.o.Token, org: hm.o.Org})
		}()
	}
	close(start)
	for i := 0; i < n; i++ {
		<-done
	}
	var got int64 // считаем в базе: ответ API мог бы скрыть копии
	e.db.Raw(`SELECT count(*) FROM schedule_tasks st JOIN daily_schedules ds ON ds.id = st.schedule_id
		WHERE ds.organization_id = ? AND (ds.date AT TIME ZONE 'UTC')::date = ?::date AND st.title = ?`, hm.o.Org, date, "Проветрить").Scan(&got)
	if got != 2 {
		t.Fatalf("задач «Проветрить» в дне: %d, ждали 2 (09:00 и 18:00)", got)
	}
}
