package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/menu"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/notify"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AdminHandler struct {
	db *gorm.DB
	// FillDays дособирает ближайшие дни дома (новая зона уборки сразу попадает в расписание).
	FillDays func(orgID uint)
	// RefreshCleaning пересобирает уборку ближайших дней (зону изменили или удалили).
	RefreshCleaning func(orgID uint)
	// RefreshMeals обновляет еду ближайших дней (приём пищи добавили, изменили или удалили).
	RefreshMeals func(orgID uint)
}

func NewAdminHandler(db *gorm.DB) *AdminHandler {
	return &AdminHandler{db: db}
}

// orgDB возвращает DB-сессию, скоупленную по organization_id из контекста.
func (h *AdminHandler) orgDB(c *gin.Context) *gorm.DB {
	m := middleware.MustMembership(c)
	return h.db.Where("organization_id = ?", m.OrganizationID)
}

// orgID извлекает OrganizationID из контекста.
func (h *AdminHandler) orgID(c *gin.Context) uint {
	return middleware.MustMembership(c).OrganizationID
}

// Recipe handlers

func (h *AdminHandler) GetRecipes(c *gin.Context) {
	var recipes []models.Recipe
	if err := h.orgDB(c).Preload("MealTimes").Order("created_at DESC").Find(&recipes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, recipes)
}

func (h *AdminHandler) GetRecipe(c *gin.Context) {
	id := c.Param("id")
	var recipe models.Recipe
	if err := h.orgDB(c).Preload("MealTimes").First(&recipe, id).Error; err != nil {
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
	recipe.OrganizationID = h.orgID(c)
	if !h.validDishType(c, recipe.DishTypeID) {
		return
	}
	if recipe.DishTypeID != nil && *recipe.DishTypeID == 0 {
		recipe.DishTypeID = nil
	}

	// Create the recipe first
	if err := h.db.Create(&recipe).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Associate meal times if provided
	if len(input.MealTimeIDs) > 0 {
		var mealTimes []models.MealTime
		if err := h.orgDB(c).Find(&mealTimes, input.MealTimeIDs).Error; err != nil {
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

	if err := h.orgDB(c).First(&recipe, id).Error; err != nil {
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

	// Текст рецепта правит любой участник (CapEditRecipes): кто готовит, тот и уточняет состав и способ.
	recipe.Name = input.Name
	recipe.Description = input.Description
	recipe.Ingredients = input.Ingredients
	recipe.Instructions = input.Instructions
	recipe.Tags = input.Tags
	recipe.VideoURL = input.VideoURL
	recipe.Rating = input.Rating
	recipe.PrepTime = input.PrepTime
	recipe.CookTime = input.CookTime
	recipe.Servings = input.Servings

	// Для кого блюдо, фото, «готовим сейчас» и приёмы пищи влияют на меню — только с CapManageRecipes.
	m := middleware.MustMembership(c)
	canManage := middleware.Can(m, middleware.CapManageRecipes)
	if canManage {
		recipe.FamilyMember = input.FamilyMember
		recipe.ImageURL = input.ImageURL
		recipe.IsActive = input.IsActive
		// Тип блюда — только если прислали (старые клиенты поле не шлют — тип не затираем).
		if input.DishTypeID != nil {
			if !h.validDishType(c, input.DishTypeID) {
				return
			}
			recipe.DishTypeID = input.DishTypeID
			if *input.DishTypeID == 0 {
				recipe.DishTypeID = nil
			}
		}
	}

	var editor models.User
	if err := h.db.Select("id", "name").First(&editor, m.UserID).Error; err == nil {
		recipe.UpdatedByID = &editor.ID
		recipe.UpdatedByName = editor.Name
	}

	if err := h.db.Save(&recipe).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update meal time associations
	if canManage && input.MealTimeIDs != nil {
		var mealTimes []models.MealTime
		if len(input.MealTimeIDs) > 0 {
			if err := h.orgDB(c).Find(&mealTimes, input.MealTimeIDs).Error; err != nil {
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
	if err := h.orgDB(c).First(&recipe, id).Error; err != nil {
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

	// Замены блюд на день, где участвовал рецепт («Вернуть» к удалённому рецепту невозможно).
	if err := h.db.Exec("DELETE FROM meal_swaps WHERE original_recipe_id = ? OR current_recipe_id = ?", id, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear meal swaps"})
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

// MealTime handlers

func (h *AdminHandler) GetMealTimes(c *gin.Context) {
	var mealTimes []models.MealTime
	if err := h.orgDB(c).Preload("Recipes").Order("default_time").Find(&mealTimes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, mealTimes)
}

func (h *AdminHandler) GetMealTime(c *gin.Context) {
	id := c.Param("id")
	var mealTime models.MealTime

	if err := h.orgDB(c).Preload("Recipes").First(&mealTime, id).Error; err != nil {
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
	mealTime.ID = 0
	mealTime.OrganizationID = h.orgID(c)

	if err := h.db.Create(&mealTime).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if h.RefreshMeals != nil {
		h.RefreshMeals(h.orgID(c))
	}
	c.JSON(http.StatusCreated, mealTime)
}

func (h *AdminHandler) UpdateMealTime(c *gin.Context) {
	id := c.Param("id")
	var mealTime models.MealTime

	if err := h.orgDB(c).First(&mealTime, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Meal time not found"})
		return
	}

	var input struct {
		models.MealTime
		RecipeIDs []uint `json:"recipe_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	input.MealTime.ID = mealTime.ID
	input.MealTime.OrganizationID = mealTime.OrganizationID
	if err := h.db.Save(&input.MealTime).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update recipe associations
	var recipes []models.Recipe
	if len(input.RecipeIDs) > 0 {
		if err := h.orgDB(c).Find(&recipes, input.RecipeIDs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find recipes"})
			return
		}
	}
	if err := h.db.Model(&input.MealTime).Association("Recipes").Replace(recipes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update recipes"})
		return
	}

	h.db.Preload("Recipes").First(&input.MealTime, input.MealTime.ID)
	if h.RefreshMeals != nil {
		h.RefreshMeals(h.orgID(c))
	}
	c.JSON(http.StatusOK, input.MealTime)
}

func (h *AdminHandler) DeleteMealTime(c *gin.Context) {
	id := c.Param("id")
	res := h.orgDB(c).Delete(&models.MealTime{}, id)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Meal time not found"})
		return
	}
	if h.RefreshMeals != nil {
		h.RefreshMeals(h.orgID(c))
	}
	c.JSON(http.StatusOK, gin.H{"message": "Meal time deleted"})
}

// CleaningZone handlers

func (h *AdminHandler) GetCleaningZones(c *gin.Context) {
	var zones []models.CleaningZone
	if err := h.orgDB(c).Order("priority DESC").Find(&zones).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// days — в какие дни недели (ISO 1..7) зона попадёт в расписание; workdays — рабочие дни дома.
	// Фронт показывает их как есть, а не считает сам.
	workdays := calendar.OrgWorkdays(h.db, h.orgID(c))
	type zoneView struct {
		models.CleaningZone
		Days     []int `json:"days"`
		Workdays []int `json:"workdays"`
	}
	out := make([]zoneView, len(zones))
	for i, z := range zones {
		out[i] = zoneView{z, calendar.CleaningDays(z.FrequencyPerWeek, z.ID, workdays), workdays}
	}
	c.JSON(http.StatusOK, out)
}

func (h *AdminHandler) GetCleaningZone(c *gin.Context) {
	var zone models.CleaningZone
	if err := h.orgDB(c).First(&zone, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Cleaning zone not found"})
		return
	}
	c.JSON(http.StatusOK, zone)
}

// zoneInput — что можно задать у зоны уборки (организация и id — только с сервера).
type zoneInput struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	FrequencyPerWeek int    `json:"frequency_per_week"`
	Priority         string `json:"priority"`
}

func (in *zoneInput) validate() string {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "name is required"
	}
	if in.FrequencyPerWeek < 1 || in.FrequencyPerWeek > 7 {
		return "frequency_per_week must be 1..7"
	}
	switch in.Priority {
	case "":
		in.Priority = "medium"
	case "high", "medium", "low":
	default:
		return "priority must be high|medium|low"
	}
	return ""
}

func (h *AdminHandler) CreateCleaningZone(c *gin.Context) {
	var in zoneInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	zone := models.CleaningZone{
		OrganizationID:   h.orgID(c),
		Name:             in.Name,
		Description:      in.Description,
		FrequencyPerWeek: in.FrequencyPerWeek,
		Priority:         in.Priority,
	}
	if err := h.db.Create(&zone).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.FillDays != nil {
		h.FillDays(zone.OrganizationID)
	}
	c.JSON(http.StatusCreated, zone)
}

func (h *AdminHandler) UpdateCleaningZone(c *gin.Context) {
	var zone models.CleaningZone
	if err := h.orgDB(c).First(&zone, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Cleaning zone not found"})
		return
	}
	var in zoneInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	zone.Name, zone.Description, zone.FrequencyPerWeek, zone.Priority = in.Name, in.Description, in.FrequencyPerWeek, in.Priority
	if err := h.db.Save(&zone).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.RefreshCleaning != nil {
		h.RefreshCleaning(zone.OrganizationID)
	}
	c.JSON(http.StatusOK, zone)
}

func (h *AdminHandler) DeleteCleaningZone(c *gin.Context) {
	var zone models.CleaningZone
	if err := h.orgDB(c).First(&zone, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Cleaning zone not found"})
		return
	}
	id := zone.ID

	// Clear many2many references in task_zones
	if err := h.db.Exec("DELETE FROM task_zones WHERE cleaning_zone_id = ?", id).Error; err != nil {
		log.Printf("Warning: failed to clear task_zones for zone %d: %v", id, err)
	}
	// Clear deprecated zone_id FK in schedule_tasks
	if err := h.db.Exec("UPDATE schedule_tasks SET zone_id = NULL WHERE zone_id = ?", id).Error; err != nil {
		log.Printf("Warning: failed to clear zone_id FK for zone %d: %v", id, err)
	}

	if err := h.db.Delete(&models.CleaningZone{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.RefreshCleaning != nil {
		h.RefreshCleaning(h.orgID(c))
	}
	c.JSON(http.StatusOK, gin.H{"message": "Cleaning zone deleted"})
}

// ChildcareSchedule handlers

func (h *AdminHandler) GetChildcareSchedules(c *gin.Context) {
	var schedules []models.ChildcareSchedule

	// Normalize to UTC midnight so today's entries are always included
	startDate := clock.OrgToday(h.db, h.orgID(c))
	endDate := startDate.AddDate(0, 0, 60)

	if err := h.orgDB(c).Where("date >= ? AND date < ?", startDate, endDate).
		Order("date, start_time").Find(&schedules).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, schedules)
}

func (h *AdminHandler) GetChildcareSchedule(c *gin.Context) {
	id := c.Param("id")
	var schedule models.ChildcareSchedule

	if err := h.orgDB(c).First(&schedule, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Childcare schedule not found"})
		return
	}

	c.JSON(http.StatusOK, schedule)
}

func (h *AdminHandler) CreateChildcareSchedule(c *gin.Context) {
	var schedule models.ChildcareSchedule
	if err := c.ShouldBindJSON(&schedule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	schedule.ID = 0
	schedule.OrganizationID = h.orgID(c)

	if err := h.db.Create(&schedule).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, schedule)
}

func (h *AdminHandler) UpdateChildcareSchedule(c *gin.Context) {
	id := c.Param("id")
	var schedule models.ChildcareSchedule

	if err := h.orgDB(c).First(&schedule, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Childcare schedule not found"})
		return
	}
	keepID, keepOrg := schedule.ID, schedule.OrganizationID
	if err := c.ShouldBindJSON(&schedule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	schedule.ID, schedule.OrganizationID = keepID, keepOrg

	if err := h.db.Save(&schedule).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, schedule)
}

func (h *AdminHandler) DeleteChildcareSchedule(c *gin.Context) {
	id := c.Param("id")
	res := h.orgDB(c).Delete(&models.ChildcareSchedule{}, id)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Childcare schedule not found"})
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
	var n int64
	h.orgDB(c).Model(&models.Recipe{}).Where("id = ?", recipeID).Count(&n)
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}
	var comments []models.RecipeComment
	if err := h.orgDB(c).Where("recipe_id = ?", recipeID).Order("created_at DESC").Find(&comments).Error; err != nil {
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

	var recipe models.Recipe
	if err := h.orgDB(c).First(&recipe, recipeIDInt).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}
	comment := models.RecipeComment{
		OrganizationID: h.orgID(c),
		RecipeID:       uint(recipeIDInt),
		Comment:        input.Comment,
	}

	if err := h.db.Create(&comment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, comment)
}

func (h *AdminHandler) DeleteRecipeComment(c *gin.Context) {
	id := c.Param("id")
	res := h.orgDB(c).Delete(&models.RecipeComment{}, id)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Comment deleted"})
}

// Task handlers

// UpdateTask updates a schedule task fields and its recipe associations.
func (h *AdminHandler) UpdateTask(c *gin.Context) {
	id := c.Param("id")
	var task models.ScheduleTask
	if err := h.orgDB(c).Preload("Recipes").First(&task, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	var input struct {
		Time             string  `json:"time"`
		EndTime          string  `json:"end_time"`
		Title            string  `json:"title"`
		Description      string  `json:"description"`
		RecipeIDs        []uint  `json:"recipe_ids"`
		TaskCategoryID   *uint   `json:"task_category_id"`
		AssignedToUserID *uint   `json:"assigned_to_user_id"`
		Priority         *string `json:"priority"`
		// Date — перенести разовую задачу на другой день (YYYY-MM-DD).
		Date *string `json:"date"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !checkTaskRefs(c, h.db, h.orgID(c), input.TaskCategoryID, input.AssignedToUserID) {
		return
	}
	if input.Date != nil && *input.Date != "" {
		if task.TaskType != "custom" || task.RecurringTaskID != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "only one-off tasks can be moved", "code": "not_movable"})
			return
		}
		d, err := time.Parse("2006-01-02", *input.Date)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid date, use YYYY-MM-DD"})
			return
		}
		day, err := models.EnsureDailySchedule(h.db, h.orgID(c), clock.Day(d), false)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create schedule"})
			return
		}
		task.ScheduleID = day.ID
	}
	before := task
	if input.Priority != nil {
		task.Priority = *input.Priority
	}
	task.Time = input.Time
	task.EndTime = input.EndTime
	task.Title = input.Title
	task.Description = input.Description
	task.TaskCategoryID = input.TaskCategoryID
	task.AssignedToUserID = input.AssignedToUserID

	if err := h.db.Save(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Replace recipe associations (many2many)
	var recipes []models.Recipe
	if len(input.RecipeIDs) > 0 {
		if err := h.orgDB(c).Find(&recipes, input.RecipeIDs).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find recipes"})
			return
		}
	}
	oldRecipes := map[uint]bool{}
	for _, r := range task.Recipes {
		oldRecipes[r.ID] = true
	}
	if err := h.db.Model(&task).Association("Recipes").Replace(recipes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update recipes"})
		return
	}
	// Блюдо заменили — открытые списки «под меню» на этот день обновляются сами.
	if task.TaskType == "meal" {
		kept := map[uint]bool{}
		for _, r := range recipes {
			kept[r.ID] = true
			if !oldRecipes[r.ID] {
				if err := menu.RecipeAdded(h.db, task, r); err != nil {
					log.Printf("shopping from menu: add recipe %d to task %d: %v", r.ID, task.ID, err)
				}
			}
		}
		for id := range oldRecipes {
			if !kept[id] {
				if err := menu.RecipeRemoved(h.db, task, id); err != nil {
					log.Printf("shopping from menu: remove recipe %d from task %d: %v", id, task.ID, err)
				}
			}
		}
	}

	// Also update the legacy RecipeID FK: set to first recipe if any
	if len(recipes) > 0 {
		task.RecipeID = &recipes[0].ID
	} else {
		task.RecipeID = nil
	}
	h.db.Save(&task)

	h.db.Preload("Recipes").Preload("Recipe").First(&task, id)
	notify.TaskUpdated(h.db, currentUserID(c), &before, &task)
	c.JSON(http.StatusOK, task)
}

// GetTask returns a single task with its recipes and zones
func (h *AdminHandler) GetTask(c *gin.Context) {
	id := c.Param("id")
	var task models.ScheduleTask

	if err := h.orgDB(c).Preload("Recipes").Preload("Recipe").Preload("Zone").Preload("Zones").First(&task, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}
	if hiddenFromMember(c, &task) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}
	list := []models.ScheduleTask{task}
	models.FillMealInfo(h.db, list)
	c.JSON(http.StatusOK, list[0])
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
	if err := h.orgDB(c).Preload("Recipes").First(&task, taskID).Error; err != nil {
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
	if err := h.orgDB(c).First(&recipe, input.RecipeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	// Add recipe to task using Association; продукты нового блюда сами дописываются
	// в открытые списки «под меню» на этот день.
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&task).Association("Recipes").Append(&recipe); err != nil {
			return err
		}
		return menu.RecipeAdded(tx, task, recipe)
	}); err != nil {
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
	if err := h.orgDB(c).Preload("Recipes").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Get the recipe
	var recipe models.Recipe
	if err := h.orgDB(c).First(&recipe, recipeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}

	// Remove recipe from task; в списках «под меню» продукты этого блюда помечаются «можно убрать».
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&task).Association("Recipes").Delete(&recipe); err != nil {
			return err
		}
		return menu.RecipeRemoved(tx, task, recipe.ID)
	}); err != nil {
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
	if err := h.orgDB(c).Preload("Zones").First(&task, taskID).Error; err != nil {
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
	if err := h.orgDB(c).First(&zone, input.ZoneID).Error; err != nil {
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
	if err := h.orgDB(c).Preload("Zones").First(&task, taskID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Get the zone
	var zone models.CleaningZone
	if err := h.orgDB(c).First(&zone, zoneID).Error; err != nil {
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

// ── Custom Tasks ──────────────────────────────────────────────────────────────

// CreateCustomTask adds a one-off task to a specific date.
// Custom tasks survive schedule regeneration.
func (h *AdminHandler) CreateCustomTask(c *gin.Context) {
	var input struct {
		Date             string `json:"date"`
		Time             string `json:"time"`
		EndTime          string `json:"end_time"`
		Priority         string `json:"priority"`
		Title            string `json:"title"`
		Description      string `json:"description"`
		TaskCategoryID   *uint  `json:"task_category_id"`
		AssignedToUserID *uint  `json:"assigned_to_user_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Title == "" || input.Date == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "date and title are required"})
		return
	}

	date, err := time.Parse("2006-01-02", input.Date)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid date format, use YYYY-MM-DD"})
		return
	}
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	if !checkTaskRefs(c, h.db, h.orgID(c), input.TaskCategoryID, input.AssignedToUserID) {
		return
	}

	// Find or create daily_schedule for this date
	schedule, err := models.EnsureDailySchedule(h.db, h.orgID(c), date, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create schedule"})
		return
	}

	task := models.ScheduleTask{
		ScheduleID:       schedule.ID,
		OrganizationID:   h.orgID(c),
		TaskType:         "custom",
		Time:             input.Time,
		EndTime:          input.EndTime,
		Priority:         input.Priority,
		Title:            input.Title,
		Description:      input.Description,
		TaskCategoryID:   input.TaskCategoryID,
		AssignedToUserID: input.AssignedToUserID,
		Completed:        false,
	}
	if err := h.db.Create(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	notify.TaskCreated(h.db, currentUserID(c), &task)
	c.JSON(http.StatusCreated, task)
}

// DeleteCustomTask removes a custom task by ID.
func (h *AdminHandler) DeleteCustomTask(c *gin.Context) {
	id := c.Param("id")
	var task models.ScheduleTask
	if err := h.orgDB(c).First(&task, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}
	if task.TaskType != "custom" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only custom tasks can be deleted this way"})
		return
	}
	// Повторяющаяся: запоминаем день, чтобы задача не появилась снова.
	if task.RecurringTaskID != nil {
		var sched models.DailySchedule
		if h.db.First(&sched, task.ScheduleID).Error == nil {
			h.db.Create(&models.RecurringTaskSkip{RecurringTaskID: *task.RecurringTaskID, Date: utcDay(sched.Date)})
		}
	}
	notify.TaskDeleted(h.db, currentUserID(c), &task)
	if err := h.db.Delete(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Custom task deleted"})
}

// validDishType — тип блюда этого дома (nil и 0 — без типа).
func (h *AdminHandler) validDishType(c *gin.Context, id *uint) bool {
	if id == nil || *id == 0 {
		return true
	}
	var n int64
	h.db.Model(&models.DishType{}).Where("id = ? AND organization_id = ?", *id, h.orgID(c)).Count(&n)
	if n == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown dish_type_id", "code": "bad_dish_type"})
		return false
	}
	return true
}
