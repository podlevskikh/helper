package calendar

import (
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/models"
)

// Причины, по которым день рабочий или выходной.
const (
	ReasonWorkOverride = "work_override" // рабочий по договорённости
	ReasonVacation     = "vacation"
	ReasonDayOff       = "day_off" // отгул / выходной по договорённости
	ReasonHoliday      = "holiday"
	ReasonWeekly       = "weekly" // постоянный выходной день недели
)

// DayStatus — рабочий ли день у сотрудника и почему.
type DayStatus struct {
	Date       string `json:"date"`
	Working    bool   `json:"working"`
	Reason     string `json:"reason,omitempty"`
	Holiday    string `json:"holiday,omitempty"` // название праздника, если он в этот день
	OverrideID *uint  `json:"override_id,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Person — то, что нужно, чтобы посчитать рабочие дни сотрудника.
type Person struct {
	OrgID        uint
	UserID       uint
	DaysOff      map[int]bool
	Country      string
	VacationDays int
}

// ISOWeekday: 1 = Пн … 7 = Вс.
func ISOWeekday(d time.Time) int {
	wd := int(d.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// ParseDaysOff разбирает "6,7" → {6, 7}.
func ParseDaysOff(s string) map[int]bool {
	out := map[int]bool{}
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n >= 1 && n <= 7 {
			out[n] = true
		}
	}
	return out
}

// FormatDaysOff — {7, 6} → "6,7".
func FormatDaysOff(days []int) string {
	var parts []string
	for d := 1; d <= 7; d++ {
		for _, x := range days {
			if x == d {
				parts = append(parts, strconv.Itoa(d))
				break
			}
		}
	}
	return strings.Join(parts, ",")
}

// PersonFrom собирает Person из участника организации.
func PersonFrom(m models.Membership) *Person {
	country := m.HolidayCountry
	if country == "" {
		country = DefaultCountry
	}
	return &Person{
		OrgID:        m.OrganizationID,
		UserID:       m.UserID,
		DaysOff:      ParseDaysOff(m.DaysOff),
		Country:      country,
		VacationDays: m.VacationDaysPerYear,
	}
}

// LoadPerson — сотрудник организации (nil, если такого участника нет).
func LoadPerson(db *gorm.DB, orgID, userID uint) (*Person, error) {
	var m models.Membership
	if err := db.Where("organization_id = ? AND user_id = ?", orgID, userID).First(&m).Error; err != nil {
		return nil, err
	}
	return PersonFrom(m), nil
}

// Range считает рабочие/выходные дни сотрудника с from по to включительно.
func Range(db *gorm.DB, p *Person, from, to time.Time) ([]DayStatus, error) {
	from, to = day(from), day(to)
	if to.Before(from) {
		return nil, nil
	}

	holidays := map[string]string{}
	if p.Country != NoHolidays {
		var hs []models.PublicHoliday
		if err := db.Where("country_code = ? AND date >= ? AND date <= ?", p.Country, from, to).
			Order("date").Find(&hs).Error; err != nil {
			return nil, err
		}
		for _, h := range hs {
			k := h.Date.UTC().Format("2006-01-02")
			if _, ok := holidays[k]; !ok {
				holidays[k] = h.Name
			}
		}
	}

	var overrides []models.StaffDayOverride
	if err := db.Where("organization_id = ? AND user_id = ? AND start_date <= ? AND end_date >= ?",
		p.OrgID, p.UserID, to, from).Order("id").Find(&overrides).Error; err != nil {
		return nil, err
	}

	var out []DayStatus
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		s := DayStatus{Date: k, Working: true, Holiday: holidays[k]}

		// Особые дни: «рабочий» важнее отпуска и отгула.
		var work, off *models.StaffDayOverride
		for i := range overrides {
			o := &overrides[i]
			if d.Before(day(o.StartDate)) || d.After(day(o.EndDate)) {
				continue
			}
			if o.Kind == models.DayOverrideWork {
				work = o
			} else if off == nil || o.Kind == models.DayOverrideDayOff {
				off = o
			}
		}
		// Без особых дней: праздник или постоянный выходной.
		switch {
		case s.Holiday != "":
			s.Working, s.Reason = false, ReasonHoliday
		case p.DaysOff[ISOWeekday(d)]:
			s.Working, s.Reason = false, ReasonWeekly
		}
		switch {
		case work != nil:
			s.Working, s.Reason, s.OverrideID, s.Note = true, ReasonWorkOverride, &work.ID, work.Note
		case off != nil && s.Working:
			// Отпуск/отгул выпадает только на рабочие дни; в праздник и выходной остаётся их причина.
			s.Working, s.OverrideID, s.Note = false, &off.ID, off.Note
			s.Reason = ReasonDayOff
			if off.Kind == models.DayOverrideVacation {
				s.Reason = ReasonVacation
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// StatusOn — один день сотрудника.
func StatusOn(db *gorm.DB, orgID, userID uint, date time.Time) (DayStatus, error) {
	p, err := LoadPerson(db, orgID, userID)
	if err != nil {
		return DayStatus{Date: day(date).Format("2006-01-02"), Working: true}, err
	}
	list, err := Range(db, p, date, date)
	if err != nil || len(list) == 0 {
		return DayStatus{Date: day(date).Format("2006-01-02"), Working: true}, err
	}
	return list[0], nil
}

// IsWorking — работает ли сотрудник в этот день (при ошибке считаем, что работает).
func IsWorking(db *gorm.DB, orgID, userID uint, date time.Time) bool {
	s, _ := StatusOn(db, orgID, userID, date)
	return s.Working
}

// AnyoneWorks — работает ли в этот день хоть один активный помощник организации.
// Если помощников нет, день считается рабочим (расписание ведёт сам наниматель).
func AnyoneWorks(db *gorm.DB, orgID uint, date time.Time) bool {
	var ms []models.Membership
	db.Where("organization_id = ? AND role = ? AND status = ?", orgID, models.RoleHelper, models.MembershipActive).Find(&ms)
	if len(ms) == 0 {
		return true
	}
	for _, m := range ms {
		list, err := Range(db, PersonFrom(m), date, date)
		if err != nil || len(list) == 0 || list[0].Working {
			return true
		}
	}
	return false
}

// VacationUsed — сколько рабочих дней отпуска сотрудник взял (или возьмёт) в этом году:
// дни отпусков без его выходных, праздников и дней, где он работает по договорённости.
func VacationUsed(db *gorm.DB, p *Person, year int) int {
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
	list, err := Range(db, p, from, to)
	if err != nil {
		return 0
	}
	n := 0
	for _, s := range list {
		if s.Reason == ReasonVacation {
			n++
		}
	}
	return n
}

// CountVacationDays — сколько рабочих дней займёт отпуск с start по end (для подсказки при добавлении).
func CountVacationDays(db *gorm.DB, p *Person, start, end time.Time) int {
	list, err := Range(db, p, start, end)
	if err != nil {
		return 0
	}
	n := 0
	for _, s := range list {
		// Рабочий сейчас или уже в отпуске — значит, без отпуска был бы рабочим.
		if (s.Working && s.Reason == "") || s.Reason == ReasonVacation {
			n++
		}
	}
	return n
}
