package notify

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Params — данные для текста уведомления. Текст собирается при отправке,
// на языке получателя (users.locale), поэтому в очереди лежат только факты.
type Params struct {
	Actor     string   `json:"actor,omitempty"`      // кто сделал (имя)
	Name      string   `json:"name,omitempty"`       // имя получателя / нового участника / помощника в итоге дня
	Title     string   `json:"title,omitempty"`      // задача
	TaskType  string   `json:"task_type,omitempty"`  // meal|cleaning|childcare|custom — для перевода служебных названий
	Time      string   `json:"time,omitempty"`       // HH:MM задачи
	StartAt   string   `json:"start_at,omitempty"`   // «начать готовить примерно в»
	Day       string   `json:"day,omitempty"`        // today | tomorrow
	Lead      int      `json:"lead,omitempty"`       // за сколько минут напоминание
	Important bool     `json:"important,omitempty"`  // ★ высокая важность
	Count     int      `json:"count,omitempty"`      // задач / пунктов
	HighCount int      `json:"high_count,omitempty"` // важных задач
	FirstTime string   `json:"first_time,omitempty"`
	Items     []string `json:"items,omitempty"`      // пункты списка / несделанные задачи
	ItemTypes []string `json:"item_types,omitempty"` // типы задач для Items (итог дня)
	Done      int      `json:"done,omitempty"`
	Total     int      `json:"total,omitempty"`
	Left      int      `json:"left,omitempty"`
	At        string   `json:"at,omitempty"` // время отметки HH:MM
	Titles    []string `json:"titles,omitempty"`
}

const maxTitle = 60

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// pluralRu: 1 задача, 2 задачи, 5 задач.
func pluralRu(n int, one, few, many string) string {
	n10, n100 := n%10, n%100
	switch {
	case n10 == 1 && n100 != 11:
		return one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		return few
	default:
		return many
	}
}

func pluralEn(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func isRu(locale string) bool { return strings.HasPrefix(strings.ToLower(locale), "ru") }

// quoteList — «А», «Б» и ещё N. Одинаковые названия показываем один раз,
// «ещё N» считает все оставшиеся задачи.
func quoteList(items []string, max int, ru bool) string {
	var parts []string
	shown := map[string]bool{}
	covered := 0
	for _, it := range items {
		if shown[it] {
			covered++
			continue
		}
		if len(parts) == max {
			continue
		}
		shown[it] = true
		covered++
		if ru {
			parts = append(parts, "«"+clip(it, 30)+"»")
		} else {
			parts = append(parts, "“"+clip(it, 30)+"”")
		}
	}
	s := strings.Join(parts, ", ")
	if rest := len(items) - covered; rest > 0 {
		if ru {
			s += fmt.Sprintf(" и ещё %d", rest)
		} else {
			s += fmt.Sprintf(" and %d more", rest)
		}
	}
	return s
}

func plainList(items []string, max int) string {
	var parts []string
	for i, it := range items {
		if i == max {
			break
		}
		parts = append(parts, clip(it, 30))
	}
	s := strings.Join(parts, ", ")
	if len(items) > max {
		s += "…"
	}
	return s
}

// Render собирает заголовок и текст пуша на языке получателя (ru, иначе английский).
func Render(kind Kind, locale string, p Params) (title, body string) {
	ru := isRu(locale)
	task := clip(TaskTitle(p.TaskType, p.Title, locale), maxTitle)
	tomorrow := p.Day == "tomorrow"
	switch kind {
	case KindMorning:
		if ru {
			title = "Доброе утро, " + p.Name
			body = fmt.Sprintf("Сегодня %d %s", p.Count, pluralRu(p.Count, "задача", "задачи", "задач"))
			if p.HighCount > 0 {
				body += fmt.Sprintf(", %d %s", p.HighCount, pluralRu(p.HighCount, "важная", "важные", "важных"))
			}
			if task != "" {
				body += ". Первая — «" + task + "»"
				if p.FirstTime != "" {
					body += " в " + p.FirstTime
				}
			}
		} else {
			title = "Good morning, " + p.Name
			body = fmt.Sprintf("%d %s today", p.Count, pluralEn(p.Count, "task", "tasks"))
			if p.HighCount > 0 {
				body += fmt.Sprintf(", %d important", p.HighCount)
			}
			if task != "" {
				body += ". First: “" + task + "”"
				if p.FirstTime != "" {
					body += " at " + p.FirstTime
				}
			}
		}
	case KindReminder:
		if p.StartAt != "" {
			if ru {
				title = task + ": начать готовить в " + p.StartAt
				body = "Подать к " + p.Time
			} else {
				title = task + ": start cooking at " + p.StartAt
				body = "Serve at " + p.Time
			}
		} else {
			if ru {
				title = fmt.Sprintf("Через %d мин · %s", p.Lead, task)
				body = p.Time
			} else {
				title = fmt.Sprintf("In %d min · %s", p.Lead, task)
				body = p.Time
			}
		}
		if p.Important {
			if ru {
				body += " · важная задача"
			} else {
				body += " · important task"
			}
		}
	case KindTaskNew:
		// Имя не склоняем («от Анны» не собрать из любого имени) — «Анна: новая задача».
		switch {
		case ru && tomorrow:
			title = "новая задача на завтра"
		case ru:
			title = "новая задача на сегодня"
		case tomorrow:
			title = "new task for tomorrow"
		default:
			title = "new task for today"
		}
		if p.Actor != "" {
			title = p.Actor + ": " + title
		} else {
			title = strings.ToUpper(title[:1]) + title[1:]
		}
		body = task
		if p.Time != "" {
			body += " · " + p.Time
		}
	case KindTaskMoved:
		switch {
		case ru && p.Time == "":
			title = task + " — когда удобно"
		case ru && tomorrow:
			title = task + " — завтра в " + p.Time
		case ru:
			title = task + " — теперь в " + p.Time
		case p.Time == "":
			title = task + " — any time"
		case tomorrow:
			title = task + " — tomorrow at " + p.Time
		default:
			title = task + " — now at " + p.Time
		}
		if ru {
			body = "Новое время · " + p.Actor
		} else {
			body = "New time · " + p.Actor
		}
	case KindTaskCancelled:
		if ru {
			title = "Задача отменена"
			body = task + " — сегодня не нужно"
			if tomorrow {
				body = task + " — завтра не нужно"
			}
		} else {
			title = "Task cancelled"
			body = task + " — not needed today"
			if tomorrow {
				body = task + " — not needed tomorrow"
			}
		}
	case KindListClosed:
		if ru {
			title = "Покупки привезли"
			body = fmt.Sprintf("Куплено %d из %d", p.Done, p.Total)
			if p.Left > 0 {
				body += fmt.Sprintf(". Ещё %d — в новом списке", p.Left)
			}
		} else {
			title = "Groceries are in"
			body = fmt.Sprintf("%d of %d bought", p.Done, p.Total)
			if p.Left > 0 {
				body += fmt.Sprintf(". %d moved to a new list", p.Left)
			}
		}
	case KindListSent:
		if ru {
			title = p.Actor + ": список покупок"
			body = fmt.Sprintf("%d %s", p.Count, pluralRu(p.Count, "пункт", "пункта", "пунктов"))
		} else {
			title = p.Actor + ": shopping list"
			body = fmt.Sprintf("%d %s", p.Count, pluralEn(p.Count, "item", "items"))
		}
		if len(p.Items) > 0 {
			body += ": " + plainList(p.Items, 4)
		}
	case KindListAdded:
		if ru {
			title = p.Actor + ": дописано в список"
		} else {
			title = p.Actor + ": added to the list"
		}
		body = fmt.Sprintf("+%d: %s", len(p.Items), plainList(p.Items, 5))
	case KindDigest:
		if p.Done >= p.Total {
			if ru {
				title = p.Name + ": всё сделано"
				body = fmt.Sprintf("%d из %d", p.Done, p.Total)
			} else {
				title = p.Name + ": all done"
				body = fmt.Sprintf("%d of %d", p.Done, p.Total)
			}
		} else {
			items := make([]string, len(p.Items))
			for i, it := range p.Items {
				tt := ""
				if i < len(p.ItemTypes) {
					tt = p.ItemTypes[i]
				}
				items[i] = TaskTitle(tt, it, locale)
			}
			if ru {
				title = fmt.Sprintf("%s: сделано %d из %d", p.Name, p.Done, p.Total)
				body = "Не сделано: " + quoteList(items, 2, true)
			} else {
				title = fmt.Sprintf("%s: %d of %d done", p.Name, p.Done, p.Total)
				body = "Not done: " + quoteList(items, 2, false)
			}
		}
	case KindImportantDone:
		if ru {
			title = "Готово · " + task
			body = p.Actor + " · отмечено в " + p.At
		} else {
			title = "Done · " + task
			body = p.Actor + " · checked at " + p.At
		}
	case KindMemberJoined:
		if ru {
			title = p.Name + " теперь в доме"
			body = "Можно ставить задачи"
		} else {
			title = p.Name + " joined your home"
			body = "You can now assign tasks"
		}
	case KindQuietSummary:
		if ru {
			title = "Пока вы отдыхали"
		} else {
			title = "While you were resting"
		}
		body = strings.Join(p.Titles, "; ")
		if body != "" {
			body = strings.ToUpper(string([]rune(body)[:1])) + string([]rune(body)[1:])
		}
	case KindTest:
		if ru {
			title = "Проверка уведомлений"
			body = "Если вы это видите — всё работает"
		} else {
			title = "Test notification"
			body = "If you can see this, it works"
		}
	}
	return clip(title, 90), clip(body, 180)
}

// SummaryLine — строка для сводки «Пока вы отдыхали»: коротко и с названием задачи.
func SummaryLine(kind Kind, locale string, p Params) string {
	ru := isRu(locale)
	task := clip(TaskTitle(p.TaskType, p.Title, locale), 40)
	switch kind {
	case KindTaskNew:
		if ru {
			return "новая задача «" + task + "»"
		}
		return "new task “" + task + "”"
	case KindTaskCancelled:
		if ru {
			return "отменена «" + task + "»"
		}
		return "cancelled “" + task + "”"
	case KindTaskMoved:
		if ru {
			return "«" + task + "» в " + p.Time
		}
		return "“" + task + "” at " + p.Time
	case KindListClosed:
		if ru {
			return "покупки привезли"
		}
		return "groceries are in"
	case KindListSent:
		if ru {
			return "список покупок (" + p.Actor + ")"
		}
		return "shopping list from " + p.Actor
	}
	t, _ := Render(kind, locale, p)
	return t
}

// TaskTitle — название задачи для пуша: у системных задач (еда, уборка, ребёнок)
// заголовок в базе служебный, его переводим.
func TaskTitle(taskType, title, locale string) string {
	ru := isRu(locale)
	switch taskType {
	case "meal":
		if i := strings.Index(title, " - "); i > 0 {
			return title[:i]
		}
	case "cleaning":
		zone := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(title, "Уборка"), ":"))
		if ru {
			return "Уборка: " + zone
		}
		return "Cleaning: " + zone
	case "childcare":
		if ru {
			return "Уход за ребёнком"
		}
		return "Childcare"
	}
	return title
}
