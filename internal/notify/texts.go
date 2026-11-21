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
	Reason    string   `json:"reason,omitempty"` // почему помощник отдыхает: weekly | holiday | vacation | day_off
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

// quoteList — «А», «Б» и ещё N. Одинаковые названия показываем один раз,
// «ещё N» считает все оставшиеся задачи.
func quoteList(items []string, max int, lang string) string {
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
		parts = append(parts, quote(lang, clip(it, 30)))
	}
	s := strings.Join(parts, listSep(lang))
	if rest := len(items) - covered; rest > 0 {
		s += tr(lang, "and_more", "n", rest)
	}
	return s
}

// listSep — запятая перечисления: в арабском «، ».
func listSep(lang string) string {
	if lang == "ar" {
		return "، "
	}
	return ", "
}

func plainList(items []string, max int, lang string) string {
	var parts []string
	for i, it := range items {
		if i == max {
			break
		}
		parts = append(parts, clip(it, 30))
	}
	s := strings.Join(parts, listSep(lang))
	if len(items) > max {
		s += "…"
	}
	return s
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[:1])) + string(r[1:])
}

// Render собирает заголовок и текст пуша на языке получателя (см. Lang; незнакомый язык — английский).
func Render(kind Kind, locale string, p Params) (title, body string) {
	l := Lang(locale)
	task := clip(TaskTitle(p.TaskType, p.Title, locale), maxTitle)
	tomorrow := p.Day == "tomorrow"
	switch kind {
	case KindMorning:
		title = tr(l, "morning.title", "name", p.Name)
		body = trN(l, "morning.count", p.Count)
		if p.HighCount > 0 {
			body += trN(l, "morning.high", p.HighCount)
		}
		if task != "" {
			body += tr(l, "morning.first", "task", quote(l, task))
			if p.FirstTime != "" {
				body += tr(l, "morning.first_at", "time", p.FirstTime)
			}
		}
	case KindReminder:
		if p.StartAt != "" {
			title = tr(l, "reminder.cook_title", "task", task, "time", p.StartAt)
			body = tr(l, "reminder.cook_body", "time", p.Time)
		} else {
			title = tr(l, "reminder.title", "n", p.Lead, "task", task)
			body = p.Time
		}
		if p.Important {
			body += tr(l, "reminder.important")
		}
	case KindTaskNew:
		// Имя не склоняем («от Анны» не собрать из любого имени) — «Анна: новая задача».
		key := "task_new.today"
		if tomorrow {
			key = "task_new.tomorrow"
		}
		if p.Actor != "" {
			title = tr(l, key+"_by", "actor", p.Actor)
		} else {
			title = tr(l, key)
		}
		body = task
		if p.Time != "" {
			body += " · " + p.Time
		}
	case KindTaskMoved:
		switch {
		case p.Time == "":
			title = tr(l, "task_moved.any", "task", task)
		case tomorrow:
			title = tr(l, "task_moved.tomorrow", "task", task, "time", p.Time)
		default:
			title = tr(l, "task_moved.now", "task", task, "time", p.Time)
		}
		body = tr(l, "task_moved.body", "actor", p.Actor)
	case KindTaskCancelled:
		title = tr(l, "task_cancelled.title")
		if tomorrow {
			body = tr(l, "task_cancelled.tomorrow", "task", task)
		} else {
			body = tr(l, "task_cancelled.today", "task", task)
		}
	case KindListClosed:
		title = tr(l, "list_closed.title")
		body = tr(l, "list_closed.body", "done", p.Done, "total", p.Total)
		if p.Left > 0 {
			body += tr(l, "list_closed.left", "n", p.Left)
		}
	case KindListSent:
		title = tr(l, "list_sent.title", "actor", p.Actor)
		body = trN(l, "list_sent.count", p.Count)
		if len(p.Items) > 0 {
			body += ": " + plainList(p.Items, 4, l)
		}
	case KindListAdded:
		title = tr(l, "list_added.title", "actor", p.Actor)
		body = fmt.Sprintf("+%d: %s", len(p.Items), plainList(p.Items, 5, l))
	case KindDigest:
		if p.Done >= p.Total {
			title = tr(l, "digest.all_title", "name", p.Name)
			body = tr(l, "digest.all_body", "done", p.Done, "total", p.Total)
		} else {
			items := make([]string, len(p.Items))
			for i, it := range p.Items {
				tt := ""
				if i < len(p.ItemTypes) {
					tt = p.ItemTypes[i]
				}
				items[i] = TaskTitle(tt, it, locale)
			}
			title = tr(l, "digest.title", "name", p.Name, "done", p.Done, "total", p.Total)
			body = tr(l, "digest.not_done", "list", quoteList(items, 2, l))
		}
	case KindImportantDone:
		title = tr(l, "important_done.title", "task", task)
		body = tr(l, "important_done.body", "actor", p.Actor, "time", p.At)
	case KindDayOff:
		reason := "weekly"
		switch p.Reason {
		case "holiday", "vacation", "day_off":
			reason = p.Reason
		}
		title = tr(l, "day_off.title", "name", p.Name, "reason", tr(l, "reason."+reason))
		body = trN(l, "day_off.body", p.Count, "list", plainList(p.Items, 3, l))
	case KindMemberJoined:
		title = tr(l, "member_joined.title", "name", p.Name)
		body = tr(l, "member_joined.body")
	case KindQuietSummary:
		title = tr(l, "quiet.title")
		body = upperFirst(strings.Join(p.Titles, "; "))
	case KindTest:
		title = tr(l, "test.title")
		body = tr(l, "test.body")
	}
	return clip(title, 90), clip(body, 180)
}

// SummaryLine — строка для сводки «Пока вы отдыхали»: коротко и с названием задачи.
func SummaryLine(kind Kind, locale string, p Params) string {
	l := Lang(locale)
	task := quote(l, clip(TaskTitle(p.TaskType, p.Title, locale), 40))
	switch kind {
	case KindTaskNew:
		return tr(l, "summary.task_new", "task", task)
	case KindTaskCancelled:
		return tr(l, "summary.cancelled", "task", task)
	case KindTaskMoved:
		return tr(l, "summary.moved", "task", task, "time", p.Time)
	case KindListClosed:
		return tr(l, "summary.list_closed")
	case KindListSent:
		return tr(l, "summary.list_sent", "actor", p.Actor)
	}
	t, _ := Render(kind, locale, p)
	return t
}

// TaskTitle — название задачи для пуша: у системных задач (еда, уборка, ребёнок)
// заголовок в базе служебный, его переводим.
func TaskTitle(taskType, title, locale string) string {
	l := Lang(locale)
	switch taskType {
	case "meal":
		if i := strings.Index(title, " - "); i > 0 {
			return title[:i]
		}
	case "cleaning":
		zone := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(title, "Уборка"), ":"))
		return tr(l, "task_title.cleaning", "zone", zone)
	case "childcare":
		return tr(l, "task_title.childcare")
	}
	return title
}
