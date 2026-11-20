package server

import (
	"fmt"
	"strings"
	"testing"

	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"
)

// MEALDAY-01, MEALDAY-02: порции на день и замена блюда с «Вернуть»; открытый список под меню
// пересчитывается сам, закрытый — нет; блюдо планировщика (старое поле recipe_id) тоже меняется.
func TestMealDayServingsAndSwap(t *testing.T) {
	e := newEnv(t, 1100)
	o := e.owner("mealday")
	h := e.member(o, "helper", "mealday")
	mk := func(name, ingr string) uint {
		w := e.do(req{method: "POST", path: "/admin/api/recipes", token: o.Token, org: o.Org, body: map[string]any{
			"name": name, "ingredients": ingr, "family_member": "all", "is_active": true, "servings": 1}})
		e.expect("рецепт "+name, w, 201)
		return uint(e.json(w)["id"].(float64))
	}
	borsch := mk("Борщ день", "Свёкла — 70 г\nСоль")
	chicken := mk("Курица день", "Курица (бёдра) — 250 г")
	plov := mk("Плов день", "Рис — 80 г")

	// Обед завтра, как его ставит планировщик: блюдо только в recipe_id, meal_recipes пуст.
	day := clock.OrgToday(e.db, o.Org).AddDate(0, 0, 1)
	s, err := models.EnsureDailySchedule(e.db, o.Org, day, false)
	if err != nil {
		t.Fatal(err)
	}
	lunch := models.ScheduleTask{OrganizationID: o.Org, ScheduleID: s.ID, TaskType: "meal", Title: "Обед - all",
		Time: "13:00", RecipeID: &borsch}
	e.db.Create(&lunch)
	other := models.ScheduleTask{OrganizationID: o.Org, ScheduleID: s.ID, TaskType: "custom", Title: "Мусор"}
	e.db.Create(&other)
	taskPath := fmt.Sprintf("/admin/api/tasks/%d", lunch.ID)

	w := e.do(req{method: "POST", path: "/helper/api/shopping-lists/from-menu", token: h.Token, org: h.Org, body: map[string]any{"days": 3}})
	e.expect("список из меню", w, 201)
	listID := e.json(w)["id"].(float64)
	items := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		w := e.do(req{method: "GET", path: fmt.Sprintf("/helper/api/shopping-lists/%.0f", listID), token: h.Token, org: h.Org})
		list, _ := e.json(w)["items"].([]any)
		for _, x := range list {
			it := x.(map[string]any)
			out[it["item"].(string)] = it
		}
		return out
	}
	if b := items()["Свёкла"]; b == nil || b["need_amount"] != 150.0 {
		t.Fatalf("блюдо из recipe_id не попало в покупки (70 г × 2 → 150 г): %v", b)
	}

	// Порции.
	servings := func(n any, token string, org uint) map[string]any {
		w := e.do(req{method: "PUT", path: taskPath + "/servings", token: token, org: org, body: map[string]any{"servings": n}})
		if w.Code != 200 {
			return map[string]any{"_code": float64(w.Code)}
		}
		return e.json(w)
	}
	if r := servings(3, h.Token, h.Org); r["_code"] != 403.0 {
		t.Errorf("помощник меняет порции: %v", r)
	}
	if r := servings(21, o.Token, o.Org); r["_code"] != 400.0 {
		t.Errorf("21 порция: %v", r)
	}
	e.expect("порции у не-еды", e.do(req{method: "PUT", path: fmt.Sprintf("/admin/api/tasks/%d/servings", other.ID), token: o.Token, org: o.Org,
		body: map[string]any{"servings": 3}}), 400)
	r := servings(3, o.Token, o.Org)
	if r["servings"] != 3.0 || r["usual_servings"] != 2.0 {
		t.Fatalf("порции 3: %v", r)
	}
	b := items()["Свёкла"]
	if b["need_amount"] != 250.0 || b["buy_amount"] != 250.0 {
		t.Errorf("свёкла на 3 порции (210 → 250 г, как при сборке): %v", b)
	}
	if src := b["sources"].(string); strings.Count(src, `"task_id"`) != 1 || !strings.Contains(src, `"servings":3`) {
		t.Errorf("источники после роста порций: %s", src)
	}
	servings(1, o.Token, o.Org)
	if b := items()["Свёкла"]; b["need_amount"] != 70.0 {
		t.Errorf("свёкла на 1 порцию: %v", b)
	}
	servings(0, o.Token, o.Org) // как обычно = 2
	if b := items()["Свёкла"]; b["need_amount"] != 150.0 {
		t.Errorf("свёкла «как обычно» — как при сборке: %v", b)
	}

	// Замена: из recipe_id → курица → плов, затем «Вернуть».
	swap := func(from, to uint) map[string]any {
		w := e.do(req{method: "POST", path: taskPath + "/swap", token: o.Token, org: o.Org,
			body: map[string]any{"from_recipe_id": from, "to_recipe_id": to}})
		e.expect(fmt.Sprintf("замена %d → %d", from, to), w, 200)
		return e.json(w)
	}
	e.expect("помощник меняет блюдо", e.do(req{method: "POST", path: taskPath + "/swap", token: h.Token, org: h.Org,
		body: map[string]any{"from_recipe_id": borsch, "to_recipe_id": chicken}}), 403)
	e.expect("блюда нет в обеде", e.do(req{method: "POST", path: taskPath + "/swap", token: o.Token, org: o.Org,
		body: map[string]any{"from_recipe_id": plov, "to_recipe_id": chicken}}), 400)
	dishes := func(task map[string]any) []uint {
		out := []uint{}
		list, _ := task["recipes"].([]any)
		for _, x := range list {
			out = append(out, uint(x.(map[string]any)["id"].(float64)))
		}
		return out
	}
	swapOf := func(task map[string]any) map[string]any {
		list, _ := task["swaps"].([]any)
		if len(list) != 1 {
			t.Fatalf("замены: %v", task["swaps"])
		}
		return list[0].(map[string]any)
	}
	tk := swap(borsch, chicken)
	if d := dishes(tk); len(d) != 1 || d[0] != chicken {
		t.Errorf("после замены блюда: %v", d)
	}
	if sw := swapOf(tk); sw["original_recipe_id"] != float64(borsch) || sw["original_name"] != "Борщ день" || sw["current_recipe_id"] != float64(chicken) {
		t.Errorf("замена: %v", sw)
	}
	it := items()
	if it["Курица (бёдра)"] == nil || !strings.Contains(it["Свёкла"]["sources"].(string), `"removed":true`) {
		t.Errorf("покупки после замены: %v", it)
	}
	tk = swap(chicken, plov)
	if sw := swapOf(tk); sw["original_recipe_id"] != float64(borsch) || sw["current_recipe_id"] != float64(plov) {
		t.Errorf("повторная замена потеряла исходное блюдо: %v", sw)
	}
	e.expect("вернуть не заменённое", e.do(req{method: "POST", path: taskPath + "/swap/undo", token: o.Token, org: o.Org,
		body: map[string]any{"recipe_id": chicken}}), 404)
	w = e.do(req{method: "POST", path: taskPath + "/swap/undo", token: o.Token, org: o.Org, body: map[string]any{"recipe_id": plov}})
	e.expect("вернуть", w, 200)
	tk = e.json(w)
	if d := dishes(tk); len(d) != 1 || d[0] != borsch || tk["swaps"] != nil {
		t.Errorf("после «Вернуть»: блюда %v, замены %v", d, tk["swaps"])
	}
	it = items()
	if strings.Contains(it["Свёкла"]["sources"].(string), `"removed":true`) || it["Свёкла"]["need_amount"] != 150.0 {
		t.Errorf("свёкла после «Вернуть»: %v", it["Свёкла"])
	}

	// День у помощника: обычные порции видны.
	w = e.do(req{method: "GET", path: "/helper/api/schedule/date/" + day.Format("2006-01-02"), token: h.Token, org: h.Org})
	e.expect("день помощника", w, 200)
	found := false
	for _, x := range e.json(w)["tasks"].([]any) {
		tsk := x.(map[string]any)
		if tsk["id"] == float64(lunch.ID) {
			found = tsk["usual_servings"] == 2.0
		}
	}
	if !found {
		t.Errorf("в дне помощника нет usual_servings у обеда")
	}

	// Закрытый список не пересчитываем.
	e.db.Model(&models.ShoppingList{}).Where("id = ?", uint(listID)).Update("status", models.ShoppingListClosed)
	servings(5, o.Token, o.Org)
	if b := items()["Свёкла"]; b["need_amount"] != 150.0 {
		t.Errorf("закрытый список изменился: %v", b)
	}
}
