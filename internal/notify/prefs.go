package notify

import (
	"fmt"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/models"

	"gorm.io/gorm"
)

// Prefs — настройки уведомлений пользователя в доме (то, что видит экран «Уведомления»).
type Prefs struct {
	Disabled        []string `json:"disabled"`
	MorningTime     string   `json:"morning_time"`
	DigestTime      string   `json:"digest_time"`
	ReminderLeadMin int      `json:"reminder_lead_min"`
	QuietEnabled    bool     `json:"quiet_enabled"`
	QuietFrom       string   `json:"quiet_from"`
	QuietTo         string   `json:"quiet_to"`
}

// DefaultPrefs — всё включено; план 07:30, итог 19:00, напоминание за 15 мин, тихие часы 21:00–07:00.
func DefaultPrefs() Prefs {
	return Prefs{
		Disabled:        []string{},
		MorningTime:     "07:30",
		DigestTime:      "19:00",
		ReminderLeadMin: 15,
		QuietEnabled:    true,
		QuietFrom:       "21:00",
		QuietTo:         "07:00",
	}
}

func (p Prefs) Enabled(k Kind) bool {
	key := prefKey(k)
	if key == "" {
		return true
	}
	for _, d := range p.Disabled {
		if d == key {
			return false
		}
	}
	return true
}

// LoadPrefs — настройки с подставленными значениями по умолчанию.
func LoadPrefs(db *gorm.DB, userID, orgID uint) Prefs {
	p := DefaultPrefs()
	var row models.NotificationPref
	if db.Where("user_id = ? AND organization_id = ?", userID, orgID).First(&row).Error != nil {
		return p
	}
	if row.Disabled != "" {
		p.Disabled = strings.Split(row.Disabled, ",")
	}
	if validHHMM(row.MorningTime) {
		p.MorningTime = row.MorningTime
	}
	if validHHMM(row.DigestTime) {
		p.DigestTime = row.DigestTime
	}
	if row.ReminderLeadMin > 0 {
		p.ReminderLeadMin = row.ReminderLeadMin
	}
	p.QuietEnabled = !row.QuietOff
	if validHHMM(row.QuietFrom) {
		p.QuietFrom = row.QuietFrom
	}
	if validHHMM(row.QuietTo) {
		p.QuietTo = row.QuietTo
	}
	return p
}

// Validate проверяет ввод с экрана настроек.
func (p Prefs) Validate() error {
	known := map[string]bool{}
	for _, k := range PrefKeys {
		known[k] = true
	}
	for _, d := range p.Disabled {
		if !known[d] {
			return fmt.Errorf("unknown notification kind %q", d)
		}
	}
	for _, t := range []string{p.MorningTime, p.DigestTime, p.QuietFrom, p.QuietTo} {
		if !validHHMM(t) {
			return fmt.Errorf("time must be HH:MM, got %q", t)
		}
	}
	switch p.ReminderLeadMin {
	case 5, 10, 15, 30, 60:
	default:
		return fmt.Errorf("reminder_lead_min must be 5, 10, 15, 30 or 60")
	}
	return nil
}

// SavePrefs записывает настройки (создаёт строку при первом сохранении).
func SavePrefs(db *gorm.DB, userID, orgID uint, p Prefs) error {
	row := models.NotificationPref{UserID: userID, OrganizationID: orgID}
	db.Where("user_id = ? AND organization_id = ?", userID, orgID).First(&row)
	row.Disabled = strings.Join(p.Disabled, ",")
	row.MorningTime = p.MorningTime
	row.DigestTime = p.DigestTime
	row.ReminderLeadMin = p.ReminderLeadMin
	row.QuietOff = !p.QuietEnabled
	row.QuietFrom = p.QuietFrom
	row.QuietTo = p.QuietTo
	return db.Save(&row).Error
}

func validHHMM(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil && len(s) == 5
}

func minutesOf(hhmm string) int {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return -1
	}
	return t.Hour()*60 + t.Minute()
}

// inQuiet — попадает ли местное время в тихие часы (интервал может переходить через полночь).
func (p Prefs) inQuiet(local time.Time) bool {
	if !p.QuietEnabled {
		return false
	}
	from, to := minutesOf(p.QuietFrom), minutesOf(p.QuietTo)
	m := local.Hour()*60 + local.Minute()
	if from == to {
		return false
	}
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}

// quietEnd — ближайший конец тихих часов после local.
func (p Prefs) quietEnd(local time.Time) time.Time {
	to := minutesOf(p.QuietTo)
	end := time.Date(local.Year(), local.Month(), local.Day(), to/60, to%60, 0, 0, local.Location())
	if !end.After(local) {
		end = end.AddDate(0, 0, 1)
	}
	return end
}
