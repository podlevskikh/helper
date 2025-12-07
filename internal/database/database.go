package database

import (
	"log"

	"podlevskikh/awesomeProject/internal/data"
	"podlevskikh/awesomeProject/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// Initialize sets up the database connection and runs migrations
func Initialize(dbPath string) error {
	var err error

	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		return err
	}

	log.Println("Database connection established")

	// Run migrations
	err = DB.AutoMigrate(
		&models.Recipe{},
		&models.MealTime{},
		&models.CleaningZone{},
		&models.ChildcareSchedule{},
		&models.DailySchedule{},
		&models.ScheduleTask{},
		&models.ShoppingListItem{},
		&models.Settings{},
		&models.Holiday{},
		&models.RecipeComment{},
	)

	if err != nil {
		return err
	}

	log.Println("Database migrations completed")

	// Initialize default settings
	initializeDefaultSettings()

	// Initialize Cyprus holidays
	if err := data.InitializeCyprusHolidays(DB); err != nil {
		log.Printf("Warning: Failed to initialize holidays: %v", err)
	} else {
		log.Println("Cyprus holidays initialized")
	}

	return nil
}

// initializeDefaultSettings creates default settings if they don't exist
func initializeDefaultSettings() {
	defaultSettings := []models.Settings{
		{Key: "schedule_days_ahead", Value: "7", Description: "Number of days to generate schedule ahead"},
		{Key: "auto_generate_schedule", Value: "true", Description: "Automatically generate schedule daily"},
	}

	for _, setting := range defaultSettings {
		var existing models.Settings
		result := DB.Where("key = ?", setting.Key).First(&existing)
		if result.Error == gorm.ErrRecordNotFound {
			DB.Create(&setting)
			log.Printf("Created default setting: %s = %s", setting.Key, setting.Value)
		}
	}
}

// GetDB returns the database instance
func GetDB() *gorm.DB {
	return DB
}
