package notify

import (
	"fmt"
	"strings"
)

// Тексты пушей на языках интерфейса. Английский и русский — здесь, остальные — texts_<код>.go
// (регистрируются в init). Ключи у всех языков одни и те же; нет перевода — берём английский.
// Параметры в {фигурных} скобках подставляет tr; формы по числу — через «|» (см. pluralIndex).
// Словарь бренда — docs/brand/voice.md фронта: «помощник», «ведёт дом», на «вы».

var catalogs = map[string]map[string]string{"en": catalogEn, "ru": catalogRu}

// Коды языка устройства, которые означают язык приложения: tl — филиппинский, in — индонезийский, be — русский.
var langAliases = map[string]string{"tl": "fil", "in": "id", "be": "ru"}

// Lang — код языка пушей по users.locale («ru», «uk-UA», «fil»); незнакомый — английский.
func Lang(locale string) string {
	base := strings.ToLower(locale)
	if i := strings.IndexAny(base, "-_"); i >= 0 {
		base = base[:i]
	}
	if a, ok := langAliases[base]; ok {
		base = a
	}
	if _, ok := catalogs[base]; ok {
		return base
	}
	return "en"
}

// pluralIndex — номер формы по числу, как в приложении (src/i18n/index.ts):
// ru, uk — 1 / 2–4 / 5+; id — одна форма; si — 0 и 1 / остальное; ar — 6 форм; остальные — 1 / остальное.
func pluralIndex(lang string, n int) int {
	if n < 0 {
		n = -n
	}
	switch lang {
	case "ru", "uk":
		n10, n100 := n%10, n%100
		switch {
		case n10 == 1 && n100 != 11:
			return 0
		case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
			return 1
		default:
			return 2
		}
	case "id":
		return 0
	case "si":
		if n <= 1 {
			return 0
		}
		return 1
	case "ar":
		// CLDR: 0 | 1 | 2 | 3–10 | 11–99 | 100, 101, 102…
		m100 := n % 100
		switch {
		case n == 0:
			return 0
		case n == 1:
			return 1
		case n == 2:
			return 2
		case m100 >= 3 && m100 <= 10:
			return 3
		case m100 >= 11:
			return 4
		}
		return 5
	}
	if n == 1 {
		return 0
	}
	return 1
}

func raw(lang, key string) string {
	if s, ok := catalogs[lang][key]; ok {
		return s
	}
	return catalogEn[key]
}

func fill(s string, kv []any) string {
	for i := 0; i+1 < len(kv); i += 2 {
		s = strings.ReplaceAll(s, "{"+fmt.Sprint(kv[i])+"}", fmt.Sprint(kv[i+1]))
	}
	return s
}

// tr — строка по ключу на языке lang; kv — пары «параметр, значение».
func tr(lang, key string, kv ...any) string { return fill(raw(lang, key), kv) }

// trN — строка с формами по числу n (подставляется в {n}).
func trN(lang, key string, n int, kv ...any) string {
	forms := strings.Split(raw(lang, key), "|")
	i := pluralIndex(lang, n)
	if i >= len(forms) {
		i = len(forms) - 1
	}
	return fill(forms[i], append([]any{"n", n}, kv...))
}

// quote — название в кавычках, принятых в языке.
func quote(lang, s string) string { return fill(raw(lang, "quote"), []any{"x", s}) }

var catalogEn = map[string]string{
	"quote":                   "“{x}”",
	"and_more":                " and {n} more",
	"reason.weekly":           "a day off",
	"reason.holiday":          "a holiday",
	"reason.vacation":         "on vacation",
	"reason.day_off":          "a day off",
	"morning.title":           "Good morning, {name}",
	"morning.count":           "{n} task today|{n} tasks today",
	"morning.high":            ", {n} important|, {n} important",
	"morning.first":           ". First: {task}",
	"morning.first_at":        " at {time}",
	"reminder.cook_title":     "{task}: start cooking at {time}",
	"reminder.cook_body":      "Serve at {time}",
	"reminder.title":          "In {n} min · {task}",
	"reminder.important":      " · important task",
	"task_new.today":          "New task for today",
	"task_new.tomorrow":       "New task for tomorrow",
	"task_new.today_by":       "{actor}: new task for today",
	"task_new.tomorrow_by":    "{actor}: new task for tomorrow",
	"task_moved.any":          "{task} — any time",
	"task_moved.tomorrow":     "{task} — tomorrow at {time}",
	"task_moved.now":          "{task} — now at {time}",
	"task_moved.body":         "New time · {actor}",
	"task_cancelled.title":    "Task cancelled",
	"task_cancelled.today":    "{task} — not needed today",
	"task_cancelled.tomorrow": "{task} — not needed tomorrow",
	"list_closed.title":       "Groceries are in",
	"list_closed.body":        "{done} of {total} bought",
	"list_closed.left":        ". {n} moved to a new list",
	"list_sent.title":         "{actor}: shopping list",
	"list_sent.count":         "{n} item|{n} items",
	"list_added.title":        "{actor}: added to the list",
	"digest.all_title":        "{name}: all done",
	"digest.all_body":         "{done} of {total}",
	"digest.title":            "{name}: {done} of {total} done",
	"digest.not_done":         "Not done: {list}",
	"important_done.title":    "Done · {task}",
	"important_done.body":     "{actor} · checked at {time}",
	"day_off.title":           "Tomorrow {name} is {reason}",
	"day_off.body":            "{n} recurring task will be yours: {list}|{n} recurring tasks will be yours: {list}",
	"member_joined.title":     "{name} joined your home team",
	"member_joined.body":      "You can now assign tasks",
	"quiet.title":             "While you were resting",
	"test.title":              "Test notification",
	"test.body":               "If you can see this, it works",
	"summary.task_new":        "new task {task}",
	"summary.cancelled":       "cancelled {task}",
	"summary.moved":           "{task} at {time}",
	"summary.list_closed":     "groceries are in",
	"summary.list_sent":       "shopping list from {actor}",
	"task_title.cleaning":     "Cleaning: {zone}",
	"task_title.childcare":    "Childcare",
}

var catalogRu = map[string]string{
	"quote":                   "«{x}»",
	"and_more":                " и ещё {n}",
	"reason.weekly":           "выходной",
	"reason.holiday":          "праздник",
	"reason.vacation":         "отпуск",
	"reason.day_off":          "отгул",
	"morning.title":           "Доброе утро, {name}",
	"morning.count":           "Сегодня {n} задача|Сегодня {n} задачи|Сегодня {n} задач",
	"morning.high":            ", {n} важная|, {n} важные|, {n} важных",
	"morning.first":           ". Первая — {task}",
	"morning.first_at":        " в {time}",
	"reminder.cook_title":     "{task}: начать готовить в {time}",
	"reminder.cook_body":      "Подать к {time}",
	"reminder.title":          "Через {n} мин · {task}",
	"reminder.important":      " · важная задача",
	"task_new.today":          "Новая задача на сегодня",
	"task_new.tomorrow":       "Новая задача на завтра",
	"task_new.today_by":       "{actor}: новая задача на сегодня",
	"task_new.tomorrow_by":    "{actor}: новая задача на завтра",
	"task_moved.any":          "{task} — когда удобно",
	"task_moved.tomorrow":     "{task} — завтра в {time}",
	"task_moved.now":          "{task} — теперь в {time}",
	"task_moved.body":         "Новое время · {actor}",
	"task_cancelled.title":    "Задача отменена",
	"task_cancelled.today":    "{task} — сегодня не нужно",
	"task_cancelled.tomorrow": "{task} — завтра не нужно",
	"list_closed.title":       "Покупки привезли",
	"list_closed.body":        "Куплено {done} из {total}",
	"list_closed.left":        ". Ещё {n} — в новом списке",
	"list_sent.title":         "{actor}: список покупок",
	"list_sent.count":         "{n} пункт|{n} пункта|{n} пунктов",
	"list_added.title":        "{actor}: дописано в список",
	"digest.all_title":        "{name}: всё сделано",
	"digest.all_body":         "{done} из {total}",
	"digest.title":            "{name}: сделано {done} из {total}",
	"digest.not_done":         "Не сделано: {list}",
	"important_done.title":    "Готово · {task}",
	"important_done.body":     "{actor} · отмечено в {time}",
	"day_off.title":           "{name}: завтра {reason}",
	"day_off.body":            "{n} регулярная задача перейдёт к вам: {list}|{n} регулярные задачи перейдут к вам: {list}|{n} регулярных задач перейдут к вам: {list}",
	"member_joined.title":     "{name} теперь в команде",
	"member_joined.body":      "Можно ставить задачи",
	"quiet.title":             "Пока вы отдыхали",
	"test.title":              "Проверка уведомлений",
	"test.body":               "Если вы это видите — всё работает",
	"summary.task_new":        "новая задача {task}",
	"summary.cancelled":       "отменена {task}",
	"summary.moved":           "{task} в {time}",
	"summary.list_closed":     "покупки привезли",
	"summary.list_sent":       "список покупок ({actor})",
	"task_title.cleaning":     "Уборка: {zone}",
	"task_title.childcare":    "Уход за ребёнком",
}
