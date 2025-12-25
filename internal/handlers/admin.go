package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"podlevskikh/awesomeProject/internal/models"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AdminHandler struct {
	db *gorm.DB
}

func NewAdminHandler(db *gorm.DB) *AdminHandler {
	return &AdminHandler{db: db}
}

// Recipe handlers

func (h *AdminHandler) GetRecipes(c *gin.Context) {
	var recipes []models.Recipe
	if err := h.db.Preload("MealTimes").Order("created_at DESC").Find(&recipes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, recipes)
}

func (h *AdminHandler) GetRecipe(c *gin.Context) {
	id := c.Param("id")
	var recipe models.Recipe
	if err := h.db.Preload("MealTimes").First(&recipe, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}
	c.JSON(http.StatusOK, recipe)
}

func (h *AdminHandler) CreateRecipe(c *gin.Context) {
	var input struct {
		models.Recipe
		MealTimeIDs []uint `json:"meal_time_ids"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	recipe := input.Recipe

	// Create the recipe first
	if err := h.db.Create(&recipe).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Associate meal times if provided
	if len(input.MealTimeIDs) > 0 {
		var mealTimes []models.MealTime
		if err := h.db.Find(&mealTimes, input.MealTimeIDs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find meal times"})
			return
		}

		if err := h.db.Model(&recipe).Association("MealTimes").Replace(mealTimes); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to associate meal times"})
			return
		}
	}

	// Reload recipe with associations
	h.db.Preload("MealTimes").First(&recipe, recipe.ID)

	c.JSON(http.StatusCreated, recipe)
}

func (h *AdminHandler) UpdateRecipe(c *gin.Context) {
	id := c.Param("id")
	var recipe models.Recipe

	if err := h.db.First(&recipe, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	var input struct {
		models.Recipe
		MealTimeIDs []uint `json:"meal_time_ids"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update recipe fields
	recipe.Name = input.Name
	recipe.Description = input.Description
	recipe.Ingredients = input.Ingredients
	recipe.Instructions = input.Instructions
	recipe.FamilyMember = input.FamilyMember
	recipe.Tags = input.Tags
	recipe.ImageURL = input.ImageURL
	recipe.VideoURL = input.VideoURL
	recipe.Rating = input.Rating
	recipe.PrepTime = input.PrepTime
	recipe.CookTime = input.CookTime
	recipe.Servings = input.Servings
	recipe.IsActive = input.IsActive

	if err := h.db.Save(&recipe).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update meal time associations
	if input.MealTimeIDs != nil {
		var mealTimes []models.MealTime
		if len(input.MealTimeIDs) > 0 {
			if err := h.db.Find(&mealTimes, input.MealTimeIDs).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find meal times"})
				return
			}
		}

		if err := h.db.Model(&recipe).Association("MealTimes").Replace(mealTimes); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update meal times"})
			return
		}
	}

	// Reload recipe with associations
	h.db.Preload("MealTimes").First(&recipe, recipe.ID)

	c.JSON(http.StatusOK, recipe)
}

func (h *AdminHandler) DeleteRecipe(c *gin.Context) {
	id := c.Param("id")

	// First, get the recipe to ensure it exists
	var recipe models.Recipe
	if err := h.db.First(&recipe, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	// Clear all associations before deleting the recipe
	// 1. Clear MealTimes association (recipe_meal_times table)
	if err := h.db.Model(&recipe).Association("MealTimes").Clear(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear meal times association"})
		return
	}

	// 2. Clear any task associations (meal_recipes table - many-to-many)
	// This removes the recipe from any scheduled tasks
	if err := h.db.Exec("DELETE FROM meal_recipes WHERE recipe_id = ?", id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear task associations"})
		return
	}

	// 3. Clear deprecated RecipeID foreign key in schedule_tasks
	// Set RecipeID to NULL for any tasks that reference this recipe
	if err := h.db.Exec("UPDATE schedule_tasks SET recipe_id = NULL WHERE recipe_id = ?", id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear schedule task references"})
		return
	}

	// 4. Delete any comments associated with this recipe
	if err := h.db.Where("recipe_id = ?", id).Delete(&models.RecipeComment{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete recipe comments"})
		return
	}

	// Now delete the recipe itself
	if err := h.db.Delete(&recipe).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Recipe deleted"})
}

// UploadRecipeImage handles image file upload for recipes
func (h *AdminHandler) UploadRecipeImage(c *gin.Context) {
	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}

	// Validate file type
	ext := filepath.Ext(file.Filename)
	allowedExts := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}
	if !allowedExts[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type. Allowed: jpg, jpeg, png, gif, webp"})
		return
	}

	// Create uploads directory if it doesn't exist
	uploadsDir := "web/static/uploads/recipes"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload directory"})
		return
	}

	// Generate unique filename
	filename := fmt.Sprintf("%d_%s", time.Now().Unix(), filepath.Base(file.Filename))
	filePath := filepath.Join(uploadsDir, filename)

	// Save file
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer src.Close()

	dst, err := os.Create(filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	// Return URL path
	imageURL := fmt.Sprintf("/static/uploads/recipes/%s", filename)
	c.JSON(http.StatusOK, gin.H{"url": imageURL})
}

// MealTime handlers

func (h *AdminHandler) GetMealTimes(c *gin.Context) {
	var mealTimes []models.MealTime
	if err := h.db.Order("default_time").Find(&mealTimes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, mealTimes)
}

func (h *AdminHandler) GetMealTime(c *gin.Context) {
	id := c.Param("id")
	var mealTime models.MealTime

	if err := h.db.First(&mealTime, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Meal time not found"})
		return
	}

	c.JSON(http.StatusOK, mealTime)
}

func (h *AdminHandler) CreateMealTime(c *gin.Context) {
	var mealTime models.MealTime
	if err := c.ShouldBindJSON(&mealTime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := h.db.Create(&mealTime).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusCreated, mealTime)
}

func (h *AdminHandler) UpdateMealTime(c *gin.Context) {
	id := c.Param("id")
	var mealTime models.MealTime
	
	if err := h.db.First(&mealTime, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Meal time not found"})
		return
	}
	
	if err := c.ShouldBindJSON(&mealTime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := h.db.Save(&mealTime).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, mealTime)
}

func (h *AdminHandler) DeleteMealTime(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.Delete(&models.MealTime{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Meal time deleted"})
}

// CleaningZone handlers

func (h *AdminHandler) GetCleaningZones(c *gin.Context) {
	var zones []models.CleaningZone
	if err := h.db.Order("priority DESC").Find(&zones).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, zones)
}

func (h *AdminHandler) GetCleaningZone(c *gin.Context) {
	id := c.Param("id")
	var zone models.CleaningZone

	if err := h.db.First(&zone, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Cleaning zone not found"})
		return
	}

	c.JSON(http.StatusOK, zone)
}

func (h *AdminHandler) CreateCleaningZone(c *gin.Context) {
	var zone models.CleaningZone
	if err := c.ShouldBindJSON(&zone); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := h.db.Create(&zone).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusCreated, zone)
}

func (h *AdminHandler) UpdateCleaningZone(c *gin.Context) {
	id := c.Param("id")
	var zone models.CleaningZone
	
	if err := h.db.First(&zone, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Cleaning zone not found"})
		return
	}
	
	if err := c.ShouldBindJSON(&zone); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := h.db.Save(&zone).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, zone)
}

func (h *AdminHandler) DeleteCleaningZone(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.Delete(&models.CleaningZone{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Cleaning zone deleted"})
}

// ChildcareSchedule handlers

func (h *AdminHandler) GetChildcareSchedules(c *gin.Context) {
	var schedules []models.ChildcareSchedule
	
	// Get schedules for the next 30 days
	startDate := time.Now()
	endDate := startDate.AddDate(0, 0, 30)
	
	if err := h.db.Where("date BETWEEN ? AND ?", startDate, endDate).
		Order("date, start_time").Find(&schedules).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, schedules)
}

func (h *AdminHandler) CreateChildcareSchedule(c *gin.Context) {
	var schedule models.ChildcareSchedule
	if err := c.ShouldBindJSON(&schedule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := h.db.Create(&schedule).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusCreated, schedule)
}

func (h *AdminHandler) UpdateChildcareSchedule(c *gin.Context) {
	id := c.Param("id")
	var schedule models.ChildcareSchedule
	
	if err := h.db.First(&schedule, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Childcare schedule not found"})
		return
	}
	
	if err := c.ShouldBindJSON(&schedule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := h.db.Save(&schedule).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, schedule)
}

func (h *AdminHandler) DeleteChildcareSchedule(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.Delete(&models.ChildcareSchedule{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Childcare schedule deleted"})
}

// Schedule management

func (h *AdminHandler) RegenerateSchedule(c *gin.Context) {
	daysStr := c.DefaultQuery("days", "7")
	days, err := strconv.Atoi(daysStr)
	if err != nil {
		days = 7
	}

	// This will be called from the main server with scheduler instance
	c.JSON(http.StatusOK, gin.H{"message": "Schedule regeneration triggered", "days": days})
}

// Recipe Comments handlers

func (h *AdminHandler) GetRecipeComments(c *gin.Context) {
	recipeID := c.Param("id")
	var comments []models.RecipeComment
	if err := h.db.Where("recipe_id = ?", recipeID).Order("created_at DESC").Find(&comments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, comments)
}

func (h *AdminHandler) CreateRecipeComment(c *gin.Context) {
	recipeID := c.Param("id")
	var input struct {
		Comment string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	recipeIDInt, err := strconv.ParseUint(recipeID, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid recipe ID"})
		return
	}

	comment := models.RecipeComment{
		RecipeID: uint(recipeIDInt),
		Comment:  input.Comment,
	}

	if err := h.db.Create(&comment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, comment)
}

func (h *AdminHandler) DeleteRecipeComment(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.Delete(&models.RecipeComment{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Comment deleted"})
}

// Task Recipe Management handlers

// GetTask returns a single task with its recipes and zones
func (h *AdminHandler) GetTask(c *gin.Context) {
	id := c.Param("id")
	var task models.ScheduleTask

	if err := h.db.Preload("Recipes").Preload("Recipe").Preload("Zone").Preload("Zones").First(&task, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	c.JSON(http.StatusOK, task)
}

// AddRecipeToTask adds a recipe to a meal task
func (h *AdminHandler) AddRecipeToTask(c *gin.Context) {
	taskID := c.Param("id")

	var input struct {
		RecipeID uint `json:"recipe_id"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get the task
	var task models.ScheduleTask
	if err := h.db.Preload("Recipes").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Verify it's a meal task
	if task.TaskType != "meal" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Can only add recipes to meal tasks"})
		return
	}

	// Get the recipe
	var recipe models.Recipe
	if err := h.db.First(&recipe, input.RecipeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	// Add recipe to task using Association
	if err := h.db.Model(&task).Association("Recipes").Append(&recipe); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Reload task with recipes
	if err := h.db.Preload("Recipes").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, task)
}

// RemoveRecipeFromTask removes a recipe from a meal task
func (h *AdminHandler) RemoveRecipeFromTask(c *gin.Context) {
	taskID := c.Param("id")
	recipeID := c.Param("recipe_id")

	// Get the task
	var task models.ScheduleTask
	if err := h.db.Preload("Recipes").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Get the recipe
	var recipe models.Recipe
	if err := h.db.First(&recipe, recipeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	// Remove recipe from task
	if err := h.db.Model(&task).Association("Recipes").Delete(&recipe); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Reload task with recipes
	if err := h.db.Preload("Recipes").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, task)
}

// AddZoneToTask adds a cleaning zone to a cleaning task
func (h *AdminHandler) AddZoneToTask(c *gin.Context) {
	taskID := c.Param("id")

	var input struct {
		ZoneID uint `json:"zone_id"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get the task
	var task models.ScheduleTask
	if err := h.db.Preload("Zones").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Verify it's a cleaning task
	if task.TaskType != "cleaning" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Can only add zones to cleaning tasks"})
		return
	}

	// Get the zone
	var zone models.CleaningZone
	if err := h.db.First(&zone, input.ZoneID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Zone not found"})
		return
	}

	// Add zone to task using Association
	if err := h.db.Model(&task).Association("Zones").Append(&zone); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Reload task with zones
	if err := h.db.Preload("Zones").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, task)
}

// RemoveZoneFromTask removes a cleaning zone from a cleaning task
func (h *AdminHandler) RemoveZoneFromTask(c *gin.Context) {
	taskID := c.Param("id")
	zoneID := c.Param("zone_id")

	// Get the task
	var task models.ScheduleTask
	if err := h.db.Preload("Zones").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Get the zone
	var zone models.CleaningZone
	if err := h.db.First(&zone, zoneID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Zone not found"})
		return
	}

	// Remove zone from task
	if err := h.db.Model(&task).Association("Zones").Delete(&zone); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Reload task with zones
	if err := h.db.Preload("Zones").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, task)
}

