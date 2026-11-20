package database

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/routines"
)

// Перенос в «Ритм дома» (helper/docs/work/routines/PRD.md §7): приёмы пищи, зоны уборки,
// повторяющиеся задачи и распорядок ребёнка становятся регулярными задачами. Старые записи
// помечаются migrated_routine_id — старые генераторы их больше не ставят. Уже собранные дни
// не пересобираются: их задачи привязываются к новым регулярным задачам (выбранные блюда и
// отметки «сделано» остаются).

// Порции у перенесённых приёмов пищи (решение владелицы).
const migratedMealServings = 2

var mealTypeNames = map[string]string{
	"breakfast": "Завтрак", "lunch": "Обед", "dinner": "Ужин",
	"snack": "Перекус", "snack1": "Перекус", "snack2": "Полдник", "babyfood": "Детское",
}

func mealDisplayName(name string) string {
	if v, ok := mealTypeNames[strings.ToLower(strings.TrimSpace(name))]; ok {
		return v
	}
	return strings.TrimSpace(name)
}

func migrateRoutinesFromLegacy(tx *gorm.DB) error {
	var orgs []models.Organization
	if err := tx.Order("id").Find(&orgs).Error; err != nil {
		return err
	}
	for _, org := range orgs {
		if err := migrateOrgRoutines(tx, org); err != nil {
			return fmt.Errorf("дом %d: %w", org.ID, err)
		}
	}
	return nil
}

// orgPeople — основной админ (поставил задачи) и помощник по умолчанию: тот, кто дольше всех в доме
// (раньше еда и уборка были «для всех помощников» — теперь у задачи один исполнитель, его можно сменить).
// Помощников нет — основной админ.
func orgPeople(tx *gorm.DB, org models.Organization) (owner, helper uint) {
	owner = org.OwnerUserID
	if owner == 0 {
		var m models.Membership
		if tx.Where("organization_id = ? AND role = ?", org.ID, models.RoleOwner).Order("id").First(&m).Error == nil {
			owner = m.UserID
		}
	}
	var helpers []uint
	tx.Model(&models.Membership{}).Where("organization_id = ? AND role = ? AND status = ?",
		org.ID, models.RoleHelper, models.MembershipActive).Order("id").Pluck("user_id", &helpers)
	helper = owner
	if len(helpers) > 0 {
		helper = helpers[0]
	}
	return owner, helper
}

func jsonTimes(ts ...string) string {
	out := []string{}
	for _, t := range ts {
		if t != "" {
			out = append(out, t)
		}
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

func migrateOrgRoutines(tx *gorm.DB, org models.Organization) error {
	owner, helper := orgPeople(tx, org)
	if owner == 0 {
		return nil // дом без людей — переносить некому
	}
	today := clock.Day(clock.Now())
	workdays := calendar.OrgWorkdays(tx, org.ID)
	weekdays := func(days []int) string { return routines.SortedWeekdays(days) }

	// 1. Типы блюд — из приёмов пищи дома (у нашего: Завтрак, Обед, Ужин).
	var meals []models.MealTime
	tx.Where("organization_id = ? AND migrated_routine_id IS NULL", org.ID).Order("default_time, id").Find(&meals)
	typeByName := map[string]uint{}
	var existing []models.DishType
	tx.Where("organization_id = ?", org.ID).Find(&existing)
	for _, t := range existing {
		typeByName[t.Name] = t.ID
	}
	mealType := map[uint]uint{} // meal_time_id → dish_type_id
	for i, m := range meals {
		name := mealDisplayName(m.Name)
		id, ok := typeByName[name]
		if !ok {
			t := models.DishType{OrganizationID: org.ID, Name: name, Position: i + 1}
			if err := tx.Create(&t).Error; err != nil {
				return err
			}
			id = t.ID
			typeByName[name] = id
		}
		mealType[m.ID] = id
	}
	// Тип у рецепта — по приёму пищи, к которому он привязан (несколько — первый по времени).
	for _, m := range meals {
		if err := tx.Exec(`UPDATE recipes SET dish_type_id = ? WHERE organization_id = ? AND dish_type_id IS NULL
			AND id IN (SELECT recipe_id FROM recipe_meal_times WHERE meal_time_id = ?)`,
			mealType[m.ID], org.ID, m.ID).Error; err != nil {
			return err
		}
	}

	newRoutine := func(r *models.Routine) error {
		r.OrganizationID = org.ID
		r.CreatedBy = owner
		if r.AssignedToUserID == 0 {
			r.AssignedToUserID = helper
		}
		if r.StartDate.IsZero() {
			r.StartDate = today
		}
		r.AnchorDate = r.StartDate
		if r.EveryN < 1 {
			r.EveryN = 1
		}
		return tx.Create(r).Error
	}

	// 2. Приёмы пищи → «Еда»: одно блюдо типа приёма пищи, 2 порции, все рабочие дни дома.
	for _, m := range meals {
		times := []string{m.DefaultTime}
		if m.DefaultTimes != "" {
			var ts []string
			if json.Unmarshal([]byte(m.DefaultTimes), &ts) == nil && len(ts) > 0 {
				times = ts
			}
		}
		r := models.Routine{Kind: models.RoutineFood, Title: mealDisplayName(m.Name), Active: m.Active,
			RepeatKind: models.RepeatWeekdays, Weekdays: weekdays(workdays),
			TimeMode: models.TimeExact, Times: jsonTimes(times...)}
		if err := newRoutine(&r); err != nil {
			return err
		}
		if err := tx.Create(&models.RoutineDish{RoutineID: r.ID, Position: 1, DishTypeID: mealType[m.ID],
			Servings: migratedMealServings}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.MealTime{}).Where("id = ?", m.ID).Update("migrated_routine_id", r.ID).Error; err != nil {
			return err
		}
		// Уже собранные дни: задачи этого приёма пищи — к регулярной задаче, блюдо — блюдом №1.
		title := fmt.Sprintf("%s - %s", m.Name, m.FamilyMember)
		var tasks []models.ScheduleTask
		tx.Preload("Recipes").Joins("JOIN daily_schedules ds ON ds.id = schedule_tasks.schedule_id").
			Where("ds.organization_id = ? AND ds.date >= ? AND schedule_tasks.task_type = 'meal' AND schedule_tasks.routine_id IS NULL AND schedule_tasks.title = ?",
				org.ID, today, title).Find(&tasks)
		for _, t := range tasks {
			if err := tx.Model(&models.ScheduleTask{}).Where("id = ?", t.ID).
				Updates(map[string]any{"routine_id": r.ID, "title": r.Title}).Error; err != nil {
				return err
			}
			rid := t.RecipeID
			if rid == nil && len(t.Recipes) > 0 {
				rid = &t.Recipes[0].ID
			}
			dt := mealType[m.ID]
			if err := tx.Create(&models.TaskDish{TaskID: t.ID, Position: 1, RecipeID: rid, DishTypeID: &dt}).Error; err != nil {
				return err
			}
		}
	}

	// 3. Зоны уборки → «Уборка»: дни недели — те, что выпадали по частоте; лимит «3 зоны в день» снят.
	var zones []models.CleaningZone
	tx.Where("organization_id = ? AND migrated_routine_id IS NULL", org.ID).Order("id").Find(&zones)
	for _, z := range zones {
		days := calendar.CleaningDays(z.FrequencyPerWeek, z.ID, workdays)
		if len(days) == 0 {
			days = workdays
		}
		prio := ""
		if z.Priority == "high" {
			prio = "high"
		}
		r := models.Routine{Kind: models.RoutineClean, Title: z.Name, Description: z.Description, Active: true,
			Priority: prio, RepeatKind: models.RepeatWeekdays, Weekdays: weekdays(days), TimeMode: models.TimeAny, Times: "[]"}
		if err := newRoutine(&r); err != nil {
			return err
		}
		if err := tx.Model(&models.CleaningZone{}).Where("id = ?", z.ID).Update("migrated_routine_id", r.ID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE schedule_tasks SET routine_id = ? WHERE routine_id IS NULL AND task_type = 'cleaning'
			AND zone_id = ? AND schedule_id IN (SELECT id FROM daily_schedules WHERE organization_id = ? AND date >= ?)`,
			r.ID, z.ID, org.ID, today).Error; err != nil {
			return err
		}
	}

	// 4. Повторяющиеся задачи → «Другое» (или «Ребёнок», если категория ребёнка), один в один.
	var recs []models.RecurringTask
	tx.Preload("TaskCategory").Where("organization_id = ? AND active = ? AND migrated_routine_id IS NULL", org.ID, true).
		Order("id").Find(&recs)
	for _, rt := range recs {
		kind := models.RoutineOther
		if rt.TaskCategory != nil && rt.TaskCategory.SystemKey == "childcare" {
			kind = models.RoutineKid
		}
		mode, times := models.TimeAny, "[]"
		switch {
		case rt.Time != "" && rt.EndTime != "":
			mode, times = models.TimeSpan, jsonTimes(rt.Time, rt.EndTime)
		case rt.Time != "":
			mode, times = models.TimeExact, jsonTimes(rt.Time)
		}
		var assignee uint
		if rt.AssignedToUserID != nil {
			assignee = *rt.AssignedToUserID
		}
		prio := ""
		if rt.Priority == "high" {
			prio = "high"
		}
		r := models.Routine{Kind: kind, Title: rt.Title, Description: rt.Description, Active: true, Priority: prio,
			TaskCategoryID: rt.TaskCategoryID, RepeatKind: models.RepeatWeekdays, Weekdays: rt.Weekdays,
			StartDate: clock.Day(rt.StartDate), TimeMode: mode, Times: times, AssignedToUserID: assignee}
		if err := newRoutine(&r); err != nil {
			return err
		}
		if err := tx.Model(&models.RecurringTask{}).Where("id = ?", rt.ID).Update("migrated_routine_id", r.ID).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE schedule_tasks SET routine_id = ? WHERE recurring_task_id = ? AND routine_id IS NULL", r.ID, rt.ID).Error; err != nil {
			return err
		}
		// Удалённые вручную дни — пропуски регулярной задачи.
		if err := tx.Exec(`INSERT INTO routine_skips (routine_id, date)
			SELECT ?, date::date FROM recurring_task_skips WHERE recurring_task_id = ? ON CONFLICT DO NOTHING`, r.ID, rt.ID).Error; err != nil {
			return err
		}
	}

	// 5. Распорядок ребёнка (записи на даты) → «Ребёнок» по дням недели: записи за 4 недели и будущие
	// с одинаковыми «с», «по» и текстом — одна регулярная задача в те дни недели, где они встречались.
	var cc []models.ChildcareSchedule
	tx.Where("organization_id = ? AND migrated_routine_id IS NULL AND date >= ?", org.ID, today.AddDate(0, 0, -28)).
		Order("date").Find(&cc)
	type key struct{ from, to, notes string }
	groups := map[key][]models.ChildcareSchedule{}
	var order []key
	for _, e := range cc {
		k := key{e.StartTime, e.EndTime, strings.TrimSpace(e.Notes)}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], e)
	}
	for _, k := range order {
		set := map[int]bool{}
		var ids []uint
		for _, e := range groups[k] {
			set[calendar.ISOWeekday(e.Date)] = true
			ids = append(ids, e.ID)
		}
		days := make([]int, 0, len(set))
		for d := range set {
			days = append(days, d)
		}
		sort.Ints(days)
		title := k.notes
		if title == "" {
			title = "Ребёнок"
		}
		r := models.Routine{Kind: models.RoutineKid, Title: title, Active: true, RepeatKind: models.RepeatWeekdays,
			Weekdays: weekdays(days), TimeMode: models.TimeSpan, Times: jsonTimes(k.from, k.to)}
		if err := newRoutine(&r); err != nil {
			return err
		}
		if err := tx.Model(&models.ChildcareSchedule{}).Where("id IN ?", ids).Update("migrated_routine_id", r.ID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE schedule_tasks SET routine_id = ? WHERE routine_id IS NULL AND task_type = 'childcare'
			AND time = ? AND end_time = ? AND schedule_id IN (SELECT id FROM daily_schedules WHERE organization_id = ? AND date >= ?)`,
			r.ID, k.from, k.to, org.ID, today).Error; err != nil {
			return err
		}
	}
	// Уже собранные дни: у задачи теперь один исполнитель (раньше еда и уборка были «для всех»).
	routines.Reassign(tx, org.ID, today, today.AddDate(0, 0, 60))
	return nil
}
