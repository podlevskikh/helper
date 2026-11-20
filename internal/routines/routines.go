// Package routines — «Ритм дома»: из регулярных задач (models.Routine) собираются задачи дня.
//
// Один генератор вместо прежних четырёх (приёмы пищи, зоны уборки, распорядок ребёнка,
// повторяющиеся задачи). Идемпотентен: задача регулярной задачи на день и время ставится один раз;
// удалённая вручную (RoutineSkip) больше не появляется. Требования — helper/docs/work/routines/PRD.md.
package routines

import (
	"encoding/json"
	"log"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"
)

// Days — на сколько дней вперёд собираем задачи (как планировщик).
const Days = 7

// ParseWeekdays: «1,3,5» → {1,3,5}.
func ParseWeekdays(s string) map[int]bool {
	out := map[int]bool{}
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n >= 1 && n <= 7 {
			out[n] = true
		}
	}
	return out
}

func day(t time.Time) time.Time { return clock.Day(t) }

// daysBetween — сколько суток от a до b (b ≥ a).
func daysBetween(a, b time.Time) int {
	return int(day(b).Sub(day(a)).Hours() / 24)
}

// DueByRule — по правилу повторения задача приходится на этот день (без решений на день и пропусков).
func DueByRule(r models.Routine, date time.Time) bool {
	date = day(date)
	if date.Before(day(r.StartDate)) {
		return false
	}
	switch r.RepeatKind {
	case models.RepeatEveryN:
		n := r.EveryN
		if n < 1 {
			n = 1
		}
		anchor := day(r.AnchorDate)
		if date.Before(anchor) {
			return false
		}
		return daysBetween(anchor, date)%n == 0
	default:
		return ParseWeekdays(r.Weekdays)[calendar.ISOWeekday(date)]
	}
}

// NextDates — ближайшие n дат по правилу, начиная с from.
func NextDates(r models.Routine, from time.Time, n int) []time.Time {
	var out []time.Time
	d := day(from)
	for i := 0; i < 400 && len(out) < n; i++ {
		if DueByRule(r, d) {
			out = append(out, d)
		}
		d = d.AddDate(0, 0, 1)
	}
	return out
}

// StandardServings — «стандартное» число порций приёма пищи (models.StandardServings).
func StandardServings(dishes []models.RoutineDish) int { return models.StandardServings(dishes) }

// TaskType — тип задачи дня по категории «Ритма дома» (на нём завязаны еда, иконки, итог дня).
func TaskType(kind string) string {
	switch kind {
	case models.RoutineFood:
		return "meal"
	case models.RoutineClean:
		return "cleaning"
	case models.RoutineKid:
		return "childcare"
	}
	return "custom"
}

// slot — одно время задачи: начало и конец (для промежутка).
type slot struct{ from, to string }

func slots(r models.Routine) []slot {
	ts := r.TimeList()
	switch r.TimeMode {
	case models.TimeExact:
		out := []slot{}
		for _, t := range ts {
			if t != "" {
				out = append(out, slot{from: t})
			}
		}
		if len(out) > 0 {
			return out
		}
	case models.TimeSpan:
		if len(ts) == 2 {
			return []slot{{from: ts[0], to: ts[1]}}
		}
	}
	return []slot{{}}
}

// assignee — кому задача в этот день и ставить ли её вообще.
// Исполнитель отдыхает → тому, кто поставил (или по решению на день: другому, перенести, не делать).
func assignee(db *gorm.DB, r models.Routine, date time.Time) (uint, bool) {
	var dec models.RoutineDayDecision
	hasDec := db.Where("routine_id = ? AND date = ?", r.ID, date).First(&dec).Error == nil
	if calendar.IsWorking(db, r.OrganizationID, r.AssignedToUserID, date) && !hasDec {
		return r.AssignedToUserID, true
	}
	if hasDec {
		switch dec.Action {
		case models.DecisionSkip, models.DecisionMove:
			return 0, false
		case models.DecisionUser:
			if dec.UserID != nil {
				return *dec.UserID, true
			}
		}
	}
	return Owner(db, r), true
}

// Owner — тот, кто поставил задачу; ушёл из дома — основной админ.
func Owner(db *gorm.DB, r models.Routine) uint {
	var n int64
	db.Model(&models.Membership{}).Where("organization_id = ? AND user_id = ? AND status = ?",
		r.OrganizationID, r.CreatedBy, models.MembershipActive).Count(&n)
	if n > 0 {
		return r.CreatedBy
	}
	var org models.Organization
	if db.First(&org, r.OrganizationID).Error == nil && org.OwnerUserID != 0 {
		return org.OwnerUserID
	}
	return r.CreatedBy
}

// movedHere — регулярные задачи, перенесённые на этот день решением «перенести».
func movedHere(db *gorm.DB, orgID uint, date time.Time) map[uint]bool {
	var ids []uint
	db.Model(&models.RoutineDayDecision{}).
		Joins("JOIN routines ON routines.id = routine_day_decisions.routine_id").
		Where("routines.organization_id = ? AND routine_day_decisions.action = ? AND routine_day_decisions.target_date = ?",
			orgID, models.DecisionMove, date).
		Pluck("routine_day_decisions.routine_id", &ids)
	out := map[uint]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// HasAny — есть ли у дома активные регулярные задачи.
func HasAny(db *gorm.DB, orgID uint) bool {
	var n int64
	db.Model(&models.Routine{}).Where("organization_id = ? AND active = ?", orgID, true).Count(&n)
	return n > 0
}

// MaterializeDay ставит в день дома задачи из регулярных. Возвращает true, если что-то создано.
func MaterializeDay(db *gorm.DB, orgID uint, schedule *models.DailySchedule, date time.Time) bool {
	date = day(date)
	var list []models.Routine
	if err := db.Preload("Dishes", func(q *gorm.DB) *gorm.DB { return q.Order("position") }).
		Preload("Checklist", func(q *gorm.DB) *gorm.DB { return q.Order("position") }).
		Where("organization_id = ? AND active = ? AND start_date <= ?", orgID, true, date).
		Find(&list).Error; err != nil || len(list) == 0 {
		return false
	}
	moved := movedHere(db, orgID, date)
	created := false
	for _, r := range list {
		if !DueByRule(r, date) && !moved[r.ID] {
			continue
		}
		var n int64
		db.Model(&models.RoutineSkip{}).Where("routine_id = ? AND date = ?", r.ID, date).Count(&n)
		if n > 0 {
			continue
		}
		who, ok := r.AssignedToUserID, true
		if !moved[r.ID] || DueByRule(r, date) {
			who, ok = assignee(db, r, date)
		}
		if !ok {
			continue
		}
		for _, s := range slots(r) {
			db.Model(&models.ScheduleTask{}).
				Where("schedule_id = ? AND routine_id = ? AND time = ?", schedule.ID, r.ID, s.from).Count(&n)
			if n > 0 {
				continue
			}
			if err := createTask(db, r, schedule, date, s, who); err != nil {
				log.Printf("routines: routine %d, %s: %v", r.ID, date.Format("2006-01-02"), err)
				continue
			}
			created = true
		}
	}
	return created
}

// categoryFor — категория задачи: своя у регулярной задачи или системная по виду.
func categoryFor(db *gorm.DB, r models.Routine) *uint {
	if r.TaskCategoryID != nil {
		return r.TaskCategoryID
	}
	var cat models.TaskCategory
	if db.Where("organization_id = ? AND system_key = ?", r.OrganizationID, TaskType(r.Kind)).First(&cat).Error == nil {
		return &cat.ID
	}
	return nil
}

func createTask(db *gorm.DB, r models.Routine, schedule *models.DailySchedule, date time.Time, s slot, who uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		id := r.ID
		uid := who
		task := models.ScheduleTask{
			OrganizationID:   r.OrganizationID,
			ScheduleID:       schedule.ID,
			TaskType:         TaskType(r.Kind),
			Time:             s.from,
			EndTime:          s.to,
			Title:            r.Title,
			Description:      r.Description,
			Priority:         r.Priority,
			TaskCategoryID:   categoryFor(tx, r),
			AssignedToUserID: &uid,
			RoutineID:        &id,
		}
		if len(r.Checklist) > 0 {
			items := make([]models.ChecklistEntry, 0, len(r.Checklist))
			for _, c := range r.Checklist {
				items = append(items, models.ChecklistEntry{Text: c.Text})
			}
			raw, _ := json.Marshal(items)
			task.Checklist = string(raw)
		}
		var dishes []models.TaskDish
		if r.Kind == models.RoutineFood && len(r.Dishes) > 0 {
			std := StandardServings(r.Dishes)
			task.Servings = std
			task.Duration = 60
			used := map[uint]bool{}
			var names []string
			for _, d := range r.Dishes {
				dt := d.DishTypeID
				td := models.TaskDish{Position: d.Position, DishTypeID: &dt}
				if d.Servings != std {
					td.Servings = d.Servings
				}
				if rec := PickRecipe(tx, r, d, date, used); rec != nil {
					rid := rec.ID
					td.RecipeID = &rid
					used[rid] = true
					names = append(names, rec.Name)
					if task.RecipeID == nil {
						task.RecipeID = &rid
					}
				}
				dishes = append(dishes, td)
			}
			if task.Description == "" {
				task.Description = strings.Join(names, " · ")
			}
		}
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		for i := range dishes {
			dishes[i].TaskID = task.ID
			if err := tx.Create(&dishes[i]).Error; err != nil {
				return err
			}
			if dishes[i].RecipeID != nil {
				if err := tx.Exec("INSERT INTO meal_recipes (schedule_task_id, recipe_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
					task.ID, *dishes[i].RecipeID).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// PickRecipe — рецепт типа блюда по кругу без повторов: из тех, что не готовили для этого блюда этой
// регулярной задачи за последние max(рецептов, 7) дней; прошли все — круг заново.
func PickRecipe(db *gorm.DB, r models.Routine, d models.RoutineDish, date time.Time, exclude map[uint]bool) *models.Recipe {
	var recipes []models.Recipe
	db.Where("organization_id = ? AND dish_type_id = ? AND is_active = ?", r.OrganizationID, d.DishTypeID, true).
		Order("id").Find(&recipes)
	pool := recipes[:0:0]
	for _, rec := range recipes {
		if !exclude[rec.ID] {
			pool = append(pool, rec)
		}
	}
	if len(pool) == 0 {
		return nil
	}
	if len(pool) == 1 {
		return &pool[0]
	}
	lookback := len(pool)
	if lookback < 7 {
		lookback = 7
	}
	var usedIDs []uint
	db.Model(&models.TaskDish{}).
		Joins("JOIN schedule_tasks st ON st.id = task_dishes.task_id").
		Joins("JOIN daily_schedules ds ON ds.id = st.schedule_id").
		Where("st.routine_id = ? AND task_dishes.position = ? AND task_dishes.recipe_id IS NOT NULL AND ds.date >= ? AND ds.date < ?",
			r.ID, d.Position, date.AddDate(0, 0, -lookback), date).
		Order("ds.date DESC").
		Pluck("task_dishes.recipe_id", &usedIDs)
	// recency: 0 — было последним, больше — давнее.
	recency := map[uint]int{}
	for i, id := range usedIDs {
		if _, ok := recency[id]; !ok {
			recency[id] = i
		}
	}
	fresh := pool[:0:0]
	for _, rec := range pool {
		if _, ok := recency[rec.ID]; !ok {
			fresh = append(fresh, rec)
		}
	}
	if len(fresh) == 0 {
		// Все были недавно — по кругу: то, что готовили давнее всех.
		oldest := pool[0]
		for _, rec := range pool[1:] {
			if recency[rec.ID] > recency[oldest.ID] {
				oldest = rec
			}
		}
		return &oldest
	}
	chosen := fresh[rand.Intn(len(fresh))]
	return &chosen
}

// ClearFuture убирает будущие невыполненные задачи регулярной задачи (с сегодняшнего дня дома),
// кроме тех, что поправили на день (порции, замена блюда). Нужно после правки, паузы и удаления.
func ClearFuture(db *gorm.DB, orgID, routineID uint) error {
	today := clock.OrgToday(db, orgID)
	return clearTasks(db, routineID, "ds.date >= ?", today)
}

// ClearDay убирает невыполненную задачу регулярной задачи в один день (после решения на день).
func ClearDay(db *gorm.DB, routineID uint, date time.Time) error {
	return clearTasks(db, routineID, "ds.date = ?", day(date))
}

func clearTasks(db *gorm.DB, routineID uint, cond string, arg any) error {
	var ids []uint
	db.Model(&models.ScheduleTask{}).
		Joins("JOIN daily_schedules ds ON ds.id = schedule_tasks.schedule_id").
		Where("schedule_tasks.routine_id = ? AND schedule_tasks.completed = false AND "+cond, routineID, arg).
		Where("NOT EXISTS (SELECT 1 FROM meal_swaps ms WHERE ms.task_id = schedule_tasks.id)").
		Pluck("schedule_tasks.id", &ids)
	if len(ids) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, q := range []string{
			"DELETE FROM meal_recipes WHERE schedule_task_id IN ?",
			"DELETE FROM task_dishes WHERE task_id IN ?",
			"DELETE FROM schedule_tasks WHERE id IN ?",
		} {
			if err := tx.Exec(q, ids).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureDays собирает регулярные задачи дома на ближайшие дни (создавая дни при необходимости).
func EnsureDays(db *gorm.DB, orgID uint, days int) {
	if !HasAny(db, orgID) {
		return
	}
	today := clock.OrgToday(db, orgID)
	for i := 0; i < days; i++ {
		d := today.AddDate(0, 0, i)
		s, err := models.EnsureDailySchedule(db, orgID, d, false)
		if err != nil {
			log.Printf("routines: day %s for org %d: %v", d.Format("2006-01-02"), orgID, err)
			continue
		}
		MaterializeDay(db, orgID, &s, d)
	}
}

// StandardFor — обычное число порций приёма пищи регулярной задачи.
func StandardFor(db *gorm.DB, routineIDs []uint) map[uint]int {
	out := map[uint]int{}
	if len(routineIDs) == 0 {
		return out
	}
	var dishes []models.RoutineDish
	db.Where("routine_id IN ?", routineIDs).Find(&dishes)
	by := map[uint][]models.RoutineDish{}
	for _, d := range dishes {
		by[d.RoutineID] = append(by[d.RoutineID], d)
	}
	for id, ds := range by {
		out[id] = StandardServings(ds)
	}
	return out
}

// SortedWeekdays: {3,1} → «1,3».
func SortedWeekdays(days []int) string {
	set := map[int]bool{}
	for _, d := range days {
		if d >= 1 && d <= 7 {
			set[d] = true
		}
	}
	keys := make([]int, 0, len(set))
	for d := range set {
		keys = append(keys, d)
	}
	sort.Ints(keys)
	parts := make([]string, len(keys))
	for i, d := range keys {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ",")
}

// Reassign пересчитывает исполнителя невыполненных регулярных задач дома в днях [from, to]:
// помощнику дали отгул — задача у того, кто поставил; отгул сняли — снова у помощника.
// Решения на день (передать, перенести, не делать) учитываются. Порции и замены блюд не трогаем.
func Reassign(db *gorm.DB, orgID uint, from, to time.Time) {
	from, to = day(from), day(to)
	if today := clock.OrgToday(db, orgID); from.Before(today) {
		from = today
	}
	if to.Before(from) {
		return
	}
	type row struct {
		ID        uint
		RoutineID uint
		Date      time.Time
		Assigned  *uint
	}
	var rows []row
	db.Model(&models.ScheduleTask{}).
		Select("schedule_tasks.id, schedule_tasks.routine_id, ds.date, schedule_tasks.assigned_to_user_id AS assigned").
		Joins("JOIN daily_schedules ds ON ds.id = schedule_tasks.schedule_id").
		Where("ds.organization_id = ? AND schedule_tasks.routine_id IS NOT NULL AND schedule_tasks.completed = false AND ds.date >= ? AND ds.date <= ?",
			orgID, from, to).
		Scan(&rows)
	cache := map[uint]*models.Routine{}
	for _, t := range rows {
		r, ok := cache[t.RoutineID]
		if !ok {
			var x models.Routine
			if db.First(&x, t.RoutineID).Error != nil {
				x.ID = 0
			}
			r = &x
			cache[t.RoutineID] = r
		}
		if r.ID == 0 {
			continue
		}
		who, ok := assignee(db, *r, day(t.Date))
		if !ok || (t.Assigned != nil && *t.Assigned == who) {
			continue
		}
		db.Model(&models.ScheduleTask{}).Where("id = ?", t.ID).Update("assigned_to_user_id", who)
	}
}
