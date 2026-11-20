// Package menu собирает список покупок из меню на дни вперёд: разбирает ингредиенты
// рецептов («Творог — 100 г»), умножает на порции, складывает одинаковые продукты.
package menu

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Ingredient — одна строка рецепта на 1 порцию.
type Ingredient struct {
	Name   string  // «Творог»
	Amount float64 // 100; 0 — количество не указано («Соль»)
	Unit   string  // г, мл, шт, ч. л., ст. л., зубч., ломт., пучок, стакан…; "" — без единицы
	Raw    string  // как написано в рецепте: «100 г»
}

var sepRe = regexp.MustCompile(`\s+[-–—]\s+`)

// Parse разбирает текст ингредиентов: по продукту на строку («Творог — 100 г»);
// старые рецепты — через запятую («Молоко - 2 стакана, Банан - 1 шт»).
func Parse(text string) []Ingredient {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil
	}
	var lines []string
	if strings.Contains(s, "\n") {
		lines = strings.Split(s, "\n")
	} else {
		lines = regexp.MustCompile(`,\s+`).Split(s, -1)
	}
	out := []Ingredient{}
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-•·*"))
		if line == "" {
			continue
		}
		name, qty := line, ""
		if loc := sepRe.FindStringIndex(line); loc != nil {
			name, qty = strings.TrimSpace(line[:loc[0]]), strings.TrimSpace(line[loc[1]:])
		}
		amt, unit := parseQty(qty)
		out = append(out, Ingredient{Name: name, Amount: amt, Unit: unit, Raw: qty})
	}
	return out
}

var fracs = map[string]float64{"½": 0.5, "¼": 0.25, "¾": 0.75, "⅓": 1.0 / 3, "⅔": 2.0 / 3}

var numRe = regexp.MustCompile(`^(\d+(?:[.,]\d+)?)?\s*([½¼¾⅓⅔])?(?:\s*/\s*(\d+))?`)

// parseQty: «100 г» → 100, г; «1,5 кг» → 1500, г; «½ шт» → 0.5, шт; «1 шт (небольшой)» → 1, шт.
func parseQty(q string) (float64, string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return 0, ""
	}
	m := numRe.FindStringSubmatch(q)
	if m == nil || (m[1] == "" && m[2] == "") {
		return 0, ""
	}
	var amt float64
	if m[1] != "" {
		amt, _ = strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
	}
	if m[2] != "" {
		amt += fracs[m[2]]
	}
	if m[3] != "" && m[2] == "" { // «1/2»
		if d, _ := strconv.ParseFloat(m[3], 64); d > 0 {
			amt /= d
		}
	}
	rest := strings.TrimSpace(q[len(m[0]):])
	if i := strings.Index(rest, "("); i >= 0 {
		rest = strings.TrimSpace(rest[:i])
	}
	unit, mul := normUnit(rest)
	return amt * mul, unit
}

// normUnit приводит единицу к короткой форме; килограммы и литры — к граммам и миллилитрам.
func normUnit(u string) (string, float64) {
	l := strings.ToLower(strings.TrimSpace(u))
	l = strings.ReplaceAll(l, " ", "")
	switch {
	case l == "":
		return "", 1
	case l == "г" || l == "гр" || l == "гр." || strings.HasPrefix(l, "грамм"):
		return "г", 1
	case l == "кг" || strings.HasPrefix(l, "килограмм"):
		return "г", 1000
	case l == "мл":
		return "мл", 1
	case l == "л" || strings.HasPrefix(l, "литр"):
		return "мл", 1000
	case l == "шт" || l == "шт." || strings.HasPrefix(l, "штук"):
		return "шт", 1
	case strings.HasPrefix(l, "ч.л") || strings.HasPrefix(l, "чл") || strings.HasPrefix(l, "чайн"):
		return "ч. л.", 1
	case strings.HasPrefix(l, "ст.л") || strings.HasPrefix(l, "стл") || strings.HasPrefix(l, "столов"):
		return "ст. л.", 1
	case strings.HasPrefix(l, "зуб"):
		return "зубч.", 1
	case strings.HasPrefix(l, "ломт"):
		return "ломт.", 1
	case strings.HasPrefix(l, "пуч"):
		return "пучок", 1
	case strings.HasPrefix(l, "стакан"):
		return "стакан", 1
	case strings.HasPrefix(l, "щепот"):
		return "щепотка", 1
	case strings.HasPrefix(l, "гол"):
		return "гол.", 1
	}
	return strings.TrimSpace(u), 1
}

// Round округляет вверх до удобного для покупки: граммы — до 50 (меньше 100 г — до 10),
// миллилитры — до 50, остальное — до целого.
func Round(amt float64, unit string) float64 {
	if amt <= 0 {
		return 0
	}
	switch unit {
	case "г", "мл":
		step := 50.0
		if amt < 100 {
			step = 10
		}
		return math.Ceil(amt/step-1e-9) * step
	}
	return math.Ceil(amt - 1e-9)
}

// Format: 1500 г → «1,5 кг», 1000 мл → «1 л», 3 шт → «3 шт».
func Format(amt float64, unit string) string {
	if amt <= 0 {
		return ""
	}
	num := func(v float64) string {
		s := strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
		return strings.Replace(s, ".", ",", 1)
	}
	switch {
	case unit == "г" && amt >= 1000:
		return num(amt/1000) + " кг"
	case unit == "мл" && amt >= 1000:
		return num(amt/1000) + " л"
	case unit == "":
		return num(amt)
	}
	return num(amt) + " " + unit
}

// Key — по нему складываем одинаковые продукты: название без регистра и уточнений в скобках.
func Key(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if i := strings.Index(n, "("); i > 0 {
		n = strings.TrimSpace(n[:i])
	}
	return strings.ReplaceAll(n, "ё", "е")
}

// Skip — то, что не покупают (вода).
func Skip(name string) bool {
	k := Key(name)
	return k == "вода" || strings.HasPrefix(k, "вода ")
}

var pantry = []string{"соль", "перец черный", "черный перец", "перец молотый", "специи", "сахар", "мука",
	"масло растительное", "масло оливковое", "растительное масло", "оливковое масло", "томатная паста",
	"мед", "уксус", "сода", "разрыхлитель", "лавровый лист"}

// Pantry — обычно есть дома: в список не кладём, показываем «проверьте».
func Pantry(name string) bool {
	k := Key(name)
	for _, p := range pantry {
		if k == p || strings.HasPrefix(k, p+" ") || strings.HasPrefix(k, p+",") {
			return true
		}
	}
	return k == "перец" // «Перец» без уточнения — специя; «Перец сладкий» — овощ
}

// Отделы магазина — значения поля ShoppingListItem.Category.
const (
	CatProduce = "produce" // овощи и фрукты
	CatMeat    = "meat"    // мясо и рыба
	CatDairy   = "dairy"   // молочное и яйца
	CatGrocery = "grocery" // хлеб и крупы, макароны
	CatOther   = "other"
)

var cats = []struct {
	cat   string
	words []string
}{
	{CatDairy, []string{"молок", "сливк", "сметан", "творог", "сыр", "пармезан", "моцарел", "йогурт", "кефир", "масло сливоч", "сливочное масло", "яйц", "яйко"}},
	{CatMeat, []string{"мяс", "говяд", "свин", "баран", "кури", "индей", "фарш", "рыб", "лосос", "семг", "треск", "тунец", "бекон", "ветчин", "колбас", "сосиск", "креветк", "филе"}},
	{CatGrocery, []string{"хлеб", "батон", "лаваш", "булк", "тост", "спагетти", "макарон", "паста", "лапш", "рис", "греч", "овсян", "хлопь", "круп", "булгур", "киноа", "кускус"}},
	{CatProduce, []string{"картоф", "морков", "лук", "чеснок", "свекл", "капуст", "брокколи", "кабач", "цукин", "баклаж", "перец", "помидор", "томат", "огур", "зелен", "укроп", "петруш", "кинз", "базилик", "салат", "шпинат", "лимон", "лайм", "банан", "ягод", "яблок", "груш", "апельсин", "фрукт", "овощ", "гриб", "шампиньон", "тыкв", "авокадо"}},
}

// Category — отдел магазина по названию продукта.
func Category(name string) string {
	k := Key(name)
	for _, c := range cats {
		for _, w := range c.words {
			if strings.Contains(k, w) {
				return c.cat
			}
		}
	}
	return CatOther
}

// Примерный вес одной штуки — чтобы «Картофель — 200 г» и «Картофель — 1 шт» из разных рецептов
// складывались в одну строку (в штуках).
var pieceGrams = []struct {
	prefix string
	grams  float64
}{
	{"картоф", 100}, {"лук", 100}, {"морков", 80}, {"помидор", 120}, {"томат", 120}, {"перец слад", 150},
	{"кабач", 300}, {"баклаж", 250}, {"лимон", 100}, {"банан", 120}, {"яблок", 150}, {"огур", 120}, {"свекл", 200},
}

// PieceGrams — вес штуки в граммах; 0 — не знаем.
func PieceGrams(name string) float64 {
	k := Key(name)
	for _, p := range pieceGrams {
		if strings.HasPrefix(k, p.prefix) {
			return p.grams
		}
	}
	return 0
}
