package server

import (
	"fmt"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/models"
)

const importText = `# Мои рецепты

## 1. Борщ
Тип: обед
Ингредиенты:
Свёкла — 80 г
Как готовить:
Сварить по-новому.

## Плов с курицей
Тип: Суп
Порций: 4
Ссылка: https://example.com/plov
Ингредиенты:
Курица — 600 г
Рис — 280 г
Как готовить:
Обжарить.

## Сырники
Ингредиенты:

## Омлет
Prep: 5 min · Cooking: 10 min
Video: https://www.youtube.com/watch?v=x
Ingredients:
Eggs — 2 pcs
How to cook:
Whisk and fry.
`

// RIMP-01, RIMP-02, RIMP-04: предпросмотр (новый / дубль / ошибка / тип), сохранение по решениям —
// «Заменить» не трогает оценку и «готовим сейчас»; ошибочный не сохраняется; отмена удаляет созданные.
// Помощник и модератор → 403; дубли и типы — только своего дома.
func TestRecipeImport(t *testing.T) {
	e := newEnv(t, 1200)
	o := e.owner("rimp")
	h := e.member(o, "helper", "rimp")
	mgr := e.member(o, "manager", "rimp")
	other := e.owner("rimpOther")
	lunch := e.dishType(o, "Обед")
	borsch := e.recipe(o, "борщ", lunch)
	e.db.Model(&models.Recipe{}).Where("id = ?", uint(borsch)).Updates(map[string]any{"rating": 5, "is_active": false})
	e.recipe(other, "Омлет", e.dishType(other, "Завтрак")) // чужой дом — не дубль

	imp := func(a acct, body map[string]any) map[string]any {
		w := e.do(req{method: "POST", path: "/admin/api/recipe-imports", token: a.Token, org: a.Org, body: body})
		if w.Code >= 300 {
			return map[string]any{"_code": float64(w.Code), "body": e.json(w)}
		}
		m := e.json(w)
		m["_code"] = float64(w.Code)
		return m
	}
	for _, a := range []acct{h, mgr} {
		if r := imp(a, map[string]any{"text": importText, "dry_run": true}); r["_code"] != 403.0 {
			t.Fatalf("загрузка без manage_recipes: %v", r["_code"])
		}
	}

	pre := imp(o, map[string]any{"text": importText, "dry_run": true, "default_dish_type_id": lunch})
	if pre["_code"] != 200.0 {
		t.Fatalf("предпросмотр: %v", pre)
	}
	items := pre["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("ждали 4 рецепта: %d", len(items))
	}
	it := func(i int) map[string]any { return items[i].(map[string]any) }
	if it(0)["status"] != "duplicate" || it(0)["duplicate_of"].(map[string]any)["id"] != borsch {
		t.Fatalf("борщ — дубль: %v", it(0))
	}
	if it(0)["dish_type"].(map[string]any)["name"] != "Обед" {
		t.Fatalf("тип «обед» найден без учёта регистра: %v", it(0)["dish_type"])
	}
	if it(1)["status"] != "new" || it(1)["new_dish_type"] != "Суп" || it(1)["dish_type"] != nil {
		t.Fatalf("плов: новый тип «Суп»: %v", it(1))
	}
	if it(2)["status"] != "error" {
		t.Fatalf("сырники без ингредиентов — ошибка: %v", it(2))
	}
	if it(3)["status"] != "new" || it(3)["dish_type"].(map[string]any)["id"] != lunch {
		t.Fatalf("омлет: тип «для всех без типа», чужой омлет не дубль: %v", it(3))
	}
	if s := pre["summary"].(map[string]any); s["new"] != 2.0 || s["duplicate"] != 1.0 || s["error"] != 1.0 {
		t.Fatalf("итог: %v", s)
	}
	var n int64
	e.db.Model(&models.Recipe{}).Where("organization_id = ?", o.Org).Count(&n)
	if n != 1 {
		t.Fatalf("предпросмотр ничего не сохраняет: рецептов %d", n)
	}

	// Чужой тип блюда не принимается.
	foreign := e.dishType(other, "Чужой")
	if r := imp(o, map[string]any{"text": importText, "dry_run": true, "default_dish_type_id": foreign}); r["_code"] != 400.0 {
		t.Fatalf("чужой тип по умолчанию: %v", r)
	}

	save := imp(o, map[string]any{"text": importText, "default_dish_type_id": lunch, "decisions": []map[string]any{
		{"index": 1, "action": "replace"}, {"index": 2, "action": "create"},
		{"index": 3, "action": "create"}, {"index": 4, "action": "create"}}})
	if save["_code"] != 201.0 || save["created"] != 2.0 || save["replaced"] != 1.0 || save["skipped"] != 1.0 {
		t.Fatalf("сохранение: %v", save)
	}

	var b models.Recipe
	e.db.First(&b, uint(borsch))
	if b.Instructions != "Сварить по-новому." || b.Rating != 5 || b.IsActive || b.Name != "борщ" {
		t.Fatalf("«Заменить» меняет текст, оценка и «готовим сейчас» остаются: %+v", b)
	}
	var plov, omelet models.Recipe
	e.db.Where("organization_id = ? AND name = ?", o.Org, "Плов с курицей").First(&plov)
	e.db.Where("organization_id = ? AND name = ?", o.Org, "Омлет").First(&omelet)
	var soup models.DishType
	e.db.Where("organization_id = ? AND name = ?", o.Org, "Суп").First(&soup)
	if soup.ID == 0 || plov.DishTypeID == nil || *plov.DishTypeID != soup.ID {
		t.Fatalf("новый тип «Суп» создан и назначен: %+v %+v", soup, plov)
	}
	if !plov.IsActive || plov.Servings != 4 || plov.SourceURL != "https://example.com/plov" || plov.ImportID == nil {
		t.Fatalf("плов: сразу «готовим сейчас», 4 порции, источник: %+v", plov)
	}
	if omelet.VideoURL != "https://www.youtube.com/watch?v=x" || omelet.PrepTime != 5 || omelet.CookTime != 10 ||
		omelet.DishTypeID == nil || *omelet.DishTypeID != uint(lunch) || omelet.UpdatedByName == "" {
		t.Fatalf("омлет: %+v", omelet)
	}

	// Повторная загрузка того же текста: всё уже есть — сохранять нечего.
	again := imp(o, map[string]any{"text": importText, "decisions": []map[string]any{{"index": 2, "action": "create"}}})
	if again["_code"] != 400.0 {
		t.Fatalf("повтор: дубли пропускаются, сохранять нечего: %v", again)
	}

	// Отмена: удаляет созданные (и из меню дня), заменённый борщ остаётся.
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 2)
	s, _ := models.EnsureDailySchedule(e.db, o.Org, day, false)
	task := models.ScheduleTask{OrganizationID: o.Org, ScheduleID: s.ID, TaskType: "meal", Title: "Обед", RecipeID: &plov.ID}
	e.db.Create(&task)
	undo := fmt.Sprintf("/admin/api/recipe-imports/%.0f", save["import_id"])
	e.expect("отмена помощником", e.do(req{method: "DELETE", path: undo, token: h.Token, org: h.Org}), 403)
	e.expect("отмена из чужого дома", e.do(req{method: "DELETE", path: undo, token: other.Token, org: other.Org}), 404)
	w := e.do(req{method: "DELETE", path: undo, token: o.Token, org: o.Org})
	e.expect("отмена", w, 200)
	if e.json(w)["deleted"] != 2.0 {
		t.Fatalf("удалено: %v", e.json(w))
	}
	e.db.Model(&models.Recipe{}).Where("organization_id = ?", o.Org).Count(&n)
	e.db.First(&task, task.ID)
	if n != 1 || task.RecipeID != nil {
		t.Fatalf("после отмены остался только борщ, задача без плова: рецептов %d, recipe_id %v", n, task.RecipeID)
	}
	e.expect("повторная отмена", e.do(req{method: "DELETE", path: undo, token: o.Token, org: o.Org}), 410)

	// Через сутки отменить нельзя.
	save = imp(o, map[string]any{"text": importText, "decisions": []map[string]any{{"index": 4, "action": "create"}}})
	if save["_code"] != 201.0 {
		t.Fatalf("вторая загрузка: %v", save)
	}
	e.db.Model(&models.RecipeImport{}).Where("id = ?", uint(save["import_id"].(float64))).
		Update("created_at", time.Now().Add(-25*time.Hour))
	e.expect("отмена через сутки", e.do(req{method: "DELETE", path: fmt.Sprintf("/admin/api/recipe-imports/%.0f", save["import_id"]),
		token: o.Token, org: o.Org}), 410)

	// Пустой текст и слишком много рецептов.
	if r := imp(o, map[string]any{"text": "  ", "dry_run": true}); r["_code"] != 400.0 {
		t.Fatalf("пустой текст: %v", r)
	}
	many := ""
	for i := 0; i < 51; i++ {
		many += fmt.Sprintf("## Рецепт %d\nИнгредиенты:\nРис — 10 г\n", i)
	}
	if r := imp(o, map[string]any{"text": many, "dry_run": true}); r["_code"] != 413.0 {
		t.Fatalf("51 рецепт: %v", r)
	}
}

// Источник рецепта: форма сохраняет и меняет его, старый клиент без поля не стирает; не ссылка → 400.
func TestRecipeSourceURL(t *testing.T) {
	e := newEnv(t, 1300)
	o := e.owner("src")
	w := e.do(req{method: "POST", path: "/admin/api/recipes", token: o.Token, org: o.Org,
		body: map[string]any{"name": "Суп", "source_url": "https://example.com/soup", "servings": 1}})
	e.expect("создать", w, 201)
	id := e.json(w)["id"].(float64)
	path := fmt.Sprintf("/admin/api/recipes/%.0f", id)
	e.expect("без поля", e.do(req{method: "PUT", path: path, token: o.Token, org: o.Org, body: map[string]any{"name": "Суп"}}), 200)
	var r models.Recipe
	e.db.First(&r, uint(id))
	if r.SourceURL != "https://example.com/soup" {
		t.Fatalf("старый клиент стёр источник: %q", r.SourceURL)
	}
	e.expect("не ссылка", e.do(req{method: "PUT", path: path, token: o.Token, org: o.Org,
		body: map[string]any{"name": "Суп", "source_url": "javascript:alert(1)"}}), 400)
	e.expect("убрать", e.do(req{method: "PUT", path: path, token: o.Token, org: o.Org,
		body: map[string]any{"name": "Суп", "source_url": ""}}), 200)
	e.db.First(&r, uint(id))
	if r.SourceURL != "" {
		t.Fatalf("источник не убрался: %q", r.SourceURL)
	}
}
