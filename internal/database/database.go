package database

import (
	"fmt"
	"log"
	"os"

	"podlevskikh/awesomeProject/internal/data"
	"podlevskikh/awesomeProject/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// Initialize sets up the database connection and runs migrations
// It accepts a DATABASE_URL connection string for PostgreSQL
func Initialize(databaseURL string) error {
	var err error

	// If databaseURL is empty, try to get it from environment
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}

	// If still empty, return error
	if databaseURL == "" {
		log.Println("ERROR: DATABASE_URL is not set")
		log.Println("Please set DATABASE_URL environment variable with PostgreSQL connection string")
		log.Println("Example: postgres://user:password@host:port/database?sslmode=disable")
		return fmt.Errorf("DATABASE_URL is required")
	}

	log.Println("Attempting to connect to PostgreSQL database...")

	DB, err = gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		log.Printf("Failed to connect to database. Error: %v", err)
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	log.Println("✅ Database connection established successfully")

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
