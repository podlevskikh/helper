package models

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureDailySchedule возвращает день дома, создавая его при отсутствии.
// Уникальный индекс (organization_id, date) + ON CONFLICT DO NOTHING: параллельные запросы
// и планировщик не создают дублей дня. day — полночь UTC (clock.Day).
func EnsureDailySchedule(db *gorm.DB, orgID uint, day time.Time, generated bool) (DailySchedule, error) {
	var s DailySchedule
	if err := db.Where("organization_id = ? AND date = ?", orgID, day).First(&s).Error; err == nil {
		return s, nil
	}
	s = DailySchedule{OrganizationID: orgID, Date: day, Generated: generated}
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&s)
	if res.Error != nil {
		return s, res.Error
	}
	if res.RowsAffected == 0 {
		err := db.Where("organization_id = ? AND date = ?", orgID, day).First(&s).Error
		return s, err
	}
	return s, nil
}
