package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/menu"
	"podlevskikh/awesomeProject/internal/models"
)

// Еда в дне: порции на один день, замена блюда и «Вернуть». Меняет тот, у кого manage_schedule.
// Открытые списки покупок «под меню» на этот день пересчитываются в той же транзакции.

// mealTask — задача еды этого дома с блюдами (и старым полем recipe_id).
func (h *AdminHandler) mealTask(c *gin.Context) (*models.ScheduleTask, bool) {
	var task models.ScheduleTask
	if err := h.orgDB(c).Preload("Recipes").Preload("Recipe").First(&task, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return nil, false
	}
	if task.TaskType != "meal" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only meal tasks", "code": "not_meal"})
		return nil, false
	}
	return &task, true
}

// respondMealTask — задача целиком, с порциями «обычно» и заменами.
func (h *AdminHandler) respondMealTask(c *gin.Context, id uint) {
	var task models.ScheduleTask
	if err := h.db.Preload("Recipes").Preload("Recipe").First(&task, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	list := []models.ScheduleTask{task}
	models.FillMealInfo(h.db, list)
	c.JSON(http.StatusOK, list[0])
}

// SetTaskServings — PUT /admin/api/tasks/:id/servings {servings}: сколько порций готовить в этот день.
// 0 — «как обычно».
func (h *AdminHandler) SetTaskServings(c *gin.Context) {
	task, ok := h.mealTask(c) // сначала задача: чужая — 404, а не ошибка ввода
	if !ok {
		return
	}
	var input struct {
		Servings *int `json:"servings"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Servings == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "servings is required"})
		return
	}
	n := *input.Servings
	if n < 0 || n > models.MaxMealServings {
		c.JSON(http.StatusBadRequest, gin.H{"error": "servings must be 0..20", "code": "bad_servings"})
		return
	}
	// «Как обычно» у задачи «Ритма дома» — стандартные порции регулярной задачи.
	if n == 0 && task.RoutineID != nil {
		var dishes []models.RoutineDish
		h.db.Where("routine_id = ?", *task.RoutineID).Find(&dishes)
		if len(dishes) > 0 {
			n = models.StandardServings(dishes)
		}
	}
	old := task.Servings
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.ScheduleTask{}).Where("id = ?", task.ID).Update("servings", n).Error; err != nil {
			return err
		}
		return menu.ServingsChanged(tx, *task, old, n)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.respondMealTask(c, task.ID)
}

// hasDish — блюдо входит в задачу (в meal_recipes или, если их нет, в старом recipe_id).
func hasDish(task *models.ScheduleTask, recipeID uint) bool {
	for _, r := range menu.TaskRecipes(*task) {
		if r.ID == recipeID {
			return true
		}
	}
	return false
}

// moveLegacyDish — блюдо, выбранное планировщиком, лежит только в recipe_id; переносим его в
// meal_recipes, чтобы дальше менять блюда одним способом.
func moveLegacyDish(tx *gorm.DB, task *models.ScheduleTask) error {
	if len(task.Recipes) > 0 || task.Recipe == nil {
		return nil
	}
	if err := tx.Exec("INSERT INTO meal_recipes (schedule_task_id, recipe_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
		task.ID, task.Recipe.ID).Error; err != nil {
		return err
	}
	task.Recipes = []models.Recipe{*task.Recipe}
	return nil
}

// replaceDish — убрать блюдо from, поставить to; recipe_id указывает на первое блюдо, как в UpdateTask.
func replaceDish(tx *gorm.DB, task *models.ScheduleTask, from uint, to models.Recipe) error {
	if err := moveLegacyDish(tx, task); err != nil {
		return err
	}
	if err := tx.Exec("DELETE FROM meal_recipes WHERE schedule_task_id = ? AND recipe_id = ?", task.ID, from).Error; err != nil {
		return err
	}
	// Связь пишем напрямую: Association().Append сохранил бы и прежние блюда из task.Recipes.
	if err := tx.Exec("INSERT INTO meal_recipes (schedule_task_id, recipe_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
		task.ID, to.ID).Error; err != nil {
		return err
	}
	// Блюдо «Ритма дома» на своём номере — тот же номер и порции, другой рецепт.
	if err := tx.Model(&models.TaskDish{}).Where("task_id = ? AND recipe_id = ?", task.ID, from).
		Update("recipe_id", to.ID).Error; err != nil {
		return err
	}
	kept := []models.Recipe{to}
	for _, r := range task.Recipes {
		if r.ID != from {
			kept = append(kept, r)
		}
	}
	task.Recipes = kept
	if task.RecipeID != nil && *task.RecipeID == from {
		if err := tx.Model(&models.ScheduleTask{}).Where("id = ?", task.ID).Update("recipe_id", to.ID).Error; err != nil {
			return err
		}
	}
	return menu.RecipeRemoved(tx, *task, from)
}

// SwapTaskRecipe — POST /admin/api/tasks/:id/swap {from_recipe_id, to_recipe_id}: заменить блюдо на этот день.
func (h *AdminHandler) SwapTaskRecipe(c *gin.Context) {
	task, ok := h.mealTask(c)
	if !ok {
		return
	}
	var input struct {
		From uint `json:"from_recipe_id"`
		To   uint `json:"to_recipe_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.From == 0 || input.To == 0 || input.From == input.To {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from_recipe_id and to_recipe_id are required and must differ"})
		return
	}
	if !hasDish(task, input.From) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the dish is not in this meal", "code": "not_in_meal"})
		return
	}
	if hasDish(task, input.To) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the dish is already in this meal", "code": "already_in_meal"})
		return
	}
	var to models.Recipe
	if err := h.orgDB(c).First(&to, input.To).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recipe not found"})
		return
	}
	uid := currentUserID(c)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := replaceDish(tx, task, input.From, to); err != nil {
			return err
		}
		if err := menu.RecipeAdded(tx, *task, to); err != nil {
			return err
		}
		// Повторная замена: исходное блюдо дня остаётся прежним.
		var sw models.MealSwap
		err := tx.Where("task_id = ? AND current_recipe_id = ?", task.ID, input.From).First(&sw).Error
		if err == nil {
			return tx.Model(&sw).Update("current_recipe_id", to.ID).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		sw = models.MealSwap{OrganizationID: task.OrganizationID, TaskID: task.ID,
			OriginalRecipeID: input.From, CurrentRecipeID: to.ID}
		if uid != 0 {
			sw.CreatedByUserID = &uid
		}
		return tx.Create(&sw).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.respondMealTask(c, task.ID)
}

// UndoTaskSwap — POST /admin/api/tasks/:id/swap/undo {recipe_id}: вернуть исходное блюдо дня.
func (h *AdminHandler) UndoTaskSwap(c *gin.Context) {
	task, ok := h.mealTask(c)
	if !ok {
		return
	}
	var input struct {
		RecipeID uint `json:"recipe_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.RecipeID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "recipe_id is required"})
		return
	}
	var sw models.MealSwap
	if err := h.db.Where("task_id = ? AND current_recipe_id = ?", task.ID, input.RecipeID).First(&sw).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no swap for this dish", "code": "no_swap"})
		return
	}
	var original models.Recipe
	if err := h.orgDB(c).First(&original, sw.OriginalRecipeID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Original recipe not found", "code": "original_gone"})
		return
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if hasDish(task, original.ID) { // исходное блюдо уже стоит в задаче — только убираем замену
			if err := tx.Exec("DELETE FROM meal_recipes WHERE schedule_task_id = ? AND recipe_id = ?", task.ID, input.RecipeID).Error; err != nil {
				return err
			}
			if err := menu.RecipeRemoved(tx, *task, input.RecipeID); err != nil {
				return err
			}
		} else {
			if err := replaceDish(tx, task, input.RecipeID, original); err != nil {
				return err
			}
			if err := menu.RecipeRestored(tx, *task, original); err != nil {
				return err
			}
		}
		return tx.Delete(&sw).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.respondMealTask(c, task.ID)
}
