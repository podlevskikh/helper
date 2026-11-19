// Package clock — «который час» и «какое сегодня число» в одном месте.
//
// Даты дней (daily_schedules.date и т.п.) храним как полночь UTC календарной даты.
// «Сегодня» дома считается в его часовом поясе (organizations.timezone, по умолчанию Europe/Nicosia),
// а не по часам сервера: сервер на проде в UTC, и с 00:00 до 02:00–03:00 по Никосии он ещё во вчерашнем дне.
package clock

import (
	"time"

	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/models"
)

// DefaultTZ — пояс дома, если он не задан.
const DefaultTZ = "Europe/Nicosia"

// Now — текущее время; в тестах подменяется.
var Now = func() time.Time { return time.Now() }

// Day — канонический день: полночь UTC той же календарной даты, что у t в его поясе.
func Day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// OrgLocation — часовой пояс дома.
func OrgLocation(db *gorm.DB, orgID uint) *time.Location {
	var org models.Organization
	name := DefaultTZ
	if db != nil && db.Select("id", "timezone").First(&org, orgID).Error == nil && org.Timezone != "" {
		name = org.Timezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc, _ = time.LoadLocation(DefaultTZ)
	}
	return loc
}

// OrgNow — текущее время в поясе дома.
func OrgNow(db *gorm.DB, orgID uint) time.Time { return Now().In(OrgLocation(db, orgID)) }

// OrgToday — сегодняшняя дата дома (полночь UTC этой даты).
func OrgToday(db *gorm.DB, orgID uint) time.Time { return Day(OrgNow(db, orgID)) }
