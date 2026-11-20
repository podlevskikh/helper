package menu

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/models"
)

// Source — для какого блюда нужен продукт: задача-приём пищи, рецепт, день, порции.
// Removed — блюдо убрали из меню после того, как список собрали (продукт остаётся, «можно убрать»).
type Source struct {
	TaskID     uint   `json:"task_id"`
	RecipeID   uint   `json:"recipe_id"`
	Recipe     string `json:"recipe"`
	Date       string `json:"date"` // YYYY-MM-DD
	Meal       string `json:"meal"` // «Обед»
	Servings   int    `json:"servings"`
	PerPortion string `json:"per_portion,omitempty"` // «100 г»
	Removed    bool   `json:"removed,omitempty"`
}

// DayMeal — приём пищи в меню дня.
type DayMeal struct {
	TaskID   uint        `json:"task_id"`
	Time     string      `json:"time"`
	Meal     string      `json:"meal"`
	Servings int         `json:"servings"`
	Passed   bool        `json:"passed"` // сегодня и уже прошёл (или отмечен сделанным) — не покупаем
	Recipes  []DayRecipe `json:"recipes"`
	recipes  []models.Recipe
}

type DayRecipe struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type Day struct {
	Date  string    `json:"date"`
	Meals []DayMeal `json:"meals"`
}

// Need — продукт в сумме за дни.
type Need struct {
	Name     string
	Amount   float64 // уже округлено для покупки; 0 — без количества
	Unit     string
	Category string
	Sources  []Source
}

// MealLabel: «Обед - all» → «Обед».
func MealLabel(title string) string {
	if i := strings.Index(title, " - "); i > 0 {
		return strings.TrimSpace(title[:i])
	}
	return strings.TrimSpace(title)
}

// Servings — сколько порций готовить в задаче.
func Servings(t models.ScheduleTask) int {
	if t.Servings > 0 {
		return t.Servings
	}
	return models.DefaultMealServings
}

// Menu — приёмы пищи с блюдами за дни [from, to] (канонические дни clock.Day: полночь UTC;
// from — сегодня дома).
// now — текущее время дома: сегодняшние прошедшие приёмы помечаются Passed.
func Menu(db *gorm.DB, orgID uint, from, to time.Time, now time.Time) ([]Day, error) {
	var tasks []models.ScheduleTask
	if err := db.Joins("JOIN daily_schedules ds ON ds.id = schedule_tasks.schedule_id").
		Where("schedule_tasks.organization_id = ? AND schedule_tasks.task_type = ? AND ds.date >= ? AND ds.date <= ?",
			orgID, "meal", from, to).
		Preload("Recipes").Order("schedule_tasks.time ASC, schedule_tasks.id ASC").
		Find(&tasks).Error; err != nil {
		return nil, err
	}
	dateOf := map[uint]string{}
	if len(tasks) > 0 {
		ids := []uint{}
		for _, t := range tasks {
			ids = append(ids, t.ScheduleID)
		}
		var scheds []models.DailySchedule
		db.Where("id IN ?", ids).Find(&scheds)
		for _, s := range scheds {
			dateOf[s.ID] = s.Date.UTC().Format("2006-01-02")
		}
	}
	today := now.Format("2006-01-02")
	hm := now.Format("15:04")
	byDate := map[string][]DayMeal{}
	for _, t := range tasks {
		d := dateOf[t.ScheduleID]
		if d == "" {
			continue
		}
		m := DayMeal{TaskID: t.ID, Time: t.Time, Meal: MealLabel(t.Title), Servings: Servings(t), recipes: t.Recipes,
			Recipes: []DayRecipe{}}
		m.Passed = t.Completed || (d == today && t.Time != "" && t.Time < hm)
		for _, r := range t.Recipes {
			m.Recipes = append(m.Recipes, DayRecipe{ID: r.ID, Name: r.Name})
		}
		byDate[d] = append(byDate[d], m)
	}
	days := []Day{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		meals := byDate[key]
		if meals == nil {
			meals = []DayMeal{}
		}
		days = append(days, Day{Date: key, Meals: meals})
	}
	return days, nil
}

// Needs складывает ингредиенты всех непрошедших приёмов пищи. Возвращает продукты для списка
// (по отделам, внутри — по алфавиту) и «обычно есть дома» (без повторов).
func Needs(days []Day) ([]Need, []string) {
	acc := map[string]*Need{}
	order := []string{}
	pantrySeen := map[string]bool{}
	pantry := []string{}
	raw := map[string]float64{}
	for _, d := range days {
		for _, m := range d.Meals {
			if m.Passed {
				continue
			}
			for _, r := range m.recipes {
				for _, ing := range Parse(r.Ingredients) {
					if Skip(ing.Name) {
						continue
					}
					if Pantry(ing.Name) {
						k := Key(ing.Name)
						if !pantrySeen[k] {
							pantrySeen[k] = true
							pantry = append(pantry, ing.Name)
						}
						continue
					}
					key := Key(ing.Name) + "|" + ing.Unit
					n, ok := acc[key]
					if !ok {
						n = &Need{Name: ing.Name, Unit: ing.Unit, Category: Category(ing.Name)}
						acc[key] = n
						order = append(order, key)
					}
					raw[key] += ing.Amount * float64(m.Servings)
					n.Sources = append(n.Sources, Source{TaskID: m.TaskID, RecipeID: r.ID, Recipe: r.Name, Date: d.Date,
						Meal: m.Meal, Servings: m.Servings, PerPortion: ing.Raw})
				}
			}
		}
	}
	// Один продукт в штуках и в граммах (картофель «1 шт» в борще и «200 г» в курице) — переводим граммы в штуки.
	for _, k := range order {
		n := acc[k]
		if n == nil || n.Unit != "г" {
			continue
		}
		w := PieceGrams(n.Name)
		pk := strings.TrimSuffix(k, "|г") + "|шт"
		if p, ok := acc[pk]; ok && w > 0 {
			raw[pk] += raw[k] / w
			p.Sources = append(p.Sources, n.Sources...)
			delete(acc, k)
		}
	}
	out := make([]Need, 0, len(order))
	for _, k := range order {
		if acc[k] == nil {
			continue
		}
		n := acc[k]
		n.Amount = Round(raw[k], n.Unit)
		out = append(out, *n)
	}
	rank := map[string]int{CatProduce: 0, CatMeat: 1, CatDairy: 2, CatGrocery: 3, CatOther: 4}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Category] != rank[out[j].Category] {
			return rank[out[i].Category] < rank[out[j].Category]
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, pantry
}

func encodeSources(s []Source) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func decodeSources(s string) []Source {
	var out []Source
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	return out
}

// Item — пункт списка из продукта меню.
func Item(orgID, listID uint, n Need, pos int, addedBy string, uid *uint) models.ShoppingListItem {
	return models.ShoppingListItem{
		OrganizationID: orgID,
		ListID:         &listID,
		Item:           n.Name,
		Quantity:       Format(n.Amount, n.Unit),
		Category:       n.Category,
		AddedBy:        addedBy,
		AddedByUserID:  uid,
		Position:       pos,
		FromMenu:       true,
		NeedAmount:     n.Amount,
		BuyAmount:      n.Amount,
		Unit:           n.Unit,
		Sources:        encodeSources(n.Sources),
	}
}

// openMenuLists — несданные списки «под меню», которые покрывают день.
func openMenuLists(tx *gorm.DB, orgID uint, date string) []models.ShoppingList {
	var lists []models.ShoppingList
	tx.Where("organization_id = ? AND status IN ? AND menu_from <> '' AND menu_from <= ? AND menu_to >= ?",
		orgID, []string{models.ShoppingListDraft, models.ShoppingListOpen}, date, date).Find(&lists)
	return lists
}

func taskDate(tx *gorm.DB, t models.ScheduleTask) string {
	var s models.DailySchedule
	if err := tx.First(&s, t.ScheduleID).Error; err != nil {
		return ""
	}
	return s.Date.UTC().Format("2006-01-02")
}

// RecipeAdded — в приём пищи добавили блюдо (наниматель заменил блюдо): продукты для него
// сами дописываются в открытые списки «под меню» на этот день. Помощнику ничего делать не нужно.
func RecipeAdded(tx *gorm.DB, task models.ScheduleTask, recipe models.Recipe) error {
	date := taskDate(tx, task)
	if date == "" {
		return nil
	}
	lists := openMenuLists(tx, task.OrganizationID, date)
	if len(lists) == 0 {
		return nil
	}
	day := Day{Date: date, Meals: []DayMeal{{TaskID: task.ID, Meal: MealLabel(task.Title), Servings: Servings(task),
		recipes: []models.Recipe{recipe}}}}
	needs, _ := Needs([]Day{day})
	for _, l := range lists {
		var items []models.ShoppingListItem
		tx.Where("list_id = ?", l.ID).Order("position ASC, id ASC").Find(&items)
		maxPos := 0
		for _, it := range items {
			if it.Position > maxPos {
				maxPos = it.Position
			}
		}
		for _, n := range needs {
			var hit *models.ShoppingListItem
			for i := range items {
				it := &items[i]
				if it.FromMenu && !it.Purchased && Key(it.Item) == Key(n.Name) && it.Unit == n.Unit {
					hit = it
					break
				}
			}
			add := n.Amount
			if hit == nil && n.Unit == "г" && PieceGrams(n.Name) > 0 { // в списке этот продукт в штуках
				for i := range items {
					it := &items[i]
					if it.FromMenu && !it.Purchased && Key(it.Item) == Key(n.Name) && it.Unit == "шт" {
						hit, add = it, Round(n.Amount/PieceGrams(n.Name), "шт")
						break
					}
				}
			}
			if hit != nil {
				// Добавляем к тому, что уже берём; лишнее не убираем — это решение того, кто заменил блюдо.
				hit.NeedAmount += add
				hit.BuyAmount += add
				hit.Quantity = Format(hit.BuyAmount, hit.Unit)
				hit.Sources = encodeSources(append(decodeSources(hit.Sources), n.Sources...))
				if err := tx.Save(hit).Error; err != nil {
					return err
				}
				continue
			}
			maxPos++
			it := Item(l.OrganizationID, l.ID, n, maxPos, "menu", nil)
			if err := tx.Create(&it).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// RecipeRemoved — блюдо убрали из приёма пищи: в открытых списках «под меню» источник
// помечается убранным; количество не трогаем (лишнее купить не страшно).
func RecipeRemoved(tx *gorm.DB, task models.ScheduleTask, recipeID uint) error {
	date := taskDate(tx, task)
	if date == "" {
		return nil
	}
	for _, l := range openMenuLists(tx, task.OrganizationID, date) {
		var items []models.ShoppingListItem
		tx.Where("list_id = ? AND from_menu = ?", l.ID, true).Find(&items)
		for _, it := range items {
			src := decodeSources(it.Sources)
			changed := false
			for i := range src {
				if src[i].TaskID == task.ID && src[i].RecipeID == recipeID && !src[i].Removed {
					src[i].Removed = true
					changed = true
				}
			}
			if changed {
				if err := tx.Model(&models.ShoppingListItem{}).Where("id = ?", it.ID).
					Update("sources", encodeSources(src)).Error; err != nil {
					return err
				}
			}
		}
	}
	return nil
}
