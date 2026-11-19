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
//  2. Дубли дня сливаются в самую раннюю строку: разовые и повторяющиеся задачи переносятся,
//     собранные планировщиком (еда, уборка, ребёнок) — удаляются, если в основной строке уже есть свои.
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
		var keepGenerated int64
		tx.Raw(`SELECT count(*) FROM schedule_tasks WHERE schedule_id = ? AND task_type IN ('meal','cleaning','childcare')`, d.Keep).Scan(&keepGenerated)
		if keepGenerated > 0 {
			gen := `SELECT id FROM schedule_tasks WHERE schedule_id = ? AND task_type IN ('meal','cleaning','childcare')`
			for _, q := range []string{
				"DELETE FROM meal_recipes WHERE schedule_task_id IN (" + gen + ")",
				"DELETE FROM task_zones WHERE schedule_task_id IN (" + gen + ")",
				"DELETE FROM schedule_tasks WHERE id IN (" + gen + ")",
			} {
				if err := tx.Exec(q, d.Drop).Error; err != nil {
					return err
				}
			}
		}
		// Повторяющаяся задача уже есть в основном дне — копию из дубля убираем.
		if err := tx.Exec(`DELETE FROM schedule_tasks t WHERE t.schedule_id = ? AND t.recurring_task_id IS NOT NULL
			AND EXISTS (SELECT 1 FROM schedule_tasks k WHERE k.schedule_id = ? AND k.recurring_task_id = t.recurring_task_id)`,
			d.Drop, d.Keep).Error; err != nil {
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
