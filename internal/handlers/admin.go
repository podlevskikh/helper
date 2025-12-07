package handlers

import (
	"net/http"
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
	if err := h.db.Order("created_at DESC").Find(&recipes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, recipes)
}

func (h *AdminHandler) GetRecipe(c *gin.Context) {
	id := c.Param("id")
	var recipe models.Recipe
	if err := h.db.First(&recipe, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}
	c.JSON(http.StatusOK, recipe)
}

func (h *AdminHandler) CreateRecipe(c *gin.Context) {
	var recipe models.Recipe
	if err := c.ShouldBindJSON(&recipe); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := h.db.Create(&recipe).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusCreated, recipe)
}

func (h *AdminHandler) UpdateRecipe(c *gin.Context) {
	id := c.Param("id")
	var recipe models.Recipe
	
	if err := h.db.First(&recipe, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}
	
	if err := c.ShouldBindJSON(&recipe); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := h.db.Save(&recipe).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, recipe)
}

func (h *AdminHandler) DeleteRecipe(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.Delete(&models.Recipe{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Recipe deleted"})
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

