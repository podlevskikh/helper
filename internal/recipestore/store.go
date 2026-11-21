// Package recipestore — рецепты дома как действия: загрузка списком (предпросмотр, сохранение, отмена),
// правка, удаление. Общее для API приложения (/admin/api/recipe-imports) и MCP-коннектора для Claude (/mcp).
// Права (manage_recipes) и дом проверяет вызывающий; здесь — только данные одного дома.
package recipestore

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/recipeimport"

	"gorm.io/gorm"
)

// UndoWindow — сколько после загрузки работает «Отменить загрузку».
const UndoWindow = 24 * time.Hour

// Источники загрузки.
const (
	SourceText = "text" // «Загрузить списком» в приложении
	SourceMCP  = "mcp"  // из чата с Claude
)

// Error — ошибка для ответа: HTTP-код, машинный код и (необязательно) подробности.
type Error struct {
	Status int
	Code   string
	Msg    string
	Extra  map[string]any
}

func (e *Error) Error() string { return e.Msg }

func fail(status int, code, msg string) *Error { return &Error{Status: status, Code: code, Msg: msg} }

// Actor — кто делает: человек и как его показывать в «Изменено: …».
type Actor struct {
	UserID uint
	Name   string // уже с пометкой источника, например «Татьяна · Claude»
}

// Decision — решение по рецепту из загрузки.
type Decision struct {
	Index       int    `json:"index"`
	Action      string `json:"action"` // create | replace | skip
	DishTypeID  *uint  `json:"dish_type_id"`
	NewDishType string `json:"new_dish_type"` // создать тип с таким названием (или взять, если уже есть)
}

// ImportInput — текст по шаблону и решения.
type ImportInput struct {
	Text              string     `json:"text"`
	DefaultDishTypeID *uint      `json:"default_dish_type_id"`
	DryRun            bool       `json:"dry_run"`
	Decisions         []Decision `json:"decisions"`
}

// IDName — ссылка на объект в ответе.
type IDName struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// PreviewItem — рецепт из текста с тем, что о нём известно в доме.
type PreviewItem struct {
	recipeimport.Item
	Status      string  `json:"status"` // new | duplicate | error
	DuplicateOf *IDName `json:"duplicate_of"`
	DishType    *IDName `json:"dish_type"`     // тип дома: из текста или «для всех без типа»
	NewDishType string  `json:"new_dish_type"` // в тексте тип, которого в доме нет — предложим создать
}

// Preview — ответ предпросмотра.
type Preview struct {
	Items   []PreviewItem  `json:"items"`
	Summary map[string]int `json:"summary"`
	Max     int            `json:"max"`
}

// Result — ответ сохранения.
type Result struct {
	ImportID  uint      `json:"import_id"`
	Created   int       `json:"created"`
	Replaced  int       `json:"replaced"`
	Skipped   int       `json:"skipped"`
	Recipes   []IDName  `json:"recipes"`
	UndoUntil time.Time `json:"undo_until"`
	Source    string    `json:"source"`
}

type house struct {
	items   []PreviewItem
	types   map[string]models.DishType // по нормализованному названию
	typeIDs map[uint]models.DishType
}

func load(db *gorm.DB, orgID uint, in ImportInput) (*house, *Error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, fail(http.StatusBadRequest, "empty", "text is empty")
	}
	if len(in.Text) > recipeimport.MaxBytes {
		e := fail(http.StatusRequestEntityTooLarge, "too_large", "text is too large")
		e.Extra = map[string]any{"max_bytes": recipeimport.MaxBytes}
		return nil, e
	}
	items := recipeimport.Parse(in.Text)
	if len(items) == 0 {
		return nil, fail(http.StatusBadRequest, "no_recipes", "no recipes found")
	}
	if len(items) > recipeimport.MaxRecipes {
		e := fail(http.StatusRequestEntityTooLarge, "too_many", "too many recipes")
		e.Extra = map[string]any{"max": recipeimport.MaxRecipes, "found": len(items)}
		return nil, e
	}

	h := &house{types: map[string]models.DishType{}, typeIDs: map[uint]models.DishType{}}
	byName := map[string]models.Recipe{}
	var recipes []models.Recipe
	if err := db.Where("organization_id = ?", orgID).Select("id", "name").Order("id").Find(&recipes).Error; err != nil {
		return nil, fail(http.StatusInternalServerError, "db", err.Error())
	}
	for _, r := range recipes {
		k := recipeimport.NormalizeName(r.Name)
		if _, ok := byName[k]; !ok {
			byName[k] = r
		}
	}
	var types []models.DishType
	if err := db.Where("organization_id = ?", orgID).Order("position, id").Find(&types).Error; err != nil {
		return nil, fail(http.StatusInternalServerError, "db", err.Error())
	}
	for _, t := range types {
		h.types[recipeimport.NormalizeName(t.Name)] = t
		h.typeIDs[t.ID] = t
	}
	var def *IDName
	if in.DefaultDishTypeID != nil && *in.DefaultDishTypeID != 0 {
		t, ok := h.typeIDs[*in.DefaultDishTypeID]
		if !ok {
			return nil, fail(http.StatusBadRequest, "bad_dish_type", "unknown dish_type_id")
		}
		def = &IDName{t.ID, t.Name}
	}

	for _, it := range items {
		p := PreviewItem{Item: it, Status: "new"}
		if r, ok := byName[recipeimport.NormalizeName(it.Name)]; ok && it.Name != "" {
			p.DuplicateOf = &IDName{r.ID, r.Name}
			p.Status = "duplicate"
		}
		if !it.OK() {
			p.Status = "error"
		}
		if it.DishType != "" {
			if t, ok := h.types[recipeimport.NormalizeName(it.DishType)]; ok {
				p.DishType = &IDName{t.ID, t.Name}
			} else {
				p.NewDishType = it.DishType
			}
		} else {
			p.DishType = def
		}
		h.items = append(h.items, p)
	}
	return h, nil
}

// PreviewImport — разбор без сохранения.
func PreviewImport(db *gorm.DB, orgID uint, in ImportInput) (*Preview, *Error) {
	h, e := load(db, orgID, in)
	if e != nil {
		return nil, e
	}
	sum := map[string]int{"new": 0, "duplicate": 0, "error": 0}
	for _, it := range h.items {
		sum[it.Status]++
	}
	return &Preview{Items: h.items, Summary: sum, Max: recipeimport.MaxRecipes}, nil
}

// DefaultDecisions — как в приложении без правок: новые — загружать, дубли и ошибки — пропускать.
func DefaultDecisions(p *Preview) []Decision {
	out := make([]Decision, 0, len(p.Items))
	for _, it := range p.Items {
		a := "skip"
		if it.Status == "new" {
			a = "create"
		}
		out = append(out, Decision{Index: it.Index, Action: a})
	}
	return out
}

// SaveImport — сохранить по решениям одной транзакцией. Ничего не сохранилось — 400 nothing_to_save.
func SaveImport(db *gorm.DB, orgID uint, who Actor, in ImportInput, source string) (*Result, *Error) {
	h, e := load(db, orgID, in)
	if e != nil {
		return nil, e
	}
	if len(in.Decisions) == 0 {
		return nil, fail(http.StatusBadRequest, "no_decisions", "decisions are required")
	}
	dec := map[int]Decision{}
	for _, d := range in.Decisions {
		if d.Index < 1 || d.Index > len(h.items) {
			return nil, fail(http.StatusBadRequest, "bad_decision", "decision index out of range")
		}
		switch d.Action {
		case "create", "replace", "skip":
		default:
			return nil, fail(http.StatusBadRequest, "bad_decision", "unknown action")
		}
		if d.DishTypeID != nil && *d.DishTypeID != 0 {
			if _, ok := h.typeIDs[*d.DishTypeID]; !ok {
				return nil, fail(http.StatusBadRequest, "bad_dish_type", "unknown dish_type_id")
			}
		}
		if len([]rune(strings.TrimSpace(d.NewDishType))) > 60 {
			return nil, fail(http.StatusBadRequest, "bad_dish_type", "dish type name is too long")
		}
		dec[d.Index] = d
	}
	if source == "" {
		source = SourceText
	}

	imp := models.RecipeImport{OrganizationID: orgID, UserID: who.UserID, UserName: who.Name, Source: source}
	var saved []IDName
	errNothing := errors.New("nothing to save")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&imp).Error; err != nil {
			return err
		}
		// Тип блюда: выбранный, новый по названию, из текста, «для всех без типа».
		typeFor := func(it PreviewItem, d Decision) (*uint, error) {
			if d.DishTypeID != nil && *d.DishTypeID != 0 {
				return d.DishTypeID, nil
			}
			name := strings.TrimSpace(d.NewDishType)
			if name == "" && d.DishTypeID == nil && it.DishType == nil {
				name = it.NewDishType
			}
			if name != "" {
				k := recipeimport.NormalizeName(name)
				if t, ok := h.types[k]; ok {
					return &t.ID, nil
				}
				var pos int
				tx.Model(&models.DishType{}).Where("organization_id = ?", orgID).Select("coalesce(max(position), 0)").Scan(&pos)
				t := models.DishType{OrganizationID: orgID, Name: name, Position: pos + 1}
				if err := tx.Create(&t).Error; err != nil {
					return nil, err
				}
				h.types[k] = t
				return &t.ID, nil
			}
			if d.DishTypeID == nil && it.DishType != nil {
				return &it.DishType.ID, nil
			}
			return nil, nil // явно «без типа» (dish_type_id: 0)
		}

		for _, it := range h.items {
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
				r.UpdatedByID, r.UpdatedByName = &who.UserID, who.Name
				if err := tx.Omit("MealTimes").Save(&r).Error; err != nil {
					return err
				}
				imp.Replaced++
				saved = append(saved, IDName{r.ID, r.Name})
				continue
			}
			r := models.Recipe{OrganizationID: orgID, Name: it.Name, Description: it.Description,
				Ingredients: it.Ingredients, Instructions: it.Instructions, PrepTime: it.PrepTime, CookTime: it.CookTime,
				Servings: it.Servings, FamilyMember: "all", Tags: it.Tags, VideoURL: it.VideoURL, SourceURL: it.SourceURL,
				IsActive: true, DishTypeID: typeID, ImportID: &imp.ID, UpdatedByID: &who.UserID, UpdatedByName: who.Name}
			if err := tx.Create(&r).Error; err != nil {
				return err
			}
			imp.Created++
			saved = append(saved, IDName{r.ID, r.Name})
		}
		if imp.Created+imp.Replaced == 0 {
			return errNothing
		}
		return tx.Save(&imp).Error
	})
	if errors.Is(err, errNothing) {
		return nil, fail(http.StatusBadRequest, "nothing_to_save", "nothing to save")
	}
	if err != nil {
		return nil, fail(http.StatusInternalServerError, "db", err.Error())
	}
	return &Result{ImportID: imp.ID, Created: imp.Created, Replaced: imp.Replaced, Skipped: imp.Skipped,
		Recipes: saved, UndoUntil: imp.CreatedAt.Add(UndoWindow), Source: source}, nil
}

// UndoImport — удалить рецепты, созданные загрузкой (сутки). Заменённые не откатываются.
func UndoImport(db *gorm.DB, orgID, importID uint) (deleted int, replaced int, e *Error) {
	var imp models.RecipeImport
	if err := db.Where("organization_id = ?", orgID).First(&imp, importID).Error; err != nil {
		return 0, 0, fail(http.StatusNotFound, "not_found", "Import not found")
	}
	if imp.UndoneAt != nil {
		return 0, 0, fail(http.StatusGone, "undo_done", "already undone")
	}
	if time.Since(imp.CreatedAt) > UndoWindow {
		return 0, 0, fail(http.StatusGone, "undo_expired", "undo window has passed")
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var recipes []models.Recipe
		if err := tx.Where("organization_id = ? AND import_id = ?", imp.OrganizationID, imp.ID).Find(&recipes).Error; err != nil {
			return err
		}
		for _, r := range recipes {
			if err := Delete(tx, r); err != nil {
				return err
			}
			deleted++
		}
		now := time.Now()
		return tx.Model(&imp).Update("undone_at", &now).Error
	})
	if err != nil {
		return 0, 0, fail(http.StatusInternalServerError, "db", err.Error())
	}
	return deleted, imp.Replaced, nil
}

// RecentImports — загрузки дома за последние сутки, которые ещё можно отменить (новые сверху).
func RecentImports(db *gorm.DB, orgID uint) ([]models.RecipeImport, error) {
	var list []models.RecipeImport
	err := db.Where("organization_id = ? AND undone_at IS NULL AND created > 0 AND created_at > ?", orgID, time.Now().Add(-UndoWindow)).
		Order("created_at DESC").Limit(10).Find(&list).Error
	return list, err
}

// Delete удаляет рецепт и всё, что на него ссылается: ротации приёмов пищи, блюда задач (meal_recipes),
// замены на день, старое поле schedule_tasks.recipe_id, блюда «Ритма дома» на день, заметки.
func Delete(tx *gorm.DB, recipe models.Recipe) error {
	if err := tx.Model(&recipe).Association("MealTimes").Clear(); err != nil {
		return err
	}
	for _, q := range []string{
		"DELETE FROM meal_recipes WHERE recipe_id = ?",
		"UPDATE schedule_tasks SET recipe_id = NULL WHERE recipe_id = ?",
		"UPDATE task_dishes SET recipe_id = NULL WHERE recipe_id = ?",
	} {
		if err := tx.Exec(q, recipe.ID).Error; err != nil {
			return err
		}
	}
	// Замены блюд на день, где участвовал рецепт («Вернуть» к удалённому рецепту невозможно).
	if err := tx.Exec("DELETE FROM meal_swaps WHERE original_recipe_id = ? OR current_recipe_id = ?", recipe.ID, recipe.ID).Error; err != nil {
		return err
	}
	if err := tx.Where("recipe_id = ?", recipe.ID).Delete(&models.RecipeComment{}).Error; err != nil {
		return err
	}
	return tx.Delete(&recipe).Error
}

// Patch — правка рецепта: меняются только переданные поля (nil — не трогаем).
type Patch struct {
	Name         *string   `json:"name,omitempty"`
	Description  *string   `json:"description,omitempty"`
	Ingredients  *[]string `json:"ingredients,omitempty"` // по продукту на строку: «Творог — 200 г»
	Instructions *string   `json:"instructions,omitempty"`
	PrepTime     *int      `json:"prep_time,omitempty"`
	CookTime     *int      `json:"cook_time,omitempty"`
	Servings     *int      `json:"servings,omitempty"`
	Tags         *[]string `json:"tags,omitempty"`
	VideoURL     *string   `json:"video_url,omitempty"`
	SourceURL    *string   `json:"source_url,omitempty"`
	DishTypeID   *uint     `json:"dish_type_id,omitempty"` // 0 — без типа
	IsActive     *bool     `json:"is_active,omitempty"`
}

// Editable — можно ли менять рецепт извне (MCP). Сейчас все рецепты дома внесены людьми;
// когда появится общая база рецептов (вариант E), её рецепты будут только для чтения.
func Editable(r models.Recipe) bool { return true }

// Update — правка рецепта дома по Patch. Возвращает обновлённый рецепт.
func Update(db *gorm.DB, orgID, recipeID uint, who Actor, p Patch) (*models.Recipe, *Error) {
	var r models.Recipe
	if err := db.Where("organization_id = ?", orgID).First(&r, recipeID).Error; err != nil {
		return nil, fail(http.StatusNotFound, "not_found", "Recipe not found")
	}
	if !Editable(r) {
		return nil, fail(http.StatusForbidden, "read_only", "this recipe can't be edited")
	}
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" || len([]rune(n)) > recipeimport.MaxName {
			return nil, fail(http.StatusBadRequest, "bad_name", "name must be 1–120 characters")
		}
		r.Name = n
	}
	if p.Description != nil {
		r.Description = strings.TrimSpace(*p.Description)
	}
	if p.Ingredients != nil {
		lines := recipeimport.NormalizeIngredients(*p.Ingredients)
		if len(lines) == 0 {
			return nil, fail(http.StatusBadRequest, "no_ingredients", "ingredients can't be empty")
		}
		r.Ingredients = strings.Join(lines, "\n")
	}
	if p.Instructions != nil {
		r.Instructions = strings.TrimSpace(*p.Instructions)
	}
	minutes := func(v *int, field string, dst *int) *Error {
		if v == nil {
			return nil
		}
		if *v < 0 || *v > 24*60 {
			return fail(http.StatusBadRequest, "bad_number", field+" must be 0–1440 minutes")
		}
		*dst = *v
		return nil
	}
	if e := minutes(p.PrepTime, "prep_time", &r.PrepTime); e != nil {
		return nil, e
	}
	if e := minutes(p.CookTime, "cook_time", &r.CookTime); e != nil {
		return nil, e
	}
	if p.Servings != nil {
		if *p.Servings < 1 || *p.Servings > 100 {
			return nil, fail(http.StatusBadRequest, "bad_number", "servings must be 1–100")
		}
		r.Servings = *p.Servings
	}
	if p.Tags != nil {
		var tags []string
		for _, t := range *p.Tags {
			if t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")); t != "" {
				tags = append(tags, t)
			}
		}
		r.Tags = strings.Join(tags, ", ")
	}
	link := func(v *string, field string, dst *string) *Error {
		if v == nil {
			return nil
		}
		if strings.TrimSpace(*v) == "" {
			*dst = ""
			return nil
		}
		u, ok := recipeimport.CleanURL(*v)
		if !ok {
			return fail(http.StatusBadRequest, "bad_link", field+" must be an http(s) link")
		}
		*dst = u
		return nil
	}
	if e := link(p.VideoURL, "video_url", &r.VideoURL); e != nil {
		return nil, e
	}
	if e := link(p.SourceURL, "source_url", &r.SourceURL); e != nil {
		return nil, e
	}
	if p.DishTypeID != nil {
		if *p.DishTypeID == 0 {
			r.DishTypeID = nil
		} else {
			var n int64
			db.Model(&models.DishType{}).Where("id = ? AND organization_id = ?", *p.DishTypeID, orgID).Count(&n)
			if n == 0 {
				return nil, fail(http.StatusBadRequest, "bad_dish_type", "unknown dish_type_id")
			}
			id := *p.DishTypeID
			r.DishTypeID = &id
		}
	}
	if p.IsActive != nil {
		r.IsActive = *p.IsActive
	}
	r.UpdatedByID, r.UpdatedByName = &who.UserID, who.Name
	if err := db.Omit("MealTimes").Save(&r).Error; err != nil {
		return nil, fail(http.StatusInternalServerError, "db", err.Error())
	}
	return &r, nil
}

// ActorName — имя для «Изменено: …»: имя человека и, если не из приложения, откуда.
func ActorName(name, via string) string {
	name = strings.TrimSpace(name)
	if via == "" {
		return name
	}
	if name == "" {
		return via
	}
	return fmt.Sprintf("%s · %s", name, via)
}
