package scheduler

import (
	"fmt"
	"log"
	"math/rand"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/data"
	"time"

	"gorm.io/gorm"
)

type Scheduler struct {
	db *gorm.DB
}

func NewScheduler(db *gorm.DB) *Scheduler {
	return &Scheduler{db: db}
}

// GenerateScheduleForDate generates a complete schedule for a specific date
func (s *Scheduler) GenerateScheduleForDate(date time.Time) error {
	// Normalize date to start of day
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())

	log.Printf("Generating schedule for date: %s", date.Format("2006-01-02"))

	// Check if it's a holiday or Sunday
	if data.IsHoliday(s.db, date) {
		log.Printf("Date %s is a holiday or Sunday, skipping schedule generation", date.Format("2006-01-02"))
		return nil
	}

	// Check if schedule already exists
	var existingSchedule models.DailySchedule
	result := s.db.Where("date = ?", date).First(&existingSchedule)

	if result.Error == nil {
		log.Printf("Schedule already exists for %s, skipping", date.Format("2006-01-02"))
		return nil
	}

	// Create new daily schedule
	schedule := models.DailySchedule{
		Date:      date,
		Generated: true,
	}

	if err := s.db.Create(&schedule).Error; err != nil {
		return fmt.Errorf("failed to create daily schedule: %w", err)
	}
	
	// Generate meal tasks
	if err := s.generateMealTasks(&schedule, date); err != nil {
		return fmt.Errorf("failed to generate meal tasks: %w", err)
	}
	
	// Generate cleaning tasks
	if err := s.generateCleaningTasks(&schedule, date); err != nil {
		return fmt.Errorf("failed to generate cleaning tasks: %w", err)
	}
	
	// Add childcare tasks if they exist
	if err := s.addChildcareTasks(&schedule, date); err != nil {
		return fmt.Errorf("failed to add childcare tasks: %w", err)
	}
	
	log.Printf("Successfully generated schedule for %s with ID %d", date.Format("2006-01-02"), schedule.ID)
	return nil
}

// generateMealTasks creates meal tasks based on configured meal times
func (s *Scheduler) generateMealTasks(schedule *models.DailySchedule, date time.Time) error {
	var mealTimes []models.MealTime
	if err := s.db.Where("active = ?", true).Find(&mealTimes).Error; err != nil {
		return err
	}
	
	for _, mealTime := range mealTimes {
		// Find a suitable recipe for this meal
		recipe, err := s.selectRecipeForMeal(mealTime.Name, mealTime.FamilyMember)
		if err != nil {
			log.Printf("Warning: No recipe found for %s (%s), creating task without recipe", mealTime.Name, mealTime.FamilyMember)
		}
		
		task := models.ScheduleTask{
			ScheduleID:  schedule.ID,
			TaskType:    "meal",
			Time:        mealTime.DefaultTime,
			Title:       fmt.Sprintf("%s - %s", mealTime.Name, mealTime.FamilyMember),
			Description: "",
			Completed:   false,
		}
		
		if recipe != nil {
			task.RecipeID = &recipe.ID
			task.Description = recipe.Name
			task.Duration = 60 // default 1 hour for meals
		} else {
			task.Duration = 60 // default 1 hour
		}
		
		if err := s.db.Create(&task).Error; err != nil {
			return err
		}
	}
	
	return nil
}

// selectRecipeForMeal selects an appropriate recipe for a meal
func (s *Scheduler) selectRecipeForMeal(mealName, familyMember string) (*models.Recipe, error) {
	var recipes []models.Recipe
	
	// Query recipes matching the meal category and family member
	query := s.db.Where("category = ?", mealName)
	
	// Filter by family member (all, specific, or matching)
	query = query.Where("family_member = ? OR family_member = ?", familyMember, "all")
	
	if err := query.Find(&recipes).Error; err != nil {
		return nil, err
	}
	
	if len(recipes) == 0 {
		return nil, fmt.Errorf("no recipes found")
	}
	
	// Randomly select a recipe (can be improved with rotation logic)
	selectedRecipe := recipes[rand.Intn(len(recipes))]
	return &selectedRecipe, nil
}

// generateCleaningTasks creates cleaning tasks based on zone frequency
func (s *Scheduler) generateCleaningTasks(schedule *models.DailySchedule, date time.Time) error {
	var zones []models.CleaningZone
	// Order by priority: high > medium > low
	priorityOrder := "CASE priority WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END"
	if err := s.db.Order(priorityOrder).Find(&zones).Error; err != nil {
		return err
	}

	// Get the day of week (0 = Sunday, 6 = Saturday)
	dayOfWeek := int(date.Weekday())

	// Limit to max 2-3 cleaning zones per day
	maxZonesPerDay := 3
	zonesAddedToday := 0

	for _, zone := range zones {
		// Stop if we've reached the daily limit
		if zonesAddedToday >= maxZonesPerDay {
			break
		}

		// Determine if this zone should be cleaned today based on frequency
		if s.shouldCleanZoneToday(zone, dayOfWeek, date) {
			task := models.ScheduleTask{
				ScheduleID:  schedule.ID,
				TaskType:    "cleaning",
				Time:        "", // No specific time for cleaning tasks
				Duration:    30, // Default 30 minutes for cleaning
				Title:       fmt.Sprintf("Clean %s", zone.Name),
				Description: zone.Description,
				ZoneID:      &zone.ID,
				Completed:   false,
			}

			if err := s.db.Create(&task).Error; err != nil {
				return err
			}

			zonesAddedToday++
		}
	}

	return nil
}

// shouldCleanZoneToday determines if a zone should be cleaned on this day
func (s *Scheduler) shouldCleanZoneToday(zone models.CleaningZone, dayOfWeek int, date time.Time) bool {
	// Simple distribution algorithm: spread cleanings across the week
	// For frequency_per_week = 1: clean on specific day based on zone ID
	// For frequency_per_week = 2: clean on 2 specific days
	// etc.
	
	if zone.FrequencyPerWeek >= 7 {
		return true // clean every day
	}
	
	// Use zone ID to determine which days to clean
	// This ensures consistent scheduling
	daysToClean := s.calculateCleaningDays(zone.FrequencyPerWeek, int(zone.ID))
	
	for _, day := range daysToClean {
		if dayOfWeek == day {
			return true
		}
	}
	
	return false
}

// calculateCleaningDays returns which days of the week to clean based on frequency
func (s *Scheduler) calculateCleaningDays(frequency, zoneID int) []int {
	if frequency <= 0 {
		return []int{}
	}
	
	// Distribute days evenly across the week
	interval := 7 / frequency
	startDay := zoneID % 7 // offset based on zone ID for variety
	
	days := make([]int, 0, frequency)
	for i := 0; i < frequency; i++ {
		day := (startDay + (i * interval)) % 7
		days = append(days, day)
	}
	
	return days
}

// findAvailableTimeSlot finds a suitable time slot for a task
func (s *Scheduler) findAvailableTimeSlot(schedule *models.DailySchedule, duration int) string {
	// Simple implementation: assign cleaning tasks to morning slots
	// Can be improved to check for conflicts
	
	// Default cleaning time slots: 9:00, 10:30, 14:00, 15:30
	defaultSlots := []string{"09:00", "10:30", "14:00", "15:30"}
	
	// Return first available slot (simplified)
	// In a more advanced version, check existing tasks for conflicts
	if len(defaultSlots) > 0 {
		return defaultSlots[rand.Intn(len(defaultSlots))]
	}
	
	return "10:00"
}

// addChildcareTasks adds childcare tasks from the childcare schedule
func (s *Scheduler) addChildcareTasks(schedule *models.DailySchedule, date time.Time) error {
	var childcareSchedules []models.ChildcareSchedule
	
	// Find childcare schedules for this date
	if err := s.db.Where("DATE(date) = DATE(?)", date).Find(&childcareSchedules).Error; err != nil {
		return err
	}
	
	for _, cc := range childcareSchedules {
		task := models.ScheduleTask{
			ScheduleID:  schedule.ID,
			TaskType:    "childcare",
			Time:        cc.StartTime,
			EndTime:     cc.EndTime,
			Duration:    s.calculateDuration(cc.StartTime, cc.EndTime),
			Title:       "Childcare",
			Description: cc.Notes,
			Completed:   false,
		}

		if err := s.db.Create(&task).Error; err != nil {
			return err
		}
	}
	
	return nil
}

// calculateDuration calculates duration in minutes between two time strings (HH:MM)
func (s *Scheduler) calculateDuration(startTime, endTime string) int {
	start, _ := time.Parse("15:04", startTime)
	end, _ := time.Parse("15:04", endTime)
	
	duration := end.Sub(start)
	return int(duration.Minutes())
}

// GenerateScheduleForNextDays generates schedules for the next N days
func (s *Scheduler) GenerateScheduleForNextDays(days int) error {
	today := time.Now()
	
	for i := 0; i < days; i++ {
		date := today.AddDate(0, 0, i)
		if err := s.GenerateScheduleForDate(date); err != nil {
			log.Printf("Error generating schedule for %s: %v", date.Format("2006-01-02"), err)
		}
	}
	
	return nil
}

