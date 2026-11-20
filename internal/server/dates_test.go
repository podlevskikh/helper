package server

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/scheduler"
)

func withNow(t *testing.T, ts string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatal(err)
	}
	old := clock.Now
	clock.Now = func() time.Time { return at }
	t.Cleanup(func() { clock.Now = old })
	return at
}

func titles(e *env, m map[string]any) []string {
	var out []string
	if ts, ok := m["tasks"].([]any); ok {
		for _, x := range ts {
			out = append(out, fmt.Sprint(x.(map[string]any)["title"]))
		}
	}
	return out
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// DATE-01: в 00:30 по Никосии (22:30 UTC прошлых суток) «сегодня» — уже новая дата, и /today совпадает с /date/<дата>.
func TestTodayUsesHouseTimezone(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("tz")
	withNow(t, "2026-11-19T22:30:00Z")
	e.expect("задача на 20-е", e.do(req{method: "POST", path: "/admin/api/custom-tasks", token: o.Token, org: o.Org,
		body: map[string]any{"date": "2026-11-20", "title": "утро 20-го"}}), 201)
	e.expect("задача на 19-е", e.do(req{method: "POST", path: "/admin/api/custom-tasks", token: o.Token, org: o.Org,
		body: map[string]any{"date": "2026-11-19", "title": "вечер 19-го"}}), 201)
	today := titles(e, e.json(e.do(req{method: "GET", path: "/helper/api/schedule/today", token: o.Token, org: o.Org})))
	byDate := titles(e, e.json(e.do(req{method: "GET", path: "/helper/api/schedule/date/2026-11-20", token: o.Token, org: o.Org})))
	if !has(today, "утро 20-го") || has(today, "вечер 19-го") {
		t.Errorf("сегодня по Никосии должно быть 20-е: %v", today)
	}
	if fmt.Sprint(today) != fmt.Sprint(byDate) {
		t.Errorf("/today %v ≠ /date/2026-11-20 %v", today, byDate)
	}
}

// DATE-03: повторяющаяся задача только в свои дни недели и не раньше start_date.
func TestRecurringWeekdays(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("rec")
	withNow(t, "2026-11-16T08:00:00Z") // понедельник
	e.expect("только воскресенье", e.do(req{method: "POST", path: "/admin/api/recurring-tasks", token: o.Token, org: o.Org,
		body: map[string]any{"title": "по воскресеньям", "weekdays": []int{7}, "start_date": "2026-11-16"}}), 201)
	e.expect("будни с 18-го", e.do(req{method: "POST", path: "/admin/api/recurring-tasks", token: o.Token, org: o.Org,
		body: map[string]any{"title": "будни", "weekdays": []int{1, 2, 3, 4, 5}, "start_date": "2026-11-18"}}), 201)
	want := map[string][2]bool{ // дата → [воскресная, будничная]
		"2026-11-16": {false, false}, "2026-11-17": {false, false}, "2026-11-18": {false, true},
		"2026-11-21": {false, false}, "2026-11-22": {true, false}, "2026-11-23": {false, true},
	}
	for d, w := range want {
		got := titles(e, e.json(e.do(req{method: "GET", path: "/helper/api/schedule/date/" + d, token: o.Token, org: o.Org})))
		if has(got, "по воскресеньям") != w[0] || has(got, "будни") != w[1] {
			t.Errorf("%s: %v, ждали воскресная=%v будни=%v", d, got, w[0], w[1])
		}
	}
}

// DATE-05: параллельные запросы не создают дублей дня.
func TestEnsureDailyScheduleConcurrent(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("dup")
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := models.EnsureDailySchedule(e.db, o.Org, day, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var n int64
	e.db.Model(&models.DailySchedule{}).Where("organization_id = ? AND date = ?", o.Org, day).Count(&n)
	if n != 1 {
		t.Errorf("дней: %d, ждали 1", n)
	}
}

// SCHED-01: повторная сборка дня и «перезапуск» планировщика не дублируют задачи.
func TestSchedulerIdempotent(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("sched")
	e.expect("зона", e.do(req{method: "POST", path: "/admin/api/zones", token: o.Token, org: o.Org,
		body: map[string]any{"name": "кухня", "frequency_per_week": 7, "priority": "high"}}), 201)
	// Уборка ставится только в рабочие дни дома: берём ближайший рабочий день не раньше послезавтра,
	// иначе тест падает, когда послезавтра выпадает на выходной.
	day := clock.OrgToday(e.db, o.Org).AddDate(0, 0, 2)
	for !slices.Contains(calendar.OrgWorkdays(e.db, o.Org), calendar.ISOWeekday(day)) {
		day = day.AddDate(0, 0, 1)
	}
	for i := 0; i < 3; i++ {
		if err := scheduler.NewScheduler(e.db).GenerateScheduleForOrgDate(o.Org, day); err != nil {
			t.Fatal(err)
		}
	}
	var days, tasks int64
	e.db.Model(&models.DailySchedule{}).Where("organization_id = ? AND date = ?", o.Org, day).Count(&days)
	e.db.Model(&models.ScheduleTask{}).Joins("JOIN daily_schedules ds ON ds.id = schedule_tasks.schedule_id").
		Where("ds.organization_id = ? AND ds.date = ? AND schedule_tasks.task_type = 'cleaning'", o.Org, day).Count(&tasks)
	if days != 1 || tasks != 1 {
		t.Errorf("дней %d, задач уборки %d — ждали 1 и 1", days, tasks)
	}
}
