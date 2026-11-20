package database

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
)

// Версионированные миграции данных. Схема (новые таблицы и колонки) — через AutoMigrate,
// она только добавляет. Всё, что меняет или удаляет данные, — здесь: каждый шаг выполняется
// один раз, в транзакции, и записывается в schema_migrations. Ошибка шага останавливает старт
// сервера (Initialize вернёт ошибку → log.Fatal), healthcheck не пройдёт, и Railway оставит
// работать предыдущую версию.
//
// Правило expand/contract: колонку или таблицу не удаляем и не переименовываем в том же релизе,
// где перестаём ими пользоваться, — только следующим релизом, когда старый код уже не работает.

type dataMigration struct {
	id  string
	run func(tx *gorm.DB) error
	// legacy — шаг выполнялся до появления schema_migrations; на существующей базе он уже применён.
	legacy bool
}

// migrationsState — то, что нужно шагам из состояния до AutoMigrate.
type migrationsState struct {
	categoryPriorityAppeared bool
}

func dataMigrations(st migrationsState) []dataMigration {
	return []dataMigration{
		{id: "2026-06-recipes-is-active-default", legacy: true, run: func(tx *gorm.DB) error {
			return tx.Exec("UPDATE recipes SET is_active = true WHERE is_active IS NULL").Error
		}},
		{id: "2026-06-settings-drop-old-unique", legacy: true, run: func(tx *gorm.DB) error {
			return tx.Exec("DROP INDEX IF EXISTS uni_settings_key").Error
		}},
		{id: "2026-11-category-childcare-priority", legacy: true, run: func(tx *gorm.DB) error {
			if !st.categoryPriorityAppeared {
				return nil
			}
			return tx.Exec("UPDATE task_categories SET priority = 'high' WHERE is_default = true AND name = ?", "Уход за ребёнком").Error
		}},
		{id: "2026-11-daily-schedules-unique-day", run: dedupeDailySchedules},
		// Дома, зарегистрированные без приёмов пищи, получают завтрак, обед и ужин (иначе меню пустое).
		{id: "2026-11-default-meal-times", run: func(tx *gorm.DB) error {
			var orgIDs []uint
			tx.Raw(`SELECT o.id FROM organizations o WHERE NOT EXISTS
				(SELECT 1 FROM meal_times m WHERE m.organization_id = o.id)`).Scan(&orgIDs)
			for _, id := range orgIDs {
				if err := EnsureDefaultMealTimes(tx, id); err != nil {
					return err
				}
			}
			return nil
		}},
		// «Ритм дома»: приёмы пищи, зоны, повторяющиеся задачи и распорядок ребёнка → регулярные задачи.
		{id: "2026-11-routines-from-legacy", run: migrateRoutinesFromLegacy},
		// Гонка сборки дня (до блокировки LockDay) ставила задачу регулярной задачи несколько раз.
		{id: "2026-11-dedupe-routine-tasks", run: dedupeRoutineTasks},
	}
}

// runMigrations выполняет шаги, которых ещё нет в schema_migrations.
func runMigrations(db *gorm.DB, st migrationsState) error {
	fresh := !db.Migrator().HasTable("schema_migrations")
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		id TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`).Error; err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	// База уже жила до schema_migrations (есть дома) — старые шаги на ней выполнены: только отмечаем.
	var orgs int64
	db.Raw("SELECT count(*) FROM organizations").Scan(&orgs)
	baseline := fresh && orgs > 0

	for _, m := range dataMigrations(st) {
		var n int64
		db.Raw("SELECT count(*) FROM schema_migrations WHERE id = ?", m.id).Scan(&n)
		if n > 0 {
			continue
		}
		if baseline && m.legacy {
			if err := db.Exec("INSERT INTO schema_migrations (id) VALUES (?) ON CONFLICT DO NOTHING", m.id).Error; err != nil {
				return err
			}
			continue
		}
		start := time.Now()
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := m.run(tx); err != nil {
				return err
			}
			return tx.Exec("INSERT INTO schema_migrations (id) VALUES (?)", m.id).Error
		})
		if err != nil {
			return fmt.Errorf("миграция %s: %w", m.id, err)
		}
		log.Printf("migration %s: ok (%s)", m.id, time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// dedupeDailySchedules: один день дома — одна строка.
//  1. Дата приводится к полуночи UTC (старые строки, созданные сервером не в UTC, лежали в 21:00/22:00 UTC накануне).
//  2. Дубли дня сливаются в самую раннюю строку (mergeDuplicateDay): разовые задачи переносятся,
//     собранные планировщиком и повторяющиеся — сливаются по типу и названию без потери отметок
//     «сделано» и выбранных блюд.
//  3. Уникальный индекс (organization_id, date).
func dedupeDailySchedules(tx *gorm.DB) error {
	if err := tx.Exec(`UPDATE daily_schedules
		SET date = (date_trunc('day', (date AT TIME ZONE 'UTC') + interval '12 hours')) AT TIME ZONE 'UTC'
		WHERE (date AT TIME ZONE 'UTC')::time <> '00:00'`).Error; err != nil {
		return err
	}
	type dup struct {
		Keep uint
		Drop uint
	}
	var dups []dup
	if err := tx.Raw(`SELECT k.keep, d.id AS drop FROM daily_schedules d
		JOIN (SELECT organization_id, date, min(id) AS keep FROM daily_schedules
		      GROUP BY organization_id, date HAVING count(*) > 1) k
		  ON k.organization_id = d.organization_id AND k.date = d.date AND d.id <> k.keep`).Scan(&dups).Error; err != nil {
		return err
	}
	for _, d := range dups {
		if err := mergeDuplicateDay(tx, d.Keep, d.Drop); err != nil {
			return err
		}
		if err := tx.Exec("UPDATE schedule_tasks SET schedule_id = ? WHERE schedule_id = ?", d.Keep, d.Drop).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE daily_schedules SET generated = generated OR (SELECT generated FROM daily_schedules WHERE id = ?) WHERE id = ?`, d.Drop, d.Keep).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM daily_schedules WHERE id = ?", d.Drop).Error; err != nil {
			return err
		}
	}
	if len(dups) > 0 {
		log.Printf("daily_schedules: слито дублей дней: %d", len(dups))
	}
	return tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_schedules_org_date ON daily_schedules (organization_id, date)").Error
}

// mergeDuplicateDay переносит задачи дня-дубля drop в основной день keep.
//   - Собранные планировщиком (еда, уборка, ребёнок): если в keep задач этого типа нет — переносятся
//     целиком. Если есть — копия из drop с тем же названием отдаёт свою отметку «сделано» и выбранные
//     блюда задаче в keep и удаляется; несовпавшие удаляются, кроме отмеченных выполненными (переносятся).
//   - Повторяющиеся: копия той же повторяющейся задачи сливается так же (отметка «сделано» сохраняется).
//   - Остальные (разовые) переносятся как есть.
func mergeDuplicateDay(tx *gorm.DB, keep, drop uint) error {
	exec := func(q string, args ...any) error { return tx.Exec(q, args...).Error }
	for _, typ := range []string{"meal", "cleaning", "childcare"} {
		var inKeep int64
		tx.Raw(`SELECT count(*) FROM schedule_tasks WHERE schedule_id = ? AND task_type = ?`, keep, typ).Scan(&inKeep)
		if inKeep == 0 {
			continue // переедут общим UPDATE ниже
		}
		steps := []struct {
			q    string
			args []any
		}{
			{`UPDATE schedule_tasks k SET completed = true, completed_at = COALESCE(k.completed_at, d.completed_at)
				FROM schedule_tasks d
				WHERE k.schedule_id = ? AND d.schedule_id = ? AND k.task_type = ? AND d.task_type = k.task_type
				  AND k.title = d.title AND d.completed AND NOT k.completed`, []any{keep, drop, typ}},
			{`UPDATE meal_recipes mr SET schedule_task_id = k.id
				FROM schedule_tasks d, schedule_tasks k
				WHERE mr.schedule_task_id = d.id AND d.schedule_id = ? AND k.schedule_id = ?
				  AND d.task_type = ? AND k.task_type = d.task_type AND k.title = d.title
				  AND NOT EXISTS (SELECT 1 FROM meal_recipes x WHERE x.schedule_task_id = k.id)`, []any{drop, keep, typ}},
		}
		// Удаляем копии: совпавшие по названию — всегда (их отметки уже в keep), несовпавшие — если не выполнены.
		dropIDs := `SELECT d.id FROM schedule_tasks d WHERE d.schedule_id = ? AND d.task_type = ?
			AND (NOT d.completed OR EXISTS (SELECT 1 FROM schedule_tasks k WHERE k.schedule_id = ? AND k.task_type = d.task_type AND k.title = d.title))`
		for _, q := range []string{
			"DELETE FROM meal_recipes WHERE schedule_task_id IN (" + dropIDs + ")",
			"DELETE FROM task_zones WHERE schedule_task_id IN (" + dropIDs + ")",
			"DELETE FROM schedule_tasks WHERE id IN (" + dropIDs + ")",
		} {
			steps = append(steps, struct {
				q    string
				args []any
			}{q, []any{drop, typ, keep}})
		}
		for _, st := range steps {
			if err := exec(st.q, st.args...); err != nil {
				return err
			}
		}
	}
	// Повторяющаяся задача уже есть в основном дне: переносим отметку «сделано» и убираем копию.
	if err := exec(`UPDATE schedule_tasks k SET completed = true, completed_at = COALESCE(k.completed_at, d.completed_at)
		FROM schedule_tasks d
		WHERE k.schedule_id = ? AND d.schedule_id = ? AND k.recurring_task_id IS NOT NULL
		  AND d.recurring_task_id = k.recurring_task_id AND d.completed AND NOT k.completed`, keep, drop); err != nil {
		return err
	}
	return exec(`DELETE FROM schedule_tasks t WHERE t.schedule_id = ? AND t.recurring_task_id IS NOT NULL
		AND EXISTS (SELECT 1 FROM schedule_tasks k WHERE k.schedule_id = ? AND k.recurring_task_id = t.recurring_task_id)`,
		drop, keep)
}

// dedupeRoutineTasks убирает копии задачи регулярной задачи в одном дне (тот же день, задача и время).
// Остаётся «самая живая» копия: выполненная → с заменой блюда → изменённая вручную → самая ранняя.
func dedupeRoutineTasks(tx *gorm.DB) error {
	var ids []uint
	if err := tx.Raw(`SELECT id FROM (
		SELECT st.id, row_number() OVER (
			PARTITION BY st.schedule_id, st.routine_id, COALESCE(st.time, '')
			ORDER BY st.completed DESC,
				EXISTS (SELECT 1 FROM meal_swaps ms WHERE ms.task_id = st.id) DESC,
				(st.updated_at > st.created_at + interval '2 seconds') DESC,
				st.id ASC) AS rn
		FROM schedule_tasks st WHERE st.routine_id IS NOT NULL) r
		WHERE rn > 1`).Scan(&ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	for _, q := range []string{
		"DELETE FROM meal_recipes WHERE schedule_task_id IN ?",
		"DELETE FROM task_zones WHERE schedule_task_id IN ?",
		"DELETE FROM task_dishes WHERE task_id IN ?",
		"DELETE FROM meal_swaps WHERE task_id IN ?",
		"DELETE FROM schedule_tasks WHERE id IN ?",
	} {
		if err := tx.Exec(q, ids).Error; err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	log.Printf("schedule_tasks: убрано копий задач регулярных задач: %d", len(ids))
	return nil
}
