package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"

	"gorm.io/gorm"
)

func init() {
	// Initialize random number generator with current time
	rand.Seed(time.Now().UnixNano())
}

// Планировщик собирает дни каждого дома из его приёмов пищи, зон уборки и распорядка ребёнка.
// Все выборки и записи — только по этому дому: чужие блюда и зоны в день не попадают.

type Scheduler struct {
	db *gorm.DB
}

func NewScheduler(db *gorm.DB) *Scheduler {
	return &Scheduler{db: db}
}

// GenerateScheduleForDate собирает день для всех домов.
func (s *Scheduler) GenerateScheduleForDate(date time.Time) error {
	var orgIDs []uint
	if err := s.db.Model(&models.Organization{}).Order("id").Pluck("id", &orgIDs).Error; err != nil {
		return err
	}
	var firstErr error
	for _, orgID := range orgIDs {
		if err := s.GenerateScheduleForOrgDate(orgID, date); err != nil {
			log.Printf("Error generating schedule for org %d, %s: %v", orgID, date.Format("2006-01-02"), err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// GenerateScheduleForOrgDate собирает день одного дома.
func (s *Scheduler) GenerateScheduleForOrgDate(orgID uint, date time.Time) error {
	// День — полночь UTC календарной даты (как во всех daily_schedules).
	date = clock.Day(date)

	log.Printf("Generating schedule for org %d, date: %s", orgID, date.Format("2006-01-02"))

	// День не собирается, если никто из помощников не работает (выходные дни, праздники страны,
	// отпуска и отгулы — см. internal/calendar).
	if !calendar.AnyoneWorks(s.db, orgID, date) {
		log.Printf("Date %s: nobody works, skipping schedule generation", date.Format("2006-01-02"))
		return nil
	}

	// Дому нечего собирать (нет приёмов пищи, зон уборки и ребёнка на этот день) — день не создаём,
	// чтобы добавленные позже зоны и приёмы пищи попали в ближайшие дни.
	if !s.hasTemplates(orgID, date) {
		return nil
	}

	// Check if schedule already exists
	var existingSchedule models.DailySchedule
	result := s.db.Where("organization_id = ? AND date = ?", orgID, date).First(&existingSchedule)

	var schedule models.DailySchedule
	if result.Error == nil {
		if existingSchedule.Generated {
			log.Printf("Schedule already generated for %s, skipping", date.Format("2006-01-02"))
			return nil
		}
		// Exists but not yet generated (e.g. holds custom tasks) — generate into it
		schedule = existingSchedule
		schedule.Generated = true
		if err := s.db.Save(&schedule).Error; err != nil {
			return fmt.Errorf("failed to update daily schedule: %w", err)
		}
	} else {
		// Create new daily schedule
		var err error
		if schedule, err = models.EnsureDailySchedule(s.db, orgID, date, true); err != nil {
			return fmt.Errorf("failed to create daily schedule: %w", err)
		}
		if !schedule.Generated { // параллельно день успел создать запрос пользователя
			schedule.Generated = true
			if err := s.db.Save(&schedule).Error; err != nil {
				return fmt.Errorf("failed to update daily schedule: %w", err)
			}
		}
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
	if err := s.db.Where("organization_id = ? AND active = ?", schedule.OrganizationID, true).Find(&mealTimes).Error; err != nil {
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
			recipe, err := s.selectRecipeForMeal(schedule.OrganizationID, mealTime.ID, mealTime.Name, mealTime.FamilyMember, date)
			if err != nil {
				log.Printf("Warning: No recipe found for %s (%s), creating task without recipe", mealTime.Name, mealTime.FamilyMember)
			}

			task := models.ScheduleTask{
				OrganizationID: schedule.OrganizationID,
				ScheduleID:     schedule.ID,
				TaskType:       "meal",
				Time:           timeSlot,
				Title:          fmt.Sprintf("%s - %s", mealTime.Name, mealTime.FamilyMember),
				Description:    "",
				Completed:      false,
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
func (s *Scheduler) selectRecipeForMeal(orgID, mealTimeID uint, mealTimeName, familyMember string, currentDate time.Time) (*models.Recipe, error) {
	recipes, err := s.eligibleRecipes(orgID, mealTimeID, mealTimeName)
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
	usedIDs := s.usedRecipeIDsSince(orgID, currentDate, lookback, taskTitle)

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

// eligibleRecipes returns active recipes linked to the given meal time via recipe_meal_times.
// Only recipes explicitly assigned to this meal time are considered.
func (s *Scheduler) eligibleRecipes(orgID, mealTimeID uint, mealTimeName string) ([]models.Recipe, error) {
	var recipes []models.Recipe
	err := s.db.
		Joins("JOIN recipe_meal_times ON recipe_meal_times.recipe_id = recipes.id").
		Where("recipe_meal_times.meal_time_id = ?", mealTimeID).
		Where("recipes.is_active = ? AND recipes.organization_id = ?", true, orgID).
		Find(&recipes).Error
	if err != nil {
		return nil, err
	}
	log.Printf("Meal time %d (%s): found %d eligible recipes", mealTimeID, mealTimeName, len(recipes))
	return recipes, nil
}

// usedRecipeIDsSince returns the set of recipe IDs used in tasks with the given title
// within the last `days` days before `before`.
// Filtering by title (e.g. "Breakfast - adult") ensures we only consider the same meal slot,
// so recipes used at lunch don't block breakfast choices.
func (s *Scheduler) usedRecipeIDsSince(orgID uint, before time.Time, days int, taskTitle string) map[uint]bool {
	used := make(map[uint]bool)

	startDate := before.AddDate(0, 0, -days)

	var tasks []models.ScheduleTask
	err := s.db.
		Joins("JOIN daily_schedules ON daily_schedules.id = schedule_tasks.schedule_id").
		Where("daily_schedules.organization_id = ?", orgID).
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
	if err := s.db.Where("organization_id = ?", schedule.OrganizationID).Order(priorityOrder).Find(&zones).Error; err != nil {
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

		// Уже есть в этом дне (например, выполнена до пересборки) — второй раз не ставим.
		var exists int64
		s.db.Model(&models.ScheduleTask{}).Where("schedule_id = ? AND zone_id = ?", schedule.ID, zone.ID).Count(&exists)
		if exists > 0 {
			zonesAddedToday++
			continue
		}
		// Determine if this zone should be cleaned today based on frequency
		if s.shouldCleanZoneToday(zone, dayOfWeek, date) {
			task := models.ScheduleTask{
				OrganizationID: schedule.OrganizationID,
				ScheduleID:     schedule.ID,
				TaskType:       "cleaning",
				Time:           "", // No specific time for cleaning tasks
				Duration:       30, // Default 30 minutes for cleaning
				Title:          zone.Name,
				Description:    zone.Description,
				ZoneID:         &zone.ID,
				Completed:      false,
			}

			if err := s.db.Create(&task).Error; err != nil {
				return err
			}

			zonesAddedToday++
		}
	}

	return nil
}

// shouldCleanZoneToday: зона убирается в свои дни недели — по рабочим дням дома (calendar.CleaningDays).
func (s *Scheduler) shouldCleanZoneToday(zone models.CleaningZone, _ int, date time.Time) bool {
	iso := calendar.ISOWeekday(date)
	for _, d := range calendar.CleaningDays(zone.FrequencyPerWeek, zone.ID, calendar.OrgWorkdays(s.db, zone.OrganizationID)) {
		if d == iso {
			return true
		}
	}
	return false
}

// RefreshCleaning пересобирает уборку в ближайших днях дома (после смены выходных или зон):
// невыполненные задачи уборки удаляются и создаются заново по текущим правилам. Выполненные остаются.
func (s *Scheduler) RefreshCleaning(orgID uint, days int) error {
	today := clock.OrgToday(s.db, orgID)
	for i := 0; i < days; i++ {
		day := today.AddDate(0, 0, i)
		var schedule models.DailySchedule
		if s.db.Where("organization_id = ? AND date = ?", orgID, day).First(&schedule).Error != nil {
			if err := s.GenerateScheduleForOrgDate(orgID, day); err != nil {
				return err
			}
			continue
		}
		err := s.db.Transaction(func(tx *gorm.DB) error {
			ids := tx.Model(&models.ScheduleTask{}).Select("id").
				Where("schedule_id = ? AND task_type = 'cleaning' AND completed = false", schedule.ID)
			if err := tx.Exec("DELETE FROM task_zones WHERE schedule_task_id IN (?)", ids).Error; err != nil {
				return err
			}
			return tx.Where("schedule_id = ? AND task_type = 'cleaning' AND completed = false", schedule.ID).
				Delete(&models.ScheduleTask{}).Error
		})
		if err != nil {
			return err
		}
		if calendar.AnyoneWorks(s.db, orgID, day) {
			if err := s.generateCleaningTasks(&schedule, day); err != nil {
				return err
			}
		}
	}
	return nil
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

	// Normalize to UTC midnight — childcare dates are stored as UTC midnight from the frontend
	normalizedDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	nextDay := normalizedDate.AddDate(0, 0, 1)

	if err := s.db.Where("organization_id = ? AND date >= ? AND date < ?", schedule.OrganizationID, normalizedDate, nextDay).Find(&childcareSchedules).Error; err != nil {
		return err
	}

	log.Printf("Found %d childcare schedules for date %s", len(childcareSchedules), date.Format("2006-01-02"))

	for _, cc := range childcareSchedules {
		task := models.ScheduleTask{
			OrganizationID: schedule.OrganizationID,
			ScheduleID:     schedule.ID,
			TaskType:       "childcare",
			Time:           cc.StartTime,
			EndTime:        cc.EndTime,
			Duration:       s.calculateDuration(cc.StartTime, cc.EndTime),
			Title:          "Childcare",
			Description:    cc.Notes,
			Completed:      false,
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

// hasTemplates — есть ли у дома из чего собрать день.
func (s *Scheduler) hasTemplates(orgID uint, date time.Time) bool {
	var n int64
	s.db.Model(&models.MealTime{}).Where("organization_id = ? AND active = ?", orgID, true).Count(&n)
	if n > 0 {
		return true
	}
	s.db.Model(&models.CleaningZone{}).Where("organization_id = ?", orgID).Count(&n)
	if n > 0 {
		return true
	}
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	s.db.Model(&models.ChildcareSchedule{}).
		Where("organization_id = ? AND date >= ? AND date < ?", orgID, day, day.AddDate(0, 0, 1)).Count(&n)
	return n > 0
}

// GenerateScheduleForOrgNextDays собирает ближайшие дни одного дома.
func (s *Scheduler) GenerateScheduleForOrgNextDays(orgID uint, days int) error {
	today := clock.OrgToday(s.db, orgID)
	for i := 0; i < days; i++ {
		date := today.AddDate(0, 0, i)
		if err := s.GenerateScheduleForOrgDate(orgID, date); err != nil {
			return err
		}
	}
	return nil
}

// GenerateScheduleForNextDays generates schedules for the next N days
func (s *Scheduler) GenerateScheduleForNextDays(days int) error {
	// У каждого дома своё «сегодня» (часовой пояс дома).
	var orgIDs []uint
	if err := s.db.Model(&models.Organization{}).Order("id").Pluck("id", &orgIDs).Error; err != nil {
		return err
	}
	for _, orgID := range orgIDs {
		if err := s.GenerateScheduleForOrgNextDays(orgID, days); err != nil {
			log.Printf("Error generating schedule for org %d: %v", orgID, err)
		}
	}
	return nil
}
