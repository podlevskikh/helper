package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	"podlevskikh/awesomeProject/internal/data"
	"podlevskikh/awesomeProject/internal/models"

	"gorm.io/gorm"
)

func init() {
	// Initialize random number generator with current time
	rand.Seed(time.Now().UnixNano())
}

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

	log.Printf("Active meal times found: %d", len(mealTimes))
	for _, mt := range mealTimes {
		log.Printf("  -> meal time: id=%d name=%q family=%q", mt.ID, mt.Name, mt.FamilyMember)
	}

	for _, mealTime := range mealTimes {
		// Get all times for this meal (support multiple times per meal)
		times := s.getMealTimes(mealTime)

		// Create a task for each time slot
		for _, timeSlot := range times {
			// Find a suitable recipe for this meal
			recipe, err := s.selectRecipeForMeal(mealTime.ID, mealTime.Name, mealTime.FamilyMember, date)
			if err != nil {
				log.Printf("Warning: No recipe found for %s (%s), creating task without recipe", mealTime.Name, mealTime.FamilyMember)
			}

			task := models.ScheduleTask{
				ScheduleID:  schedule.ID,
				TaskType:    "meal",
				Time:        timeSlot,
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
	}

	return nil
}

// getMealTimes returns all time slots for a meal time
func (s *Scheduler) getMealTimes(mealTime models.MealTime) []string {
	// If DefaultTimes is set (JSON array), parse and return it
	if mealTime.DefaultTimes != "" {
		var times []string
		// Parse JSON array - expecting format: ["09:00", "12:00", "15:00"]
		if err := json.Unmarshal([]byte(mealTime.DefaultTimes), &times); err == nil && len(times) > 0 {
			return times
		}
		log.Printf("Warning: Failed to parse DefaultTimes for meal %s, falling back to DefaultTime", mealTime.Name)
	}

	// Fallback to single DefaultTime
	return []string{mealTime.DefaultTime}
}

// selectRecipeForMeal picks a random recipe for a meal slot using a full-cycle rotation:
//   - find all eligible recipes for this meal time + family member
//   - look back N days (where N = max(recipe count, 7)) to build the "used" set
//     only counting recipes used for THIS specific meal slot (by title)
//   - pick randomly from recipes NOT in the used set ("fresh" pool)
//   - if all recipes have been used (end of cycle), reset and pick from all
func (s *Scheduler) selectRecipeForMeal(mealTimeID uint, mealTimeName, familyMember string, currentDate time.Time) (*models.Recipe, error) {
	recipes, err := s.eligibleRecipes(mealTimeID, mealTimeName, familyMember)
	if err != nil {
		return nil, err
	}
	log.Printf("DIAG selectRecipeForMeal: meal=%q family=%q -> %d eligible recipes", mealTimeName, familyMember, len(recipes))
	if len(recipes) == 0 {
		return nil, fmt.Errorf("no recipes found for meal time %d (%s)", mealTimeID, mealTimeName)
	}
	if len(recipes) == 1 {
		return &recipes[0], nil
	}

	// Lookback window = recipe pool size (so every recipe appears before any repeats),
	// but at least 7 days.
	lookback := len(recipes)
	if lookback < 7 {
		lookback = 7
	}

	// Filter used recipes only for this specific meal slot (title = "MealName - FamilyMember")
	taskTitle := fmt.Sprintf("%s - %s", mealTimeName, familyMember)
	usedIDs := s.usedRecipeIDsSince(currentDate, lookback, taskTitle)

	// Partition into fresh (not used recently) and stale
	var fresh []models.Recipe
	for _, r := range recipes {
		if !usedIDs[r.ID] {
			fresh = append(fresh, r)
		}
	}

	pool := fresh
	if len(pool) == 0 {
		// Full cycle completed — reset and pick from entire pool
		pool = recipes
		log.Printf("Recipe cycle reset for meal time %d (%s): all %d recipes were used in the last %d days",
			mealTimeID, mealTimeName, len(recipes), lookback)
	}

	chosen := pool[rand.Intn(len(pool))]
	log.Printf("Selected recipe '%s' for meal time %d/%s (%d fresh / %d total)",
		chosen.Name, mealTimeID, mealTimeName, len(fresh), len(recipes))
	return &chosen, nil
}

// eligibleRecipes returns active recipes linked to the given meal time and family member.
// Falls back progressively until it finds something.
func (s *Scheduler) eligibleRecipes(mealTimeID uint, mealTimeName, familyMember string) ([]models.Recipe, error) {
	var recipes []models.Recipe

	// Primary: many-to-many join via recipe_meal_times
	err := s.db.
		Joins("JOIN recipe_meal_times ON recipe_meal_times.recipe_id = recipes.id").
		Where("recipe_meal_times.meal_time_id = ?", mealTimeID).
		Where("recipes.family_member = ? OR recipes.family_member = 'all' OR recipes.family_member = ''", familyMember).
		Where("recipes.is_active = ?", true).
		Find(&recipes).Error
	if err != nil {
		return nil, err
	}
	if len(recipes) > 0 {
		return recipes, nil
	}

	// Fallback 1: case-insensitive category match
	err = s.db.
		Where("LOWER(category) = LOWER(?)", mealTimeName).
		Where("family_member = ? OR family_member = 'all' OR family_member = ''", familyMember).
		Where("is_active = ?", true).
		Find(&recipes).Error
	if err != nil {
		return nil, err
	}
	if len(recipes) > 0 {
		log.Printf("Meal time %d (%s): category fallback, found %d recipes", mealTimeID, mealTimeName, len(recipes))
		return recipes, nil
	}

	// Fallback 2: all active recipes for this family member (including blank family_member)
	err = s.db.
		Where("family_member = ? OR family_member = 'all' OR family_member = ''", familyMember).
		Where("is_active = ?", true).
		Find(&recipes).Error
	if err != nil {
		return nil, err
	}
	if len(recipes) > 0 {
		log.Printf("Meal time %d (%s): family_member fallback, found %d recipes", mealTimeID, mealTimeName, len(recipes))
		return recipes, nil
	}

	// Fallback 3: all active recipes regardless of family_member
	err = s.db.Where("is_active = ?", true).Find(&recipes).Error
	if err != nil {
		return nil, err
	}
	if len(recipes) > 0 {
		log.Printf("Meal time %d (%s): global fallback (no family_member filter), found %d recipes", mealTimeID, mealTimeName, len(recipes))
	}
	return recipes, nil
}

// usedRecipeIDsSince returns the set of recipe IDs used in tasks with the given title
// within the last `days` days before `before`.
// Filtering by title (e.g. "Breakfast - adult") ensures we only consider the same meal slot,
// so recipes used at lunch don't block breakfast choices.
func (s *Scheduler) usedRecipeIDsSince(before time.Time, days int, taskTitle string) map[uint]bool {
	used := make(map[uint]bool)

	startDate := before.AddDate(0, 0, -days)

	var tasks []models.ScheduleTask
	err := s.db.
		Joins("JOIN daily_schedules ON daily_schedules.id = schedule_tasks.schedule_id").
		Where("daily_schedules.date >= ? AND daily_schedules.date < ?", startDate, before).
		Where("schedule_tasks.task_type = 'meal'").
		Where("schedule_tasks.title = ?", taskTitle).
		Where("schedule_tasks.recipe_id IS NOT NULL").
		Find(&tasks).Error

	if err != nil {
		log.Printf("Warning: failed to query used recipes: %v", err)
		return used
	}

	for _, t := range tasks {
		if t.RecipeID != nil {
			used[*t.RecipeID] = true
		}
	}

	log.Printf("Slot '%s': found %d distinct recipes used in the last %d days", taskTitle, len(used), days)
	return used
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
				Title:       zone.Name,
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

	// Normalize the date to start of day for comparison
	normalizedDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	nextDay := normalizedDate.AddDate(0, 0, 1)

	// Find childcare schedules for this date
	// Use range query to handle different time zones and date storage formats
	if err := s.db.Where("date >= ? AND date < ?", normalizedDate, nextDay).Find(&childcareSchedules).Error; err != nil {
		return err
	}

	log.Printf("Found %d childcare schedules for date %s", len(childcareSchedules), date.Format("2006-01-02"))

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

		log.Printf("Created childcare task: %s - %s", cc.StartTime, cc.EndTime)
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
