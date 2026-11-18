// Package calendar — государственные праздники стран и рабочие/выходные дни сотрудников.
package calendar

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/models"
)

// NoHolidays — значение holiday_country «без праздников».
const NoHolidays = "-"

// DefaultCountry — страна праздников по умолчанию.
const DefaultCountry = "CY"

// googleIDs — ISO-код страны → идентификатор публичного календаря праздников Google
// (https://calendar.google.com/calendar/ical/en.<id>%23holiday%40group.v.calendar.google.com/public/basic.ics).
// Проверено вручную: у части стран — двухбуквенный код, у части — название.
var googleIDs = map[string]string{
	"AM": "am", "AT": "austrian", "AU": "australian", "AZ": "az", "BG": "bulgarian", "BY": "by",
	"CY": "cy", "DE": "german", "ES": "spain", "ET": "et", "FR": "french", "GB": "uk",
	"GE": "ge", "GR": "greek", "ID": "indonesian", "IN": "indian", "IT": "italian", "KG": "kg",
	"KZ": "kz", "LK": "lk", "MD": "md", "NP": "np", "PH": "philippines", "PL": "polish",
	"PT": "portuguese", "RO": "romanian", "RS": "rs", "RU": "russian", "TH": "th", "TR": "turkish",
	"UA": "ukrainian", "US": "usa", "UZ": "uz", "VN": "vietnamese",
}

// Supported — можно ли выбрать эту страну праздников ("-" — без праздников).
func Supported(code string) bool {
	if code == NoHolidays {
		return true
	}
	_, ok := googleIDs[code]
	return ok
}

// Countries — коды стран, для которых умеем загружать праздники.
func Countries() []string {
	out := make([]string, 0, len(googleIDs))
	for k := range googleIDs {
		out = append(out, k)
	}
	return out
}

var httpClient = &http.Client{Timeout: 20 * time.Second}

func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ── Google (основной источник) ───────────────────────────────────────────────

func icsUnescape(s string) string {
	r := strings.NewReplacer(`\,`, ",", `\;`, ";", `\n`, " ", `\N`, " ", `\\`, `\`)
	return strings.TrimSpace(r.Replace(s))
}

// parseICS разбирает ICS публичного календаря праздников Google и оставляет только
// выходные дни (DESCRIPTION начинается с «Public holiday»; «Observance» — не выходной).
func parseICS(r io.Reader, code string) ([]models.PublicHoliday, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	var lines []string
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:] // свёрнутая строка
			continue
		}
		lines = append(lines, l)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	var out []models.PublicHoliday
	var start, end time.Time
	var summary, desc string
	in := false
	for _, l := range lines {
		switch {
		case l == "BEGIN:VEVENT":
			in, start, end, summary, desc = true, time.Time{}, time.Time{}, "", ""
		case l == "END:VEVENT":
			in = false
			if start.IsZero() || summary == "" || !strings.HasPrefix(desc, "Public holiday") {
				continue
			}
			if end.IsZero() || !end.After(start) {
				end = start.AddDate(0, 0, 1)
			}
			for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
				out = append(out, models.PublicHoliday{CountryCode: code, Date: d, Name: summary, Source: "google", FetchedAt: now})
			}
		case !in:
		case strings.HasPrefix(l, "DTSTART"):
			if t, err := time.Parse("20060102", l[strings.LastIndex(l, ":")+1:]); err == nil {
				start = t
			}
		case strings.HasPrefix(l, "DTEND"):
			if t, err := time.Parse("20060102", l[strings.LastIndex(l, ":")+1:]); err == nil {
				end = t
			}
		case strings.HasPrefix(l, "SUMMARY"):
			summary = icsUnescape(l[strings.Index(l, ":")+1:])
		case strings.HasPrefix(l, "DESCRIPTION"):
			desc = icsUnescape(l[strings.Index(l, ":")+1:])
		}
	}
	return out, nil
}

func fetchGoogle(ctx context.Context, code string) ([]models.PublicHoliday, error) {
	id, ok := googleIDs[code]
	if !ok {
		return nil, fmt.Errorf("no google calendar for %s", code)
	}
	url := "https://calendar.google.com/calendar/ical/en." + id + "%23holiday%40group.v.calendar.google.com/public/basic.ics"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google: HTTP %d", resp.StatusCode)
	}
	return parseICS(resp.Body, code)
}

// ── Nager.Date (запасной источник) ───────────────────────────────────────────

func fetchNager(ctx context.Context, code string) ([]models.PublicHoliday, error) {
	now := time.Now().UTC()
	var out []models.PublicHoliday
	for y := now.Year() - 1; y <= now.Year()+2; y++ {
		url := fmt.Sprintf("https://date.nager.at/api/v3/PublicHolidays/%d/%s", y, code)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("nager: HTTP %d", resp.StatusCode)
		}
		var rows []struct {
			Date      string   `json:"date"`
			LocalName string   `json:"localName"`
			Name      string   `json:"name"`
			Global    bool     `json:"global"`
			Types     []string `json:"types"`
		}
		err = json.NewDecoder(resp.Body).Decode(&rows)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			public := false
			for _, t := range r.Types {
				if t == "Public" {
					public = true
				}
			}
			d, err := time.Parse("2006-01-02", r.Date)
			if !public || !r.Global || err != nil {
				continue
			}
			out = append(out, models.PublicHoliday{CountryCode: code, Date: d, Name: r.Name, LocalName: r.LocalName, Source: "nager", FetchedAt: now})
		}
	}
	return out, nil
}

// ── Синхронизация ────────────────────────────────────────────────────────────

// Sync загружает праздники страны и заменяет ими сохранённые.
func Sync(db *gorm.DB, code string) error {
	if code == NoHolidays || code == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	source := "google"
	list, err := fetchGoogle(ctx, code)
	if err != nil || len(list) == 0 {
		gErr := err
		source = "nager"
		list, err = fetchNager(ctx, code)
		if err != nil || len(list) == 0 {
			msg := fmt.Sprintf("google: %v; nager: %v", gErr, err)
			db.Where(models.HolidaySync{CountryCode: code}).
				Assign(models.HolidaySync{Status: "error", Error: msg, FetchedAt: time.Now().UTC()}).
				FirstOrCreate(&models.HolidaySync{})
			return fmt.Errorf("holidays %s: %s", code, msg)
		}
	}

	// Убираем дубли (один праздник может прийти дважды)
	seen := map[string]bool{}
	uniq := list[:0]
	for _, h := range list {
		k := h.Date.Format("2006-01-02") + "|" + h.Name
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, h)
		}
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("country_code = ?", code).Delete(&models.PublicHoliday{}).Error; err != nil {
			return err
		}
		if err := tx.CreateInBatches(uniq, 200).Error; err != nil {
			return err
		}
		return tx.Where(models.HolidaySync{CountryCode: code}).
			Assign(models.HolidaySync{Source: source, Status: "ok", Error: "", Count: len(uniq), FetchedAt: time.Now().UTC()}).
			FirstOrCreate(&models.HolidaySync{}).Error
	})
	if err == nil {
		log.Printf("holidays: %s — %d дней (%s)", code, len(uniq), source)
	}
	return err
}

var (
	syncMu   sync.Mutex
	inflight = map[string]bool{}
)

// refreshEvery — как часто перезагружаем праздники страны (переносы и исламские даты объявляют поздно).
const refreshEvery = 30 * 24 * time.Hour

// EnsureFresh загружает праздники страны в фоне, если их нет или они устарели.
func EnsureFresh(db *gorm.DB, code string) {
	if !Supported(code) || code == NoHolidays {
		return
	}
	var s models.HolidaySync
	if err := db.Where("country_code = ?", code).First(&s).Error; err == nil {
		fresh := time.Since(s.FetchedAt) < refreshEvery
		if s.Status == "ok" && fresh {
			return
		}
		if s.Status == "error" && time.Since(s.FetchedAt) < time.Hour {
			return // не долбим источник после ошибки
		}
	}
	syncMu.Lock()
	if inflight[code] {
		syncMu.Unlock()
		return
	}
	inflight[code] = true
	syncMu.Unlock()
	go func() {
		defer func() {
			syncMu.Lock()
			delete(inflight, code)
			syncMu.Unlock()
		}()
		if err := Sync(db, code); err != nil {
			log.Printf("Warning: %v", err)
		}
	}()
}

// StartRefresher — при старте и раз в сутки обновляет праздники всех стран, выбранных у сотрудников.
func StartRefresher(db *gorm.DB) {
	run := func() {
		var codes []string
		db.Model(&models.Membership{}).Distinct().Pluck("holiday_country", &codes)
		codes = append(codes, DefaultCountry)
		for _, c := range codes {
			EnsureFresh(db, c)
		}
	}
	go func() {
		run()
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for range t.C {
			run()
		}
	}()
}
