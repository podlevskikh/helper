// Package recipeimport разбирает текст многих рецептов по шаблону (docs/work/recipes-import/PRD.md во фронте):
//
//	## Плов с курицей
//	Тип: Обед
//	Подготовка: 15 мин · Готовка: 45 мин
//	Ингредиенты:
//	Куриное бедро — 150 г
//	Как готовить:
//	1. …
//
// Подписи понимаются по-русски и по-английски. Разбор детерминированный: сервер разбирает текст и при
// предпросмотре, и при сохранении, клиенту поля не доверяем.
package recipeimport

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"podlevskikh/awesomeProject/internal/menu"
)

// Лимиты одной загрузки.
const (
	MaxRecipes = 50
	MaxBytes   = 200 * 1024
	MaxName    = 120
)

// Issue — ошибка или предупреждение по рецепту. Текст собирает клиент по коду (переводы — во фронте).
type Issue struct {
	Code  string   `json:"code"`
	Value string   `json:"value,omitempty"` // что написано: «час», «4», название ссылки
	Field string   `json:"field,omitempty"` // prep | cook | servings | link
	Names []string `json:"names,omitempty"` // ингредиенты без количества
	First int      `json:"first,omitempty"` // номер первого такого же рецепта в загрузке
}

// Коды ошибок (рецепт не сохраняется) и предупреждений (сохраняется).
const (
	ErrNoName        = "no_name"
	ErrNameTooLong   = "name_too_long"
	ErrNoIngredients = "no_ingredients"
	ErrBadNumber     = "bad_number"
	ErrRepeated      = "repeated" // такое же название выше в этой загрузке

	WarnServings       = "servings"        // не на 1 порцию — сохраним как есть
	WarnNoQty          = "no_qty"          // у ингредиентов нет количества
	WarnNoInstructions = "no_instructions" // нет «как готовить»
	WarnNoMain         = "no_main_product" // главный продукт не нашёлся
	WarnBadLink        = "bad_link"        // ссылка не http(s) — не сохраним
)

// Item — один рецепт из текста.
type Item struct {
	Index        int     `json:"index"` // 1, 2, … — номер в загрузке
	Line         int     `json:"line"`  // строка, где начинается рецепт
	Name         string  `json:"name"`
	DishType     string  `json:"dish_type_name,omitempty"` // как написано в тексте
	Description  string  `json:"description"`
	PrepTime     int     `json:"prep_time"`
	CookTime     int     `json:"cook_time"`
	Servings     int     `json:"servings"`
	Tags         string  `json:"tags"`
	VideoURL     string  `json:"video_url"`
	SourceURL    string  `json:"source_url"`
	Ingredients  string  `json:"ingredients"` // «Продукт — 170 г» по строке
	Instructions string  `json:"instructions"`
	IngCount     int     `json:"ingredient_count"`
	MainProduct  string  `json:"main_product,omitempty"`
	Errors       []Issue `json:"errors"`
	Warnings     []Issue `json:"warnings"`
}

// OK — рецепт можно сохранить.
func (it Item) OK() bool { return len(it.Errors) == 0 }

type key int

const (
	kNone key = iota
	kName
	kType
	kDescription
	kServings
	kPrep
	kCook
	kTotal
	kTags
	kVideo
	kLink
	kIngredients
	kInstructions
)

var keys = map[string]key{
	"название": kName, "name": kName, "title": kName, "recipe": kName, "рецепт": kName,
	"тип": kType, "тип блюда": kType, "type": kType, "dish type": kType, "category": kType, "категория": kType,
	"описание": kDescription, "description": kDescription, "about": kDescription,
	"порций": kServings, "порции": kServings, "порция": kServings, "servings": kServings, "serves": kServings,
	"portions":   kServings,
	"подготовка": kPrep, "время подготовки": kPrep, "prep": kPrep, "prep time": kPrep, "preparation": kPrep,
	"preparation time": kPrep,
	"готовка":          kCook, "время готовки": kCook, "время приготовления": kCook, "cooking": kCook, "cook": kCook,
	"cook time": kCook, "cooking time": kCook,
	"время": kTotal, "всего": kTotal, "total": kTotal, "total time": kTotal, "time": kTotal,
	"теги": kTags, "тэги": kTags, "tags": kTags,
	"видео": kVideo, "video": kVideo,
	"ссылка": kLink, "источник": kLink, "link": kLink, "source": kLink, "url": kLink,
	"ингредиенты": kIngredients, "состав": kIngredients, "продукты": kIngredients, "ingredients": kIngredients,
	"как готовить": kInstructions, "приготовление": kInstructions, "способ приготовления": kInstructions,
	"инструкция": kInstructions, "шаги": kInstructions, "how to cook": kInstructions, "instructions": kInstructions,
	"method": kInstructions, "directions": kInstructions, "steps": kInstructions, "preparation steps": kInstructions,
}

var (
	headingRe = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*$`)
	numberRe  = regexp.MustCompile(`^\(?\d{1,3}[.)]\s*`)
	keyRe     = regexp.MustCompile(`^\**([\p{L} ]{2,30}?)\**\s*:\s*\**(.*)$`)
	bulletRe  = regexp.MustCompile(`^(?:[-•·*–—]|\d{1,2}[.)])\s+`)
	ruleRe    = regexp.MustCompile(`^\s*(?:-{3,}|\*{3,}|_{3,}|={3,})\s*$`)
	hoursRe   = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(?:ч|час|часа|часов|h|hr|hrs|hour|hours)\.?(?:\P{L}|$)`)
	minsRe    = regexp.MustCompile(`(?i)(\d+)\s*(?:м|мин|минут|минуты|минута|m|min|mins|minute|minutes)\.?(?:\P{L}|$)`)
	numRe     = regexp.MustCompile(`\d+`)
)

// lookupKey — подпись строки «Ключ: значение», если ключ знакомый.
func lookupKey(line string) (key, string, bool) {
	m := keyRe.FindStringSubmatch(line)
	if m == nil {
		return kNone, "", false
	}
	k, ok := keys[strings.ToLower(strings.Join(strings.Fields(m[1]), " "))]
	if !ok {
		return kNone, "", false
	}
	return k, strings.TrimSpace(strings.Trim(strings.TrimSpace(m[2]), "*")), true
}

// metaParts — «Servings: 1 · Prep: 10 min · Cooking: 15 min» → пары; nil, если хоть одна часть не подпись.
func metaParts(line string) [][2]string {
	parts := regexp.MustCompile(`\s+[·|•]\s+`).Split(line, -1)
	var out [][2]string
	for _, p := range parts {
		k, v, ok := lookupKey(strings.TrimSpace(p))
		if !ok {
			return nil
		}
		out = append(out, [2]string{strconv.Itoa(int(k)), v})
	}
	return out
}

type block struct {
	line     int
	name     string
	isRecipe bool // была хоть одна подпись или раздел — иначе это заголовок документа, пропускаем
	meta     map[key]string
	desc     []string
	ings     []string
	steps    []string
}

// Parse разбирает текст на рецепты. Пустой текст — пустой список.
func Parse(text string) []Item {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")

	var blocks []*block
	var cur *block
	section := kNone
	start := func(n int, name string) {
		cur = &block{line: n, name: name, meta: map[key]string{}}
		blocks = append(blocks, cur)
		section = kNone
	}
	hasHeading := false
	for _, l := range lines {
		if headingRe.MatchString(strings.TrimSpace(l)) {
			hasHeading = true
			break
		}
	}

	for i, raw := range lines {
		n := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if ruleRe.MatchString(line) {
			continue
		}
		if m := headingRe.FindStringSubmatch(line); m != nil {
			start(n, cleanName(m[1]))
			continue
		}
		if cur == nil {
			if !hasHeading {
				// Один рецепт без «#»: первая строка — название, если это не подпись.
				if k, v, ok := lookupKey(line); ok && k == kName {
					start(n, cleanName(v))
					cur.isRecipe = true
					continue
				}
				if _, _, ok := lookupKey(line); !ok {
					start(n, cleanName(line))
					continue
				}
				start(n, "")
			} else {
				continue // текст до первого заголовка
			}
		}

		// Разделы «Ингредиенты» и «Как готовить» меняет только подпись раздела.
		if k, v, ok := lookupKey(line); ok && (k == kIngredients || k == kInstructions) {
			cur.isRecipe = true
			section = k
			if v != "" {
				if k == kIngredients {
					cur.ings = append(cur.ings, v)
				} else {
					cur.steps = append(cur.steps, v)
				}
			}
			continue
		}
		switch section {
		case kIngredients:
			// В списке ингредиентов разрешены только служебные строки вида «Теги: …» — иначе это продукт.
			if parts := metaParts(line); parts != nil && !strings.Contains(line, "—") {
				applyMeta(cur, parts)
				continue
			}
			cur.ings = append(cur.ings, line)
			continue
		case kInstructions:
			cur.steps = append(cur.steps, strings.TrimRight(raw, " \t"))
			continue
		}
		if parts := metaParts(line); parts != nil {
			applyMeta(cur, parts)
			continue
		}
		cur.desc = append(cur.desc, line)
	}

	var items []Item
	for _, b := range blocks {
		if !b.isRecipe && len(blocks) > 1 {
			continue // заголовок документа, примечание
		}
		items = append(items, b.item(len(items)+1))
	}
	markRepeats(items)
	return items
}

func applyMeta(b *block, parts [][2]string) {
	for _, p := range parts {
		k, _ := strconv.Atoi(p[0])
		kk := key(k)
		b.isRecipe = true
		if kk == kName {
			if b.name == "" {
				b.name = cleanName(p[1])
			}
			continue
		}
		b.meta[kk] = p[1]
	}
}

// cleanName убирает нумерацию «1.» и разметку.
func cleanName(s string) string {
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "*_"))
	s = numberRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func (b *block) item(idx int) Item {
	it := Item{Index: idx, Line: b.line, Name: b.name, Servings: 1, Errors: []Issue{}, Warnings: []Issue{}}
	if it.Name == "" {
		it.Errors = append(it.Errors, Issue{Code: ErrNoName})
	} else if len([]rune(it.Name)) > MaxName {
		it.Errors = append(it.Errors, Issue{Code: ErrNameTooLong})
	}

	it.DishType = strings.TrimSpace(b.meta[kType])
	desc := strings.TrimSpace(b.meta[kDescription])
	if len(b.desc) > 0 {
		desc = strings.TrimSpace(strings.TrimSpace(desc + "\n" + strings.Join(b.desc, "\n")))
	}
	it.Description = desc

	minutes := func(field string, k key) int {
		v, ok := b.meta[k]
		if !ok || strings.TrimSpace(v) == "" {
			return 0
		}
		m, ok := Minutes(v)
		if !ok {
			it.Errors = append(it.Errors, Issue{Code: ErrBadNumber, Field: field, Value: v})
		}
		return m
	}
	it.PrepTime = minutes("prep", kPrep)
	it.CookTime = minutes("cook", kCook)
	if it.PrepTime == 0 && it.CookTime == 0 {
		it.CookTime = minutes("cook", kTotal)
	}
	if v, ok := b.meta[kServings]; ok && strings.TrimSpace(v) != "" {
		if s := numRe.FindString(v); s != "" {
			n, _ := strconv.Atoi(s)
			if n > 0 && n <= 100 {
				it.Servings = n
			} else {
				it.Errors = append(it.Errors, Issue{Code: ErrBadNumber, Field: "servings", Value: v})
			}
		} else {
			it.Errors = append(it.Errors, Issue{Code: ErrBadNumber, Field: "servings", Value: v})
		}
	}
	if it.Servings != 1 {
		it.Warnings = append(it.Warnings, Issue{Code: WarnServings, Value: strconv.Itoa(it.Servings)})
	}

	var tags []string
	for _, tg := range strings.Split(b.meta[kTags], ",") {
		if tg = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tg), "#")); tg != "" {
			tags = append(tags, tg)
		}
	}
	it.Tags = strings.Join(tags, ", ")

	if v := strings.TrimSpace(b.meta[kVideo]); v != "" {
		if u, ok := CleanURL(v); ok {
			it.VideoURL = u
		} else {
			it.Warnings = append(it.Warnings, Issue{Code: WarnBadLink, Field: "video", Value: v})
		}
	}
	if v := strings.TrimSpace(b.meta[kLink]); v != "" {
		if u, ok := CleanURL(v); !ok {
			it.Warnings = append(it.Warnings, Issue{Code: WarnBadLink, Field: "link", Value: v})
		} else if IsVideo(u) && it.VideoURL == "" {
			it.VideoURL = u
		} else {
			it.SourceURL = u
		}
	}

	// Ингредиенты → «Продукт — количество» по строке.
	lines, parsed, noQty := ingredients(b.ings)
	it.Ingredients = strings.Join(lines, "\n")
	it.IngCount = len(lines)
	if it.IngCount == 0 {
		it.Errors = append(it.Errors, Issue{Code: ErrNoIngredients})
	} else {
		if len(noQty) > 0 {
			it.Warnings = append(it.Warnings, Issue{Code: WarnNoQty, Names: noQty})
		}
		if i := menu.Main(it.Name, parsed); i >= 0 {
			it.MainProduct = parsed[i].Name
		} else {
			it.Warnings = append(it.Warnings, Issue{Code: WarnNoMain})
		}
	}

	it.Instructions = strings.TrimSpace(trimBlank(b.steps))
	if it.Instructions == "" {
		it.Warnings = append(it.Warnings, Issue{Code: WarnNoInstructions})
	}
	return it
}

// ingredients — строки списка → «Продукт — количество», разобранные продукты и те, у которых нет количества.
func ingredients(raw []string) (lines []string, parsed []menu.Ingredient, noQty []string) {
	for _, l := range raw {
		l = strings.TrimSpace(bulletRe.ReplaceAllString(strings.TrimSpace(l), ""))
		if l == "" {
			continue
		}
		ps := menu.Parse(l)
		if len(ps) == 0 {
			continue
		}
		in := ps[0]
		if in.Raw == "" {
			// «Мука: 100 г» — двоеточие как разделитель.
			if i := strings.LastIndex(l, ":"); i > 0 && strings.TrimSpace(l[i+1:]) != "" {
				if q := menu.Parse("x — " + strings.TrimSpace(l[i+1:])); len(q) > 0 {
					in.Name, in.Raw, in.Amount, in.Unit = strings.TrimSpace(l[:i]), q[0].Raw, q[0].Amount, q[0].Unit
				}
			}
		}
		parsed = append(parsed, in)
		if in.Raw == "" {
			lines = append(lines, in.Name)
			if !menu.Skip(in.Name) && !menu.Pantry(in.Name) && !toTaste[strings.ToLower(in.Name)] {
				noQty = append(noQty, in.Name)
			}
		} else {
			lines = append(lines, in.Name+" — "+in.Raw)
		}
	}
	return lines, parsed, noQty
}

// NormalizeIngredients — список продуктов (как пишут люди или Claude) → строки «Продукт — 170 г» для рецепта.
func NormalizeIngredients(raw []string) []string {
	var all []string
	for _, r := range raw {
		all = append(all, strings.Split(strings.ReplaceAll(r, "\r", ""), "\n")...)
	}
	lines, _, _ := ingredients(all)
	return lines
}

// trimBlank — строки шагов без хвостовых пустых, отступы как в исходнике.
func trimBlank(lines []string) string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimSpace(l))
	}
	return strings.Join(out, "\n")
}

// toTaste — без количества это нормально («Соль», «Перец»), предупреждать не о чем.
var toTaste = map[string]bool{"соль": true, "перец": true, "вода": true, "зелень": true, "специи": true,
	"salt": true, "pepper": true, "black pepper": true, "water": true, "salt and pepper": true, "herbs": true, "spices": true}

// markRepeats — второй и дальше рецепт с тем же названием в одной загрузке — ошибка.
func markRepeats(items []Item) {
	seen := map[string]int{}
	for i := range items {
		k := NormalizeName(items[i].Name)
		if k == "" {
			continue
		}
		if first, ok := seen[k]; ok {
			items[i].Errors = append(items[i].Errors, Issue{Code: ErrRepeated, First: first})
			continue
		}
		seen[k] = items[i].Index
	}
}

// NormalizeName — ключ для поиска дублей: регистр, «ё» = «е», нумерация, знаки препинания и лишние пробелы не важны.
func NormalizeName(s string) string {
	s = strings.ToLower(cleanName(s))
	s = strings.ReplaceAll(s, "ё", "е")
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

// Minutes — «15 мин», «1 ч 30 мин», «1.5 h», «25 min (includes 20 min soaking)», «40» → минуты.
func Minutes(v string) (int, bool) {
	v = strings.TrimSpace(v)
	total := 0.0
	found := false
	if m := hoursRe.FindStringSubmatch(v); m != nil {
		h, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		total += h * 60
		found = true
		v = strings.Replace(v, m[0], "", 1)
	}
	if m := minsRe.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		total += float64(n)
		found = true
	}
	if !found {
		s := numRe.FindString(v)
		if s == "" {
			return 0, false
		}
		n, _ := strconv.Atoi(s)
		total = float64(n)
	}
	if total < 0 || total > 24*60 {
		return 0, false
	}
	return int(total + 0.5), true
}

// CleanURL — ссылка http(s) без мусора по краям; ok = false, если это не ссылка.
func CleanURL(v string) (string, bool) {
	v = strings.Trim(strings.TrimSpace(v), "<>()")
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(v) > 2000 {
		return "", false
	}
	return v, true
}

// IsVideo — ссылка на видеосервис: такая идёт в «Видео», остальные — в «Источник».
func IsVideo(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	h := strings.TrimPrefix(strings.ToLower(p.Hostname()), "www.")
	h = strings.TrimPrefix(h, "m.")
	for _, v := range []string{"youtube.com", "youtu.be", "rutube.ru", "vimeo.com", "tiktok.com", "instagram.com", "vkvideo.ru"} {
		if h == v || strings.HasSuffix(h, "."+v) {
			return true
		}
	}
	return false
}
