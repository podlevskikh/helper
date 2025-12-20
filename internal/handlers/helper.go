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

// GetTodaySchedule returns today's schedule with all tasks
func (h *HelperHandler) GetTodaySchedule(c *gin.Context) {
	today := time.Now()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())

	var schedule models.DailySchedule
	if err := h.db.Preload("Tasks.Recipe").Preload("Tasks.Recipes").Preload("Tasks.Zone").Preload("Tasks.Zones").
		Where("date = ?", todayStart).First(&schedule).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"message": "No schedule for today", "tasks": []models.ScheduleTask{}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

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
	if err := h.db.Preload("Tasks.Recipe").Preload("Tasks.Recipes").Preload("Tasks.Zone").Preload("Tasks.Zones").
		Where("date = ?", date).First(&schedule).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"message": "No schedule for this date", "tasks": []models.ScheduleTask{}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

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

