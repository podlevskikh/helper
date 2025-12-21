package data

import (
	"time"
	"gorm.io/gorm"
	"podlevskikh/awesomeProject/internal/models"
)

// InitializeCyprusHolidays adds Cyprus national holidays to the database
func InitializeCyprusHolidays(db *gorm.DB) error {
	holidays := []models.Holiday{
		{Name: "New Year's Day", Date: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Epiphany", Date: time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Green Monday", Date: time.Date(2025, 3, 3, 0, 0, 0, 0, time.UTC), IsRecurring: false, Country: "Cyprus"}, // Changes yearly
		{Name: "Greek Independence Day", Date: time.Date(2025, 3, 25, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Cyprus National Day", Date: time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Good Friday", Date: time.Date(2025, 4, 18, 0, 0, 0, 0, time.UTC), IsRecurring: false, Country: "Cyprus"}, // Changes yearly
		{Name: "Easter Saturday", Date: time.Date(2025, 4, 19, 0, 0, 0, 0, time.UTC), IsRecurring: false, Country: "Cyprus"}, // Changes yearly
		{Name: "Easter Sunday", Date: time.Date(2025, 4, 20, 0, 0, 0, 0, time.UTC), IsRecurring: false, Country: "Cyprus"}, // Changes yearly
		{Name: "Easter Monday", Date: time.Date(2025, 4, 21, 0, 0, 0, 0, time.UTC), IsRecurring: false, Country: "Cyprus"}, // Changes yearly
		{Name: "Labour Day", Date: time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Orthodox Pentecost Monday", Date: time.Date(2025, 6, 9, 0, 0, 0, 0, time.UTC), IsRecurring: false, Country: "Cyprus"}, // Changes yearly
		{Name: "Assumption of Mary", Date: time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Cyprus Independence Day", Date: time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Greek National Day (Ochi Day)", Date: time.Date(2025, 10, 28, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Christmas Day", Date: time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
		{Name: "Boxing Day", Date: time.Date(2025, 12, 26, 0, 0, 0, 0, time.UTC), IsRecurring: true, Country: "Cyprus"},
	}

	for _, holiday := range holidays {
		// Check if holiday already exists
		var existing models.Holiday
		result := db.Where("name = ? AND date = ?", holiday.Name, holiday.Date).First(&existing)
		
		if result.Error == gorm.ErrRecordNotFound {
			// Holiday doesn't exist, create it
			if err := db.Create(&holiday).Error; err != nil {
				return err
			}
		}
	}

	return nil
}

// IsHoliday checks if a given date is a holiday or Sunday
func IsHoliday(db *gorm.DB, date time.Time) bool {
	// Check if it's Sunday
	if date.Weekday() == time.Sunday {
		return true
	}

	// Check if it's a public holiday
	var count int64
	// PostgreSQL uses EXTRACT for date extraction
	db.Model(&models.Holiday{}).Where(
		"(is_recurring = ? AND EXTRACT(MONTH FROM date) = ? AND EXTRACT(DAY FROM date) = ?) OR date = ?",
		true, int(date.Month()), date.Day(), date.Format("2006-01-02"),
	).Count(&count)

	return count > 0
}

