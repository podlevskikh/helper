package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/recipeimport"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Загрузка рецептов списком (docs/work/recipes-import/PRD.md во фронте). Вариант A — текст по шаблону.
// Один и тот же разбор и для предпросмотра (dry_run), и для сохранения: поля рецептов клиенту не доверяем,
// от клиента — только решения по каждому рецепту (загружать, заменить, тип блюда).

// UndoWindow — сколько после загрузки работает «Отменить загрузку».
const UndoWindow = 24 * time.Hour

type importDecision struct {
	Index       int    `json:"index"`
	Action      string `json:"action"` // create | replace | skip
	DishTypeID  *uint  `json:"dish_type_id"`
	NewDishType string `json:"new_dish_type"` // создать тип с таким названием (или взять, если уже есть)
}

type importInput struct {
	Text              string           `json:"text"`
	DefaultDishTypeID *uint            `json:"default_dish_type_id"`
	DryRun            bool             `json:"dry_run"`
	Decisions         []importDecision `json:"decisions"`
}

type idName struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type importPreviewItem struct {
	recipeimport.Item
	Status      string  `json:"status"` // new | duplicate | error
	DuplicateOf *idName `json:"duplicate_of"`
	DishType    *idName `json:"dish_type"`     // тип дома: из текста или «для всех без типа»
	NewDishType string  `json:"new_dish_type"` // в тексте тип, которого в доме нет — предложим создать
}

// importContext — разобранный текст и всё, что нужно знать о доме.
type importContext struct {
	items   []importPreviewItem
	byName  map[string]models.Recipe   // рецепты дома по нормализованному названию
	types   map[string]models.DishType // типы дома по нормализованному названию
	typeIDs map[uint]models.DishType
}

func (h *AdminHandler) loadImport(c *gin.Context, in importInput) (*importContext, bool) {
	if strings.TrimSpace(in.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text is empty", "code": "empty"})
		return nil, false
	}
	if len(in.Text) > recipeimport.MaxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "text is too large", "code": "too_large",
			"max_bytes": recipeimport.MaxBytes})
		return nil, false
	}
	items := recipeimport.Parse(in.Text)
	if len(items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no recipes found", "code": "no_recipes"})
		return nil, false
	}
	if len(items) > recipeimport.MaxRecipes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "too many recipes", "code": "too_many",
			"max": recipeimport.MaxRecipes, "found": len(items)})
		return nil, false
	}

	ctx := &importContext{byName: map[string]models.Recipe{}, types: map[string]models.DishType{}, typeIDs: map[uint]models.DishType{}}
	var recipes []models.Recipe
	if err := h.orgDB(c).Select("id", "name").Order("id").Find(&recipes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	for _, r := range recipes {
		k := recipeimport.NormalizeName(r.Name)
		if _, ok := ctx.byName[k]; !ok {
			ctx.byName[k] = r
		}
	}
	var types []models.DishType
	if err := h.orgDB(c).Order("position, id").Find(&types).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	for _, t := range types {
		ctx.types[recipeimport.NormalizeName(t.Name)] = t
		ctx.typeIDs[t.ID] = t
	}
	var def *idName
	if in.DefaultDishTypeID != nil && *in.DefaultDishTypeID != 0 {
		t, ok := ctx.typeIDs[*in.DefaultDishTypeID]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown dish_type_id", "code": "bad_dish_type"})
			return nil, false
		}
		def = &idName{t.ID, t.Name}
	}

	for _, it := range items {
		p := importPreviewItem{Item: it, Status: "new"}
		if r, ok := ctx.byName[recipeimport.NormalizeName(it.Name)]; ok && it.Name != "" {
			p.DuplicateOf = &idName{r.ID, r.Name}
			p.Status = "duplicate"
		}
		if !it.OK() {
			p.Status = "error"
		}
		if it.DishType != "" {
			if t, ok := ctx.types[recipeimport.NormalizeName(it.DishType)]; ok {
				p.DishType = &idName{t.ID, t.Name}
			} else {
				p.NewDishType = it.DishType
			}
		} else {
			p.DishType = def
		}
		ctx.items = append(ctx.items, p)
	}
	return ctx, true
}

// ImportRecipes — POST /admin/api/recipe-imports: с dry_run — предпросмотр, без — сохранить по решениям.
func (h *AdminHandler) ImportRecipes(c *gin.Context) {
	var in importInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, ok := h.loadImport(c, in)
	if !ok {
		return
	}
	if in.DryRun {
		sum := map[string]int{"new": 0, "duplicate": 0, "error": 0}
		for _, it := range ctx.items {
			sum[it.Status]++
		}
		c.JSON(http.StatusOK, gin.H{"items": ctx.items, "summary": sum, "max": recipeimport.MaxRecipes})
		return
	}
	if len(in.Decisions) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decisions are required", "code": "no_decisions"})
		return
	}
	dec := map[int]importDecision{}
	for _, d := range in.Decisions {
		if d.Index < 1 || d.Index > len(ctx.items) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "decision index out of range", "code": "bad_decision"})
			return
		}
		switch d.Action {
		case "create", "replace", "skip":
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action", "code": "bad_decision"})
			return
		}
		if d.DishTypeID != nil && *d.DishTypeID != 0 {
			if _, ok := ctx.typeIDs[*d.DishTypeID]; !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown dish_type_id", "code": "bad_dish_type"})
				return
			}
		}
		if len([]rune(strings.TrimSpace(d.NewDishType))) > 60 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "dish type name is too long", "code": "bad_dish_type"})
			return
		}
		dec[d.Index] = d
	}

	m := middleware.MustMembership(c)
	orgID := m.OrganizationID
	var user models.User
	h.db.Select("id", "name").First(&user, m.UserID)

	imp := models.RecipeImport{OrganizationID: orgID, UserID: m.UserID, UserName: user.Name, Source: "text"}
	var saved []idName
	errNothing := errors.New("nothing to save")
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&imp).Error; err != nil {
			return err
		}
		// Тип блюда для рецепта: выбранный в предпросмотре, новый по названию, из текста, «для всех без типа».
		typeFor := func(it importPreviewItem, d importDecision) (*uint, error) {
			if d.DishTypeID != nil && *d.DishTypeID != 0 {
				return d.DishTypeID, nil
			}
			name := strings.TrimSpace(d.NewDishType)
			if name == "" && d.DishTypeID == nil && it.DishType == nil {
				name = it.NewDishType
			}
			if name != "" {
				k := recipeimport.NormalizeName(name)
				if t, ok := ctx.types[k]; ok {
					return &t.ID, nil
				}
				var pos int
				tx.Model(&models.DishType{}).Where("organization_id = ?", orgID).Select("coalesce(max(position), 0)").Scan(&pos)
				t := models.DishType{OrganizationID: orgID, Name: name, Position: pos + 1}
				if err := tx.Create(&t).Error; err != nil {
					return nil, err
				}
				ctx.types[k] = t
				return &t.ID, nil
			}
			if d.DishTypeID == nil && it.DishType != nil {
				return &it.DishType.ID, nil
			}
			return nil, nil // явно «без типа» (dish_type_id: 0)
		}

		for _, it := range ctx.items {
			d, ok := dec[it.Index]
			if !ok || d.Action == "skip" || it.Status == "error" {
				imp.Skipped++
				continue
			}
			typeID, err := typeFor(it, d)
			if err != nil {
				return err
			}
			if it.DuplicateOf != nil {
				if d.Action != "replace" {
					imp.Skipped++ // дубль по умолчанию пропускаем
					continue
				}
				var r models.Recipe
				if err := tx.Where("organization_id = ?", orgID).First(&r, it.DuplicateOf.ID).Error; err != nil {
					return err
				}
				// Заменяем текст; оценка, «готовим сейчас», «для кого», заметки и ротации остаются.
				r.Description, r.Ingredients, r.Instructions = it.Description, it.Ingredients, it.Instructions
				r.PrepTime, r.CookTime, r.Servings, r.Tags = it.PrepTime, it.CookTime, it.Servings, it.Tags
				if it.VideoURL != "" {
					r.VideoURL = it.VideoURL
				}
				if it.SourceURL != "" {
					r.SourceURL = it.SourceURL
				}
				if typeID != nil {
					r.DishTypeID = typeID
				}
				r.UpdatedByID, r.UpdatedByName = &user.ID, user.Name
				if err := tx.Omit("MealTimes").Save(&r).Error; err != nil {
					return err
				}
				imp.Replaced++
				saved = append(saved, idName{r.ID, r.Name})
				continue
			}
			r := models.Recipe{OrganizationID: orgID, Name: it.Name, Description: it.Description,
				Ingredients: it.Ingredients, Instructions: it.Instructions, PrepTime: it.PrepTime, CookTime: it.CookTime,
				Servings: it.Servings, FamilyMember: "all", Tags: it.Tags, VideoURL: it.VideoURL, SourceURL: it.SourceURL,
				IsActive: true, DishTypeID: typeID, ImportID: &imp.ID, UpdatedByID: &user.ID, UpdatedByName: user.Name}
			if err := tx.Create(&r).Error; err != nil {
				return err
			}
			imp.Created++
			saved = append(saved, idName{r.ID, r.Name})
		}
		if imp.Created+imp.Replaced == 0 {
			return errNothing
		}
		return tx.Save(&imp).Error
	})
	if errors.Is(err, errNothing) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nothing to save", "code": "nothing_to_save"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"import_id": imp.ID, "created": imp.Created, "replaced": imp.Replaced,
		"skipped": imp.Skipped, "recipes": saved, "undo_until": imp.CreatedAt.Add(UndoWindow)})
}

// UndoRecipeImport — DELETE /admin/api/recipe-imports/:id: удалить рецепты, созданные загрузкой (сутки).
// Заменённые загрузкой рецепты не откатываются.
func (h *AdminHandler) UndoRecipeImport(c *gin.Context) {
	var imp models.RecipeImport
	if err := h.orgDB(c).First(&imp, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Import not found"})
		return
	}
	if imp.UndoneAt != nil {
		c.JSON(http.StatusGone, gin.H{"error": "already undone", "code": "undo_done"})
		return
	}
	if time.Since(imp.CreatedAt) > UndoWindow {
		c.JSON(http.StatusGone, gin.H{"error": "undo window has passed", "code": "undo_expired"})
		return
	}
	deleted := 0
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var recipes []models.Recipe
		if err := tx.Where("organization_id = ? AND import_id = ?", imp.OrganizationID, imp.ID).Find(&recipes).Error; err != nil {
			return err
		}
		for _, r := range recipes {
			if err := deleteRecipe(tx, r); err != nil {
				return err
			}
			deleted++
		}
		now := time.Now()
		return tx.Model(&imp).Update("undone_at", &now).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted, "replaced_kept": imp.Replaced})
}
