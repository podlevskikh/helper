package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"podlevskikh/awesomeProject/internal/models"
)

func TestRenderRuEn(t *testing.T) {
	cases := []struct {
		kind   Kind
		locale string
		p      Params
		title  string
		body   string
	}{
		{KindMorning, "ru", Params{Name: "Мария", Count: 7, HighCount: 2, Title: "Завтрак - all", TaskType: "meal", FirstTime: "08:00"},
			"Доброе утро, Мария", "Сегодня 7 задач, 2 важные. Первая — «Завтрак» в 08:00"},
		{KindMorning, "en", Params{Name: "Maria", Count: 1, Title: "Breakfast", FirstTime: "08:00"},
			"Good morning, Maria", "1 task today. First: “Breakfast” at 08:00"},
		{KindReminder, "ru", Params{Title: "Забрать Соню", Time: "13:00", Lead: 15, Important: true},
			"Через 15 мин · Забрать Соню", "13:00 · важная задача"},
		{KindReminder, "en", Params{Title: "Lunch - all", TaskType: "meal", Time: "13:00", StartAt: "12:15", Lead: 15},
			"Lunch: start cooking at 12:15", "Serve at 13:00"},
		{KindTaskNew, "ru", Params{Title: "Забрать химчистку", Actor: "Анна", Day: "today"},
			"Анна: новая задача на сегодня", "Забрать химчистку"},
		{KindTaskNew, "en", Params{Title: "Dry cleaning", Time: "11:00", Day: "tomorrow"},
			"New task for tomorrow", "Dry cleaning · 11:00"},
		{KindTaskMoved, "en", Params{Title: "Lunch", Time: "14:00", Actor: "Anna", Day: "tomorrow"},
			"Lunch — tomorrow at 14:00", "New time · Anna"},
		{KindTaskCancelled, "ru", Params{Title: "Полить цветы", Day: "today"},
			"Задача отменена", "Полить цветы — сегодня не нужно"},
		{KindListClosed, "ru", Params{Done: 12, Total: 14, Left: 2},
			"Покупки привезли", "Куплено 12 из 14. Ещё 2 — в новом списке"},
		{KindListSent, "ru", Params{Actor: "Мария", Count: 5, Items: []string{"молоко", "яйца", "творог", "хлеб", "сыр"}},
			"Мария: список покупок", "5 пунктов: молоко, яйца, творог, хлеб…"},
		{KindListAdded, "en", Params{Actor: "Maria", Items: []string{"bread", "bananas"}},
			"Maria: added to the list", "+2: bread, bananas"},
		{KindDigest, "ru", Params{Name: "Мария", Done: 6, Total: 8, Items: []string{"Ванная", "Полить цветы"}, ItemTypes: []string{"cleaning", "custom"}},
			"Мария: сделано 6 из 8", "Не сделано: «Уборка: Ванная», «Полить цветы»"},
		{KindDigest, "ru", Params{Name: "Лан", Done: 0, Total: 5, Items: []string{"Childcare", "Childcare", "Обед - all", "Ужин - all", "Childcare"}, ItemTypes: []string{"childcare", "childcare", "meal", "meal", "childcare"}},
			"Лан: сделано 0 из 5", "Не сделано: «Уход за ребёнком», «Обед» и ещё 1"},
		{KindDigest, "en", Params{Name: "Maria", Done: 8, Total: 8},
			"Maria: all done", "8 of 8"},
		{KindImportantDone, "ru", Params{Actor: "Мария", Title: "Забрать Соню из школы", At: "15:40"},
			"Готово · Забрать Соню из школы", "Мария · отмечено в 15:40"},
		{KindMemberJoined, "en", Params{Name: "Maria"}, "Maria joined your home", "You can now assign tasks"},
	}
	for _, c := range cases {
		title, body := Render(c.kind, c.locale, c.p)
		if title != c.title || body != c.body {
			t.Errorf("%s/%s:\n got  %q | %q\n want %q | %q", c.kind, c.locale, title, body, c.title, c.body)
		}
	}
}

func TestQuietSummary(t *testing.T) {
	lines := []string{
		SummaryLine(KindTaskNew, "ru", Params{Title: "Полить цветы", Actor: "Анна"}),
		SummaryLine(KindListClosed, "ru", Params{}),
		SummaryLine(KindTaskCancelled, "ru", Params{Title: "Ванная", TaskType: "cleaning"}),
	}
	title, body := Render(KindQuietSummary, "ru", Params{Titles: lines})
	if title != "Пока вы отдыхали" || body != "Новая задача «Полить цветы»; покупки привезли; отменена «Уборка: Ванная»" {
		t.Errorf("got %q | %q", title, body)
	}
}

func TestPluralIndex(t *testing.T) {
	for n, want := range map[int]int{1: 0, 2: 1, 5: 2, 11: 2, 21: 0, 22: 1, 14: 2} {
		if got := pluralIndex("ru", n); got != want {
			t.Errorf("ru %d: got %d want %d", n, got, want)
		}
	}
	for n, want := range map[int]int{0: 0, 1: 1, 2: 2, 3: 3, 10: 3, 11: 4, 99: 4, 100: 5, 102: 5, 103: 3} {
		if got := pluralIndex("ar", n); got != want {
			t.Errorf("ar %d: got %d want %d", n, got, want)
		}
	}
	if pluralIndex("id", 5) != 0 || pluralIndex("si", 0) != 0 || pluralIndex("es", 0) != 1 || pluralIndex("en", 1) != 0 {
		t.Error("правила числа для id/si/es/en")
	}
}

// LANG-04: у каждого языка пушей — все ключи английского, параметры только из английской строки,
// у форм по числу — столько форм, сколько у языка (ru, uk — 3; id — 1; остальные — 2).
func TestCatalogsComplete(t *testing.T) {
	params := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, p := range strings.Split(s, "{")[1:] {
			if i := strings.Index(p, "}"); i > 0 {
				m[p[:i]] = true
			}
		}
		return m
	}
	forms := map[string]int{"ru": 3, "uk": 3, "id": 1, "ar": 6}
	for lang, cat := range catalogs {
		if lang == "en" {
			continue
		}
		for key, en := range catalogEn {
			v, ok := cat[key]
			if !ok {
				t.Errorf("%s: нет ключа %q", lang, key)
				continue
			}
			want := params(en)
			for p := range params(v) {
				if !want[p] {
					t.Errorf("%s %q: параметр {%s} не из английской строки", lang, key, p)
				}
			}
			if strings.Contains(en, "|") {
				n := forms[lang]
				if n == 0 {
					n = 2
				}
				if got := len(strings.Split(v, "|")); got != n {
					t.Errorf("%s %q: %d форм вместо %d", lang, key, got, n)
				}
			}
		}
		for key := range cat {
			if _, ok := catalogEn[key]; !ok {
				t.Errorf("%s: лишний ключ %q", lang, key)
			}
		}
	}
}

func TestArabicListSeparator(t *testing.T) {
	_, body := Render(KindListSent, "ar", Params{Actor: "Maria", Count: 2, Items: []string{"milk", "eggs"}})
	if !strings.Contains(body, "milk، eggs") {
		t.Errorf("арабская запятая: %q", body)
	}
}

func TestLang(t *testing.T) {
	for in, want := range map[string]string{"ru": "ru", "uk-UA": "uk", "tl": "fil", "in_ID": "id", "fil": "fil", "de": "en", "": "en", "be": "ru"} {
		if got := Lang(in); got != want {
			t.Errorf("Lang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuietHours(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Nicosia")
	p := DefaultPrefs() // 21:00–07:00
	at := func(h, m int) time.Time { return time.Date(2026, 11, 18, h, m, 0, 0, loc) }
	for _, c := range []struct {
		t     time.Time
		quiet bool
	}{{at(20, 59), false}, {at(21, 0), true}, {at(23, 30), true}, {at(3, 0), true}, {at(6, 59), true}, {at(7, 0), false}, {at(12, 0), false}} {
		if got := p.inQuiet(c.t); got != c.quiet {
			t.Errorf("%s: inQuiet=%v want %v", c.t.Format("15:04"), got, c.quiet)
		}
	}
	if end := p.quietEnd(at(23, 30)); !end.Equal(time.Date(2026, 11, 19, 7, 0, 0, 0, loc)) {
		t.Errorf("quietEnd after 23:30 = %v", end)
	}
	if end := p.quietEnd(at(3, 0)); !end.Equal(at(7, 0)) {
		t.Errorf("quietEnd after 03:00 = %v", end)
	}
	p.QuietEnabled = false
	if p.inQuiet(at(23, 0)) {
		t.Error("quiet hours off but inQuiet")
	}
	day := Prefs{QuietEnabled: true, QuietFrom: "13:00", QuietTo: "15:00"}
	if !day.inQuiet(at(14, 0)) || day.inQuiet(at(15, 0)) {
		t.Error("daytime quiet window")
	}
}

func TestPrefsValidateAndEnabled(t *testing.T) {
	p := DefaultPrefs()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Disabled = []string{"digest"}
	if p.Enabled(KindDigest) || !p.Enabled(KindListSent) || !p.Enabled(KindMemberJoined) {
		t.Error("Enabled mismatch")
	}
	p.Disabled = []string{"nope"}
	if p.Validate() == nil {
		t.Error("unknown kind accepted")
	}
	p = DefaultPrefs()
	p.MorningTime = "7:30"
	if p.Validate() == nil {
		t.Error("bad time accepted")
	}
	p = DefaultPrefs()
	p.ReminderLeadMin = 7
	if p.Validate() == nil {
		t.Error("bad lead accepted")
	}
}

func TestStartAtAndWithin(t *testing.T) {
	meal := &models.ScheduleTask{TaskType: "meal", Time: "13:00", Recipes: []models.Recipe{{PrepTime: 15, CookTime: 30}, {PrepTime: 10, CookTime: 10}}}
	if got := StartAt(meal); got != "12:15" {
		t.Errorf("StartAt = %q", got)
	}
	if got := StartAt(&models.ScheduleTask{TaskType: "custom", Time: "13:00"}); got != "" {
		t.Errorf("custom StartAt = %q", got)
	}
	loc := time.UTC
	if !within(time.Date(2026, 1, 1, 7, 30, 0, 0, loc), "07:30", 10) || !within(time.Date(2026, 1, 1, 7, 39, 0, 0, loc), "07:30", 10) || within(time.Date(2026, 1, 1, 7, 40, 0, 0, loc), "07:30", 10) {
		t.Error("within window")
	}
}

func TestCalendarDay(t *testing.T) {
	nic, _ := time.LoadLocation("Europe/Nicosia")
	ny, _ := time.LoadLocation("America/New_York")
	for _, d := range []time.Time{
		time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 18, 0, 0, 0, 0, nic),
		time.Date(2026, 11, 18, 0, 0, 0, 0, ny),
	} {
		if got := CalendarDay(d); got != "2026-11-18" {
			t.Errorf("CalendarDay(%v) = %s", d, got)
		}
	}
}

func TestExpoClientRetriesAndAuth(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("no auth header")
		}
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var msgs []Message
		_ = json.NewDecoder(r.Body).Decode(&msgs)
		out := make([]Ticket, len(msgs))
		for i, m := range msgs {
			if strings.Contains(m.To, "dead") {
				out[i] = Ticket{Status: "error", Message: "not registered"}
				out[i].Details.Error = "DeviceNotRegistered"
			} else {
				out[i] = Ticket{Status: "ok", ID: "t-" + m.To}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	defer srv.Close()
	c := &ExpoClient{BaseURL: srv.URL, AccessToken: "secret", HTTP: srv.Client()}
	tickets, err := c.Send([]Message{{To: "ExponentPushToken[a]"}, {To: "ExponentPushToken[dead]"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 || tickets[0].Status != "ok" || tickets[1].Details.Error != "DeviceNotRegistered" {
		t.Errorf("tickets = %+v", tickets)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want retry after 503", calls)
	}
}

func TestExpoClientChunksBy100(t *testing.T) {
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msgs []Message
		_ = json.NewDecoder(r.Body).Decode(&msgs)
		sizes = append(sizes, len(msgs))
		out := make([]Ticket, len(msgs))
		for i := range out {
			out[i] = Ticket{Status: "ok"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	defer srv.Close()
	c := &ExpoClient{BaseURL: srv.URL, HTTP: srv.Client()}
	msgs := make([]Message, 230)
	tickets, err := c.Send(msgs)
	if err != nil || len(tickets) != 230 {
		t.Fatalf("err=%v n=%d", err, len(tickets))
	}
	if len(sizes) != 3 || sizes[0] != 100 || sizes[2] != 30 {
		t.Errorf("chunks = %v", sizes)
	}
}
