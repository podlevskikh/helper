package recipeimport

import (
	"strings"
	"testing"
)

// RIMP-03: шаблон на английском, как готовят рецепты в чате Claude: заголовок документа, «·» в одной строке,
// нумерация в названии, ссылка на сайт → «Источник», на видео → «Видео».
const english = `# Recipes for the Helper app (English, 1 serving)

Note: ingredient lines contain no commas.

---

## 1. Garlic Butter Shrimp Noodles

Description: Egg noodles with shrimp in a garlic-butter sauce.
Servings: 1 · Prep: 10 min · Cooking: 15 min
Tags: asian, noodles, shrimp, quick
Link: https://example.com/shrimp-noodles/

Ingredients:
Egg noodles — 90 g
Shrimp (peeled) — 120 g
Butter — 1 tbsp
Garlic — 2 cloves
Salt
Lime wedge — to serve

How to cook:
IMPORTANT: no salt anywhere.
1. Sauce: mix soy sauce and water.
2. Cook the noodles.

---

## 2. Shrimp Pad Thai

Type: dinner
Servings: 1 · Prep: 25 min (includes 20 min soaking) · Cooking: 10 min
Video: https://youtu.be/abc

Ingredients:
- Rice noodles — 60 g
- 100 g shrimp

How to cook:
Soak the noodles.
`

func TestParseEnglishTemplate(t *testing.T) {
	items := Parse(english)
	if len(items) != 2 {
		t.Fatalf("ждали 2 рецепта (заголовок документа пропускаем), получили %d: %+v", len(items), items)
	}
	a := items[0]
	if a.Index != 1 || a.Name != "Garlic Butter Shrimp Noodles" || a.Description != "Egg noodles with shrimp in a garlic-butter sauce." {
		t.Fatalf("название/описание: %+v", a)
	}
	if a.PrepTime != 10 || a.CookTime != 15 || a.Servings != 1 || a.Tags != "asian, noodles, shrimp, quick" {
		t.Fatalf("время/порции/теги: %+v", a)
	}
	if a.SourceURL != "https://example.com/shrimp-noodles/" || a.VideoURL != "" {
		t.Fatalf("ссылка на сайт → источник: %+v", a)
	}
	if a.IngCount != 6 || !strings.HasPrefix(a.Ingredients, "Egg noodles — 90 g\nShrimp (peeled) — 120 g\n") ||
		!strings.Contains(a.Ingredients, "\nSalt\n") {
		t.Fatalf("ингредиенты: %q", a.Ingredients)
	}
	if !strings.HasPrefix(a.Instructions, "IMPORTANT: no salt anywhere.\n1. Sauce") {
		t.Fatalf("как готовить: %q", a.Instructions)
	}
	if a.MainProduct != "Shrimp (peeled)" {
		t.Fatalf("главный продукт: %q", a.MainProduct)
	}
	if !a.OK() || len(a.Warnings) != 0 {
		t.Fatalf("соль без количества — не предупреждение, ошибок нет: %+v %+v", a.Errors, a.Warnings)
	}

	b := items[1]
	if b.Name != "Shrimp Pad Thai" || b.DishType != "dinner" || b.PrepTime != 25 || b.CookTime != 10 {
		t.Fatalf("второй: %+v", b)
	}
	if b.VideoURL != "https://youtu.be/abc" || b.SourceURL != "" {
		t.Fatalf("видео: %+v", b)
	}
	if b.Ingredients != "Rice noodles — 60 g\nshrimp — 100 g" {
		t.Fatalf("маркеры и количество впереди: %q", b.Ingredients)
	}
}

// RIMP-03: шаблон по-русски, ошибки и предупреждения по рецептам, повтор в загрузке.
func TestParseRussianIssues(t *testing.T) {
	text := `## Плов с курицей
Тип: Обед
Подготовка: 15 мин · Готовка: 1 ч 30 мин
Порций: 4
Источник: www.example.ru/plov

Ингредиенты:
Куриное бедро — 600 г
Рис — 280 г
Морковь
Мука: 100 г

Приготовление:
Обжарить.

## Сырники
Тип: Завтрак
Ингредиенты:

Как готовить:
Смешать.

## Борщ
Готовка: долго
Ингредиенты:
Свёкла — 70 г

### 3. плов С КУРИЦЕЙ!
Ингредиенты:
Рис — 80 г
`
	items := Parse(text)
	if len(items) != 4 {
		t.Fatalf("ждали 4, получили %d", len(items))
	}
	p := items[0]
	if p.CookTime != 90 || p.PrepTime != 15 || p.Servings != 4 || !p.OK() {
		t.Fatalf("плов: %+v", p)
	}
	if p.Ingredients != "Куриное бедро — 600 г\nРис — 280 г\nМорковь\nМука — 100 г" {
		t.Fatalf("ингредиенты плова: %q", p.Ingredients)
	}
	codes := func(is []Issue) string {
		var s []string
		for _, i := range is {
			s = append(s, i.Code)
		}
		return strings.Join(s, ",")
	}
	if got := codes(p.Warnings); got != "servings,bad_link,no_qty" {
		t.Fatalf("предупреждения плова: %s %+v", got, p.Warnings)
	}
	if p.Warnings[2].Names[0] != "Морковь" || p.MainProduct != "Куриное бедро" || p.Instructions != "Обжарить." {
		t.Fatalf("плов: %+v", p)
	}
	if got := codes(items[1].Errors); got != "no_ingredients" {
		t.Fatalf("сырники без ингредиентов: %s", got)
	}
	if got := codes(items[2].Errors); got != "bad_number" || items[2].Errors[0].Field != "cook" || items[2].Errors[0].Value != "долго" {
		t.Fatalf("борщ: %+v", items[2].Errors)
	}
	if got := codes(items[2].Warnings); got != "no_instructions" {
		t.Fatalf("борщ без «как готовить»: %s", got)
	}
	if e := items[3].Errors; len(e) != 1 || e[0].Code != ErrRepeated || e[0].First != 1 {
		t.Fatalf("повтор плова: %+v", e)
	}
}

// Один рецепт без «#»: первая строка — название.
func TestParseSingleWithoutHeading(t *testing.T) {
	items := Parse("Омлет\nIngredients:\nEggs — 2 pcs\nMilk — 50 ml\nHow to cook:\nWhisk and fry.")
	if len(items) != 1 || items[0].Name != "Омлет" || items[0].IngCount != 2 || !items[0].OK() {
		t.Fatalf("%+v", items)
	}
	if len(Parse("   \n\n")) != 0 {
		t.Fatal("пустой текст — ни одного рецепта")
	}
}

func TestNormalizeName(t *testing.T) {
	for _, c := range [][2]string{
		{"1. Борщ", "борщ"}, {"  ПЛОВ   с курицей!", "плов с курицей"}, {"Ёжики", "ежики"}, {"Pad-Thai", "pad thai"},
	} {
		if got := NormalizeName(c[0]); got != c[1] {
			t.Errorf("%q → %q, ждали %q", c[0], got, c[1])
		}
	}
}

func TestMinutes(t *testing.T) {
	for _, c := range []struct {
		in string
		n  int
		ok bool
	}{{"15 мин", 15, true}, {"1 ч 30 мин", 90, true}, {"1.5 h", 90, true}, {"40", 40, true},
		{"25 min (includes 20 min soaking)", 25, true}, {"час", 0, false}, {"2 hours", 120, true}} {
		n, ok := Minutes(c.in)
		if n != c.n || ok != c.ok {
			t.Errorf("%q → %d %v", c.in, n, ok)
		}
	}
}
