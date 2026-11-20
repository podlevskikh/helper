package menu

import (
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/models"
)

// ServingsChanged — в приёме пищи поменяли число порций на день (old, new — как в задаче, 0 = по умолчанию).
// Открытые списки «под меню» на этот день пересчитываются на разницу порций; у источников этой
// задачи обновляется число порций. Закрытые списки и купленное не трогаем.
func ServingsChanged(tx *gorm.DB, task models.ScheduleTask, oldServings, newServings int) error {
	before, after := oldServings, newServings
	if before <= 0 {
		before = models.DefaultMealServings
	}
	if after <= 0 {
		after = models.DefaultMealServings
	}
	if before == after {
		return nil
	}
	date := taskDate(tx, task)
	if date == "" {
		return nil
	}
	// Разница между тем, сколько продуктов нужно этому приёму пищи «после» и «до» (с тем же
	// округлением, что и при сборке списка), — чтобы правки туда-обратно не копили погрешность.
	recipes := TaskRecipes(task)
	key := func(n Need) string { return Key(n.Name) + "|" + n.Unit }
	was := map[string]float64{}
	for _, n := range taskNeeds(task, date, recipes, before) {
		was[key(n)] = n.Amount
	}
	var plus, minus []Need
	for _, n := range taskNeeds(task, date, recipes, after) {
		d := n.Amount - was[key(n)]
		delete(was, key(n))
		if d > 0 {
			n.Amount = d
			plus = append(plus, n)
		} else if d < 0 {
			n.Amount = -d
			minus = append(minus, n)
		}
	}
	for _, n := range taskNeeds(task, date, recipes, before) {
		if amt, ok := was[key(n)]; ok && amt > 0 {
			n.Amount = amt
			minus = append(minus, n)
		}
	}
	lists := openMenuLists(tx, task.OrganizationID, date)
	for _, l := range lists {
		if err := applyToList(tx, l, plus, 1); err != nil {
			return err
		}
		if err := applyToList(tx, l, minus, -1); err != nil {
			return err
		}
	}
	for _, l := range lists {
		var items []models.ShoppingListItem
		tx.Where("list_id = ? AND from_menu = ?", l.ID, true).Find(&items)
		for _, it := range items {
			// При росте порций applyNeeds дописал источники этой задачи ещё раз — оставляем по одному
			// на блюдо и ставим новое число порций.
			src := decodeSources(it.Sources)
			out := make([]Source, 0, len(src))
			seen := map[uint]bool{}
			changed := false
			for _, s := range src {
				if s.TaskID == task.ID {
					if seen[s.RecipeID] {
						changed = true
						continue
					}
					seen[s.RecipeID] = true
					if s.Servings != after {
						s.Servings = after
						changed = true
					}
				}
				out = append(out, s)
			}
			src = out
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

// RecipeRestored — блюдо вернули в приём пищи («Вернуть» после замены). Если в открытых списках
// его продукты остались с пометкой «убрано», пометку снимаем — количество там и так учтено.
// Если списка тогда ещё не было (нет ни одного такого источника) — дописываем продукты как новое блюдо.
func RecipeRestored(tx *gorm.DB, task models.ScheduleTask, recipe models.Recipe) error {
	date := taskDate(tx, task)
	if date == "" {
		return nil
	}
	for _, l := range openMenuLists(tx, task.OrganizationID, date) {
		var items []models.ShoppingListItem
		tx.Where("list_id = ? AND from_menu = ?", l.ID, true).Find(&items)
		found := false
		for _, it := range items {
			src := decodeSources(it.Sources)
			changed := false
			for i := range src {
				if src[i].TaskID == task.ID && src[i].RecipeID == recipe.ID {
					found = true
					if src[i].Removed {
						src[i].Removed = false
						changed = true
					}
				}
			}
			if changed {
				if err := tx.Model(&models.ShoppingListItem{}).Where("id = ?", it.ID).
					Update("sources", encodeSources(src)).Error; err != nil {
					return err
				}
			}
		}
		if !found {
			if err := applyToList(tx, l, taskNeeds(task, date, []models.Recipe{recipe}, Servings(task)), 1); err != nil {
				return err
			}
		}
	}
	return nil
}
