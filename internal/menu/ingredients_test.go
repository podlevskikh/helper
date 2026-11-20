package menu

import (
	"testing"

	"podlevskikh/awesomeProject/internal/models"
)

func TestParseQty(t *testing.T) {
	cases := []struct {
		in   string
		amt  float64
		unit string
	}{
		{"100 г", 100, "г"},
		{"1,5 кг", 1500, "г"},
		{"200 мл", 200, "мл"},
		{"1 л", 1000, "мл"},
		{"½ шт", 0.5, "шт"},
		{"1/2 шт", 0.5, "шт"},
		{"1 шт (небольшой)", 1, "шт"},
		{"1 ч. л.", 1, "ч. л."},
		{"½ зубчика", 0.5, "зубч."},
		{"2 ломтика", 2, "ломт."},
		{"щепотка", 0, ""},
		{"", 0, ""},
	}
	for _, c := range cases {
		amt, unit := parseQty(c.in)
		if amt != c.amt || unit != c.unit {
			t.Errorf("%q → %v %q, ждали %v %q", c.in, amt, unit, c.amt, c.unit)
		}
	}
}

func TestParseAndClassify(t *testing.T) {
	got := Parse("Говядина на кости — 100 г\nСвёкла — 70 г\nВода — 400 мл\nСоль\nСметана — 1 ст. л.\nПеред сладкий — ½ шт")
	if len(got) != 6 || got[0].Name != "Говядина на кости" || got[0].Amount != 100 {
		t.Fatalf("разбор: %+v", got)
	}
	if !Skip("Вода") || Skip("Водоросли") {
		t.Error("вода не покупается")
	}
	if !Pantry("Соль") || !Pantry("Масло растительное") || !Pantry("Перец") || Pantry("Перец сладкий") || Pantry("Масло сливочное") {
		t.Error("«обычно есть дома»")
	}
	for name, want := range map[string]string{"Свёкла": CatProduce, "Говядина на кости": CatMeat, "Сметана": CatDairy,
		"Яйца": CatDairy, "Спагетти": CatGrocery, "Масло сливочное": CatDairy, "Курица на суп": CatMeat, "Мешки": CatOther} {
		if c := Category(name); c != want {
			t.Errorf("%s → %s, ждали %s", name, c, want)
		}
	}
}

func TestRoundFormat(t *testing.T) {
	if r := Round(210, "г"); r != 250 {
		t.Errorf("210 г → %v", r)
	}
	if r := Round(90, "г"); r != 90 {
		t.Errorf("90 г → %v", r)
	}
	if r := Round(1.5, "шт"); r != 2 {
		t.Errorf("1,5 шт → %v", r)
	}
	if f := Format(1500, "г"); f != "1,5 кг" {
		t.Errorf("1500 г → %q", f)
	}
	if f := Format(1000, "мл"); f != "1 л" {
		t.Errorf("1000 мл → %q", f)
	}
}

func TestNeedsMergesPiecesAndGrams(t *testing.T) {
	day := Day{Date: "2026-11-21", Meals: []DayMeal{{TaskID: 1, Meal: "Обед", Servings: 2, recipes: []models.Recipe{
		{ID: 1, Name: "Борщ", Ingredients: "Картофель — 1 шт (небольшой)\nЛук — 25 г\nВода — 400 мл"},
		{ID: 2, Name: "Курица", Ingredients: "Картофель — 200 г\nЛук — ½ шт\nСоль"},
	}}}}
	needs, pantry := Needs([]Day{day})
	got := map[string]string{}
	for _, n := range needs {
		got[n.Name] = Format(n.Amount, n.Unit)
	}
	// картофель: 1×2 шт + 200 г×2 / 100 г = 6 шт; лук: ½×2 шт + 25 г×2 / 100 г → 2 шт
	if len(needs) != 2 || got["Картофель"] != "6 шт" || got["Лук"] != "2 шт" {
		t.Errorf("сложение штук и граммов: %v", got)
	}
	if len(pantry) != 1 || pantry[0] != "Соль" {
		t.Errorf("есть дома: %v", pantry)
	}
}

func TestMainIngredient(t *testing.T) {
	// рецепты из базы (на 1 порцию)
	cases := []struct{ recipe, text, want string }{
		{"Запеченная курица с картофелем", "Курица (бёдра или голени) — 250 г\nКартофель — 200 г\nЛук — ½ шт", "Курица (бёдра или голени)"},
		{"Паста Карбонара", "Спагетти — 100 г\nБекон — 60 г\nЯйца — 1 шт\nПармезан — 30 г", "Бекон"},
		{"Борщ", "Говядина на кости — 100 г\nСвёкла — 70 г\nВода — 400 мл", "Говядина на кости"},
		{"Рыба с овощами на пару", "Рыба (филе) — 180 г\nБрокколи — 100 г\nМорковь — 50 г", "Рыба (филе)"},
		{"Блинчики с творогом", "Мука — 50 г\nЯйца — ½ шт\nМолоко — 100 мл\nТворог — 100 г\nСахар — 2 ч. л.", "Творог"},
		{"Овсяная каша с фруктами", "Овсяные хлопья — 50 г\nМолоко — 200 мл\nБанан — ½ шт", "Овсяные хлопья"},
		{"Яичница с тостами", "Яйца — 2 шт\nХлеб — 2 ломтика\nМасло сливочное — 10 г", "Яйца"},
		{"Овощное рагу", "Кабачок — 150 г\nБаклажан — 80 г\nСоль", "Кабачок"},
	}
	for _, c := range cases {
		ings := Parse(c.text)
		i := Main(c.recipe, ings)
		if i < 0 || ings[i].Name != c.want {
			t.Errorf("%s → %d, ждали %s", c.recipe, i, c.want)
		}
	}
	if Main("Бульон", Parse("Соль\nВода — 1 л")) != -1 {
		t.Error("без главного продукта")
	}
}

// Рецепты на проде — на английском, количество впереди; диапазоны и «~».
func TestParseLeadingQuantity(t *testing.T) {
	cases := []struct {
		line, name string
		amt        float64
		unit       string
	}{
		{"1/4 cup buckwheat groats", "buckwheat groats", 0.25, "стакан"},
		{"100 g cottage cheese", "cottage cheese", 100, "г"},
		{"1 ripe banana", "ripe banana", 1, ""},
		{"- 180 g red lentils, rinsed", "red lentils, rinsed", 180, "г"},
		{"1 1/2 cups whole milk", "whole milk", 1.5, "стакан"},
		{"2 tbsp olive oil", "olive oil", 2, "ст. л."},
		{"- Beef broth — ~3 liters", "Beef broth", 3000, "мл"},
		{"- Cooked beef — 300–400 g", "Cooked beef", 400, "г"},
		{"Fruit purée", "Fruit purée", 0, ""},
		{"Творог — 200 г", "Творог", 200, "г"},
	}
	for _, c := range cases {
		got := Parse(c.line + "\nСоль") // на проде — по продукту на строку
		if len(got) != 2 || got[0].Name != c.name || got[0].Amount != c.amt || got[0].Unit != c.unit {
			t.Errorf("%q → %+v, ждали %s %v %s", c.line, got, c.name, c.amt, c.unit)
		}
	}
}

// Рецепт на 2 порции, готовим 3 → продукты × 1,5.
func TestNeedsDividesByRecipeServings(t *testing.T) {
	r := models.Recipe{ID: 1, Name: "Old", Servings: 2, Ingredients: "Рис — 200 г"}
	needs, _ := Needs([]Day{{Date: "2026-11-21", Meals: []DayMeal{{TaskID: 1, Servings: 3, recipes: []models.Recipe{r}}}}})
	if len(needs) != 1 || needs[0].Amount != 300 {
		t.Fatalf("рис на 3 порции из рецепта на 2: %+v", needs)
	}
}
