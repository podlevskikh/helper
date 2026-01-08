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

	for _, mealTime := range mealTimes {
		// Get all times for this meal (support multiple times per meal)
		times := s.getMealTimes(mealTime)

		// Create a task for each time slot
		for _, timeSlot := range times {
			// Find a suitable recipe for this meal
			recipe, err := s.selectRecipeForMeal(mealTime.ID, mealTime.FamilyMember, date)
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

// selectRecipeForMeal selects an appropriate recipe for a meal with smart rotation
// to avoid frequent repetition of the same dishes
func (s *Scheduler) selectRecipeForMeal(mealTimeID uint, familyMember string, currentDate time.Time) (*models.Recipe, error) {
	var recipes []models.Recipe

	// Query recipes that are linked to this meal time via many-to-many relation
	// Also filter by family member and is_active status
	err := s.db.
		Joins("JOIN recipe_meal_times ON recipe_meal_times.recipe_id = recipes.id").
		Where("recipe_meal_times.meal_time_id = ?", mealTimeID).
		Where("recipes.family_member = ? OR recipes.family_member = ?", familyMember, "all").
		Where("recipes.is_active = ?", true).
		Find(&recipes).Error

	if err != nil {
		return nil, err
	}

	// Fallback: if no recipes found with many-to-many, try old category field
	if len(recipes) == 0 {
		var mealTime models.MealTime
		if err := s.db.First(&mealTime, mealTimeID).Error; err != nil {
			return nil, err
		}

		// Try to find recipes using old category field
		query := s.db.Where("category = ?", mealTime.Name)
		query = query.Where("family_member = ? OR family_member = ?", familyMember, "all")
		query = query.Where("is_active = ?", true)

		if err := query.Find(&recipes).Error; err != nil {
			return nil, err
		}
	}

	if len(recipes) == 0 {
		return nil, fmt.Errorf("no recipes found")
	}

	// Get recently used recipes with their last use dates
	recentlyUsed := s.getRecentlyUsedRecipes(mealTimeID, 30) // Look back 30 days for better rotation

	// Get yesterday's recipes to ensure no repetition
	yesterdayRecipes := s.getYesterdayRecipes(currentDate, mealTimeID)

	// Select recipe with improved weighted random selection
	selectedRecipe := s.selectRecipeWithImprovedRotation(recipes, recentlyUsed, yesterdayRecipes)
	return selectedRecipe, nil
}

// getRecentlyUsedRecipes returns a map of recipe IDs to days since last use
func (s *Scheduler) getRecentlyUsedRecipes(mealTimeID uint, lookbackDays int) map[uint]int {
	recentlyUsed := make(map[uint]int)

	// Calculate the date range to look back
	today := time.Now()
	startDate := today.AddDate(0, 0, -lookbackDays)

	// Query schedule tasks with recipes from the past N days
	var tasks []models.ScheduleTask
	err := s.db.
		Joins("JOIN daily_schedules ON daily_schedules.id = schedule_tasks.schedule_id").
		Where("daily_schedules.date >= ? AND daily_schedules.date < ?", startDate, today).
		Where("schedule_tasks.task_type = ?", "meal").
		Where("schedule_tasks.recipe_id IS NOT NULL").
		Select("schedule_tasks.recipe_id, daily_schedules.date").
		Find(&tasks).Error

	if err != nil {
		log.Printf("Warning: Failed to get recently used recipes: %v", err)
		return recentlyUsed
	}

	// Build map of recipe ID to days since last use
	for _, task := range tasks {
		if task.RecipeID != nil {
			// Get the schedule date for this task
			var schedule models.DailySchedule
			if err := s.db.First(&schedule, task.ScheduleID).Error; err == nil {
				daysSince := int(today.Sub(schedule.Date).Hours() / 24)

				// Keep track of the most recent use
				if existing, ok := recentlyUsed[*task.RecipeID]; !ok || daysSince < existing {
					recentlyUsed[*task.RecipeID] = daysSince
				}
			}
		}
	}

	return recentlyUsed
}

// getYesterdayRecipes returns a set of recipe IDs used yesterday for the same meal type
// This ensures we never repeat a dish two days in a row
func (s *Scheduler) getYesterdayRecipes(currentDate time.Time, mealTimeID uint) map[uint]bool {
	yesterdayRecipes := make(map[uint]bool)

	// Calculate yesterday's date
	yesterday := currentDate.AddDate(0, 0, -1)
	yesterdayStart := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, yesterday.Location())
	yesterdayEnd := yesterdayStart.AddDate(0, 0, 1)

	// Query yesterday's meal tasks for this meal type
	var tasks []models.ScheduleTask
	err := s.db.
		Joins("JOIN daily_schedules ON daily_schedules.id = schedule_tasks.schedule_id").
		Where("daily_schedules.date >= ? AND daily_schedules.date < ?", yesterdayStart, yesterdayEnd).
		Where("schedule_tasks.task_type = ?", "meal").
		Where("schedule_tasks.recipe_id IS NOT NULL").
		Find(&tasks).Error

	if err != nil {
		log.Printf("Warning: Failed to get yesterday's recipes: %v", err)
		return yesterdayRecipes
	}

	// Build set of recipe IDs used yesterday
	for _, task := range tasks {
		if task.RecipeID != nil {
			yesterdayRecipes[*task.RecipeID] = true
		}
	}

	log.Printf("Found %d recipes used yesterday", len(yesterdayRecipes))
	return yesterdayRecipes
}

// selectRecipeWithImprovedRotation selects a recipe using improved weighted random selection
// with strict rules to prevent repetition and ensure even rotation
func (s *Scheduler) selectRecipeWithImprovedRotation(recipes []models.Recipe, recentlyUsed map[uint]int, yesterdayRecipes map[uint]bool) *models.Recipe {
	if len(recipes) == 0 {
		return nil
	}

	// If only one recipe available, return it (even if used yesterday - no choice)
	if len(recipes) == 1 {
		return &recipes[0]
	}

	// Calculate weights for each recipe
	type weightedRecipe struct {
		recipe     *models.Recipe
		weight     float64
		daysSince  int
		usedYesterday bool
	}

	var weightedRecipes []weightedRecipe
	var eligibleRecipes []weightedRecipe // Recipes NOT used yesterday
	totalWeight := 0.0
	totalEligibleWeight := 0.0

	for i := range recipes {
		recipe := &recipes[i]
		weight := 1.0 // Base weight
		daysSince := -1
		usedYesterday := yesterdayRecipes[recipe.ID]

		// Get days since last use
		if days, used := recentlyUsed[recipe.ID]; used {
			daysSince = days

			// Improved weight formula for better rotation:
			// The longer since last use, the higher the weight
			// This creates exponential preference for less recently used recipes
			if daysSince == 0 {
				weight = 0.05 // Almost never (same day)
			} else if daysSince == 1 {
				weight = 0.1  // Very low (yesterday - but this should be filtered out)
			} else if daysSince == 2 {
				weight = 0.3  // Low
			} else if daysSince == 3 {
				weight = 0.5  // Medium-low
			} else if daysSince <= 5 {
				weight = 0.8  // Medium
			} else if daysSince <= 7 {
				weight = 1.0  // Normal
			} else if daysSince <= 14 {
				weight = 1.5  // Higher preference
			} else if daysSince <= 21 {
				weight = 2.0  // Strong preference
			} else {
				weight = 3.0  // Very strong preference for recipes not used in 3+ weeks
			}
		} else {
			// Never used before - give highest weight
			weight = 5.0
		}

		wr := weightedRecipe{
			recipe:        recipe,
			weight:        weight,
			daysSince:     daysSince,
			usedYesterday: usedYesterday,
		}

		weightedRecipes = append(weightedRecipes, wr)
		totalWeight += weight

		// Track eligible recipes (not used yesterday)
		if !usedYesterday {
			eligibleRecipes = append(eligibleRecipes, wr)
			totalEligibleWeight += weight
		}
	}

	// STRICT RULE: Never use a recipe from yesterday if we have alternatives
	var candidateRecipes []weightedRecipe
	var candidateTotalWeight float64

	if len(eligibleRecipes) > 0 {
		// Use only recipes NOT used yesterday
		candidateRecipes = eligibleRecipes
		candidateTotalWeight = totalEligibleWeight
		log.Printf("Selecting from %d eligible recipes (excluding yesterday's %d recipes)",
			len(eligibleRecipes), len(yesterdayRecipes))
	} else {
		// No choice - all recipes were used yesterday (unlikely with enough recipes)
		candidateRecipes = weightedRecipes
		candidateTotalWeight = totalWeight
		log.Printf("Warning: All recipes were used yesterday, selecting from all %d recipes", len(weightedRecipes))
	}

	// Select a random recipe based on weights
	randomValue := rand.Float64() * candidateTotalWeight
	currentSum := 0.0

	for _, wr := range candidateRecipes {
		currentSum += wr.weight
		if currentSum >= randomValue {
			log.Printf("Selected recipe: %s (weight: %.2f, days since last use: %d, used yesterday: %v)",
				wr.recipe.Name, wr.weight, wr.daysSince, wr.usedYesterday)
			return wr.recipe
		}
	}

	// Fallback: return the last candidate recipe (should rarely happen)
	lastRecipe := candidateRecipes[len(candidateRecipes)-1]
	log.Printf("Fallback: Selected recipe: %s", lastRecipe.recipe.Name)
	return lastRecipe.recipe
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
