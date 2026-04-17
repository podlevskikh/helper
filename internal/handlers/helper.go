package handlers

import (
	"net/http"
	"podlevskikh/awesomeProject/internal/models"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type HelperHandler struct {
	db *gorm.DB
}

func NewHelperHandler(db *gorm.DB) *HelperHandler {
	return &HelperHandler{db: db}
}

// mergeChildcareTasks ensures all ChildcareSchedule entries for `date` have a
// corresponding ScheduleTask in the given schedule. Missing tasks are created in
// the DB and appended to schedule.Tasks.  `date` is the local calendar date.
func (h *HelperHandler) mergeChildcareTasks(schedule *models.DailySchedule, date time.Time) {
	utcDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	nextDay := utcDate.AddDate(0, 0, 1)

	var ccList []models.ChildcareSchedule
	if err := h.db.Where("date >= ? AND date < ?", utcDate, nextDay).Find(&ccList).Error; err != nil {
		return
	}

	// Build set of existing childcare task start times so we don't duplicate.
	existing := make(map[string]bool)
	for _, t := range schedule.Tasks {
		if t.TaskType == "childcare" {
			existing[t.Time] = true
		}
	}

	for _, cc := range ccList {
		if existing[cc.StartTime] {
			continue
		}
		task := models.ScheduleTask{
			ScheduleID:  schedule.ID,
			TaskType:    "childcare",
			Time:        cc.StartTime,
			EndTime:     cc.EndTime,
			Duration:    childcareDuration(cc.StartTime, cc.EndTime),
			Title:       "Childcare",
			Description: cc.Notes,
			Completed:   false,
		}
		if err := h.db.Create(&task).Error; err == nil {
			schedule.Tasks = append(schedule.Tasks, task)
		}
	}
}

// childcareDuration returns duration in minutes between two "HH:MM" strings.
func childcareDuration(startTime, endTime string) int {
	start, _ := time.Parse("15:04", startTime)
	end, _ := time.Parse("15:04", endTime)
	return int(end.Sub(start).Minutes())
}

// GetTodaySchedule returns today's schedule with all tasks
func (h *HelperHandler) GetTodaySchedule(c *gin.Context) {
	today := time.Now()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())

	var schedule models.DailySchedule
	err := h.db.Preload("Tasks.Recipe").Preload("Tasks.Recipes").Preload("Tasks.Zone").Preload("Tasks.Zones").
		Where("date = ?", todayStart).First(&schedule).Error

	if err == gorm.ErrRecordNotFound {
		// No generated schedule yet — create a minimal one if childcare entries exist.
		utcDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		var ccCount int64
		h.db.Model(&models.ChildcareSchedule{}).
			Where("date >= ? AND date < ?", utcDate, utcDate.AddDate(0, 0, 1)).
			Count(&ccCount)
		if ccCount == 0 {
			c.JSON(http.StatusOK, gin.H{"message": "No schedule for today", "tasks": []models.ScheduleTask{}})
			return
		}
		schedule = models.DailySchedule{Date: todayStart, Generated: false}
		if createErr := h.db.Create(&schedule).Error; createErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": createErr.Error()})
			return
		}
		schedule.Tasks = []models.ScheduleTask{}
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.mergeChildcareTasks(&schedule, today)
	c.JSON(http.StatusOK, schedule)
}

// GetScheduleByDate returns schedule for a specific date
func (h *HelperHandler) GetScheduleByDate(c *gin.Context) {
	dateStr := c.Param("date") // format: YYYY-MM-DD
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format. Use YYYY-MM-DD"})
		return
	}

	var schedule models.DailySchedule
	loadErr := h.db.Preload("Tasks.Recipe").Preload("Tasks.Recipes").Preload("Tasks.Zone").Preload("Tasks.Zones").
		Where("date = ?", date).First(&schedule).Error

	if loadErr == gorm.ErrRecordNotFound {
		// No generated schedule yet — create a minimal one if childcare entries exist.
		utcDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		var ccCount int64
		h.db.Model(&models.ChildcareSchedule{}).
			Where("date >= ? AND date < ?", utcDate, utcDate.AddDate(0, 0, 1)).
			Count(&ccCount)
		if ccCount == 0 {
			c.JSON(http.StatusOK, gin.H{"message": "No schedule for this date", "tasks": []models.ScheduleTask{}})
			return
		}
		schedule = models.DailySchedule{Date: date, Generated: false}
		if createErr := h.db.Create(&schedule).Error; createErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": createErr.Error()})
			return
		}
		schedule.Tasks = []models.ScheduleTask{}
	} else if loadErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": loadErr.Error()})
		return
	}

	h.mergeChildcareTasks(&schedule, date)
	c.JSON(http.StatusOK, schedule)
}

// GetUpcomingSchedules returns schedules for the next N days
func (h *HelperHandler) GetUpcomingSchedules(c *gin.Context) {
	days := 7 // default 7 days
	if daysParam := c.Query("days"); daysParam != "" {
		if d, err := strconv.Atoi(daysParam); err == nil {
			days = d
		}
	}

	// Support custom start date
	var startDate time.Time
	if startDateParam := c.Query("start_date"); startDateParam != "" {
		if parsed, err := time.Parse("2006-01-02", startDateParam); err == nil {
			startDate = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
		} else {
			today := time.Now()
			startDate = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		}
	} else {
		today := time.Now()
		startDate = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	}

	endDate := startDate.AddDate(0, 0, days)

	var schedules []models.DailySchedule
	if err := h.db.Preload("Tasks.Recipe").Preload("Tasks.Recipes").Preload("Tasks.Zone").Preload("Tasks.Zones").
		Where("date >= ? AND date < ?", startDate, endDate).
		Order("date").Find(&schedules).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Merge childcare entries into existing schedules.
	for i := range schedules {
		h.mergeChildcareTasks(&schedules[i], schedules[i].Date)
	}

	c.JSON(http.StatusOK, schedules)
}

// CompleteTask marks a task as completed
func (h *HelperHandler) CompleteTask(c *gin.Context) {
	taskID := c.Param("id")
	
	var task models.ScheduleTask
	if err := h.db.First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}
	
	task.Completed = true
	if err := h.db.Save(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, task)
}

// UncompleteTask marks a task as not completed
func (h *HelperHandler) UncompleteTask(c *gin.Context) {
	taskID := c.Param("id")
	
	var task models.ScheduleTask
	if err := h.db.First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}
	
	task.Completed = false
	if err := h.db.Save(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, task)
}

// Shopping list handlers

func (h *HelperHandler) GetShoppingList(c *gin.Context) {
	var items []models.ShoppingListItem
	if err := h.db.Where("purchased = ?", false).Order("category, item").Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *HelperHandler) AddShoppingListItem(c *gin.Context) {
	var item models.ShoppingListItem
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	item.AddedBy = "helper"
	
	if err := h.db.Create(&item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusCreated, item)
}

func (h *HelperHandler) MarkItemPurchased(c *gin.Context) {
	itemID := c.Param("id")
	
	var item models.ShoppingListItem
	if err := h.db.First(&item, itemID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
		return
	}
	
	item.Purchased = true
	if err := h.db.Save(&item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, item)
}

func (h *HelperHandler) DeleteShoppingListItem(c *gin.Context) {
	itemID := c.Param("id")
	if err := h.db.Delete(&models.ShoppingListItem{}, itemID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Item deleted"})
}

// GetRecipeDetails returns full recipe details for a specific recipe
func (h *HelperHandler) GetRecipeDetails(c *gin.Context) {
	recipeID := c.Param("id")

	var recipe models.Recipe
	if err := h.db.First(&recipe, recipeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	c.JSON(http.StatusOK, recipe)
}

// Childcare handlers

// GetTodayChildcare returns childcare schedule for today
func (h *HelperHandler) GetTodayChildcare(c *gin.Context) {
	today := time.Now()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	nextDay := todayStart.AddDate(0, 0, 1)

	var schedules []models.ChildcareSchedule
	if err := h.db.Where("date >= ? AND date < ?", todayStart, nextDay).
		Order("start_time").Find(&schedules).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, schedules)
}

// SaveTodayChildcare saves or updates childcare schedule for today
func (h *HelperHandler) SaveTodayChildcare(c *gin.Context) {
	var input struct {
		StartTime string `json:"start_time" binding:"required"`
		EndTime   string `json:"end_time" binding:"required"`
		Notes     string `json:"notes"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	today := time.Now()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	nextDay := todayStart.AddDate(0, 0, 1)

	// Check if there's already a childcare schedule for today
	var existing models.ChildcareSchedule
	err := h.db.Where("date >= ? AND date < ?", todayStart, nextDay).First(&existing).Error

	if err == gorm.ErrRecordNotFound {
		// Create new schedule
		schedule := models.ChildcareSchedule{
			Date:      todayStart,
			StartTime: input.StartTime,
			EndTime:   input.EndTime,
			Notes:     input.Notes,
		}

		if err := h.db.Create(&schedule).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, schedule)
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else {
		// Update existing schedule
		existing.StartTime = input.StartTime
		existing.EndTime = input.EndTime
		existing.Notes = input.Notes

		if err := h.db.Save(&existing).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, existing)
	}
}

// DeleteTodayChildcare deletes childcare schedule for today
func (h *HelperHandler) DeleteTodayChildcare(c *gin.Context) {
	today := time.Now()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	nextDay := todayStart.AddDate(0, 0, 1)

	// Find and delete childcare schedule for today
	if err := h.db.Where("date >= ? AND date < ?", todayStart, nextDay).
		Delete(&models.ChildcareSchedule{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Childcare schedule deleted"})
}
