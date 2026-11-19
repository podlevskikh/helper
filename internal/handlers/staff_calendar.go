package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
)

// StaffCalendarHandler — выходные дни, страна праздников, отпуска и особые дни сотрудников.
// Всё настраивает наниматель (manage_team); помощник только видит итог в своём расписании.
type StaffCalendarHandler struct {
	db *gorm.DB
	// EnsureSchedule собирает расписание дня, если его ещё нет (день стал рабочим по договорённости).
	EnsureSchedule func(date time.Time)
	// RefreshCleaning пересобирает уборку ближайших дней (сменились выходные сотрудника).
	RefreshCleaning func(orgID uint)
}

func NewStaffCalendarHandler(db *gorm.DB) *StaffCalendarHandler {
	return &StaffCalendarHandler{db: db}
}

func parseDay(s string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(s))
}

// targetMember — участник этой организации из :userId.
func (h *StaffCalendarHandler) targetMember(c *gin.Context) (*models.Membership, bool) {
	orgID := middleware.MustMembership(c).OrganizationID
	uid, err := strconv.ParseUint(c.Param("userId"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return nil, false
	}
	var m models.Membership
	if err := h.db.Where("organization_id = ? AND user_id = ?", orgID, uint(uid)).First(&m).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return nil, false
	}
	return &m, true
}

func daysOffList(s string) []int {
	set := calendar.ParseDaysOff(s)
	out := []int{}
	for d := 1; d <= 7; d++ {
		if set[d] {
			out = append(out, d)
		}
	}
	return out
}

type scheduleSettingsView struct {
	UserID              uint   `json:"user_id"`
	DaysOff             []int  `json:"days_off"`
	HolidayCountry      string `json:"holiday_country"`
	VacationDaysPerYear int    `json:"vacation_days_per_year"`
	Year                int    `json:"year"`
	VacationUsed        int    `json:"vacation_used"`
	HolidaysStatus      string `json:"holidays_status,omitempty"` // ok | error | loading
}

func (h *StaffCalendarHandler) settingsView(m models.Membership) scheduleSettingsView {
	p := calendar.PersonFrom(m)
	year := time.Now().Year()
	v := scheduleSettingsView{
		UserID:              m.UserID,
		DaysOff:             daysOffList(m.DaysOff),
		HolidayCountry:      p.Country,
		VacationDaysPerYear: m.VacationDaysPerYear,
		Year:                year,
		VacationUsed:        calendar.VacationUsed(h.db, p, year),
	}
	if p.Country != calendar.NoHolidays {
		var s models.HolidaySync
		if err := h.db.Where("country_code = ?", p.Country).First(&s).Error; err == nil {
			v.HolidaysStatus = s.Status
		} else {
			v.HolidaysStatus = "loading"
		}
	}
	return v
}

// GET /orgs/:orgId/members/:userId/schedule-settings
func (h *StaffCalendarHandler) GetSettings(c *gin.Context) {
	m, ok := h.targetMember(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, h.settingsView(*m))
}

// PATCH /orgs/:orgId/members/:userId/schedule-settings
// {days_off?: [1..7], holiday_country?: "CY" | "-", vacation_days_per_year?: 0..60}
func (h *StaffCalendarHandler) UpdateSettings(c *gin.Context) {
	m, ok := h.targetMember(c)
	if !ok {
		return
	}
	var in struct {
		DaysOff             *[]int  `json:"days_off"`
		HolidayCountry      *string `json:"holiday_country"`
		VacationDaysPerYear *int    `json:"vacation_days_per_year"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	upd := map[string]interface{}{}
	if in.DaysOff != nil {
		for _, d := range *in.DaysOff {
			if d < 1 || d > 7 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "days_off: 1 = Mon … 7 = Sun"})
				return
			}
		}
		if len(*in.DaysOff) == 7 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "days_off: at least one working day is required"})
			return
		}
		upd["days_off"] = calendar.FormatDaysOff(*in.DaysOff)
	}
	if in.HolidayCountry != nil {
		code := strings.ToUpper(strings.TrimSpace(*in.HolidayCountry))
		if !calendar.Supported(code) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "holiday_country: unsupported country"})
			return
		}
		upd["holiday_country"] = code
	}
	if in.VacationDaysPerYear != nil {
		if *in.VacationDaysPerYear < 0 || *in.VacationDaysPerYear > 60 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "vacation_days_per_year: 0…60"})
			return
		}
		upd["vacation_days_per_year"] = *in.VacationDaysPerYear
	}
	if len(upd) > 0 {
		if err := h.db.Model(&models.Membership{}).Where("id = ?", m.ID).Updates(upd).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	h.db.First(m, m.ID)
	if code, ok := upd["holiday_country"].(string); ok {
		calendar.EnsureFresh(h.db, code)
	}
	if h.RefreshCleaning != nil {
		h.RefreshCleaning(m.OrganizationID)
	}
	c.JSON(http.StatusOK, h.settingsView(*m))
}

// GET /orgs/:orgId/members/:userId/day-overrides — отпуска и особые дни с начала года.
func (h *StaffCalendarHandler) ListOverrides(c *gin.Context) {
	m, ok := h.targetMember(c)
	if !ok {
		return
	}
	from := time.Date(time.Now().Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	var list []models.StaffDayOverride
	if err := h.db.Where("organization_id = ? AND user_id = ? AND end_date >= ?", m.OrganizationID, m.UserID, from).
		Order("start_date, id").Find(&list).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	p := calendar.PersonFrom(*m)
	type row struct {
		models.StaffDayOverride
		WorkDays int `json:"work_days,omitempty"` // для отпуска — сколько рабочих дней он занимает
	}
	out := make([]row, 0, len(list))
	for _, o := range list {
		r := row{StaffDayOverride: o}
		if o.Kind == models.DayOverrideVacation {
			r.WorkDays = calendar.CountVacationDays(h.db, p, o.StartDate, o.EndDate)
		}
		out = append(out, r)
	}
	c.JSON(http.StatusOK, out)
}

// POST /orgs/:orgId/members/:userId/day-overrides
// {kind: vacation|day_off|work, start_date, end_date?, note?}
func (h *StaffCalendarHandler) CreateOverride(c *gin.Context) {
	m, ok := h.targetMember(c)
	if !ok {
		return
	}
	var in struct {
		Kind      string `json:"kind"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Note      string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	switch in.Kind {
	case models.DayOverrideVacation, models.DayOverrideDayOff, models.DayOverrideWork:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind: vacation | day_off | work"})
		return
	}
	start, err := parseDay(in.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "start_date: YYYY-MM-DD"})
		return
	}
	end := start
	if in.Kind == models.DayOverrideVacation && in.EndDate != "" {
		if end, err = parseDay(in.EndDate); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end_date: YYYY-MM-DD"})
			return
		}
		if end.Before(start) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end_date is before start_date"})
			return
		}
		if end.Sub(start) > 366*24*time.Hour {
			c.JSON(http.StatusBadRequest, gin.H{"error": "vacation is longer than a year"})
			return
		}
	}

	o := models.StaffDayOverride{
		OrganizationID: m.OrganizationID,
		UserID:         m.UserID,
		Kind:           in.Kind,
		StartDate:      start,
		EndDate:        end,
		Note:           strings.TrimSpace(in.Note),
		CreatedBy:      middleware.MustMembership(c).UserID,
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		// Один день — одна договорённость: «рабочий» и «выходной» на ту же дату заменяют друг друга.
		if in.Kind != models.DayOverrideVacation {
			if err := tx.Where("organization_id = ? AND user_id = ? AND kind IN ? AND start_date = ? AND end_date = ?",
				m.OrganizationID, m.UserID, []string{models.DayOverrideDayOff, models.DayOverrideWork}, start, start).
				Delete(&models.StaffDayOverride{}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&o).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if in.Kind == models.DayOverrideWork && h.EnsureSchedule != nil {
		h.EnsureSchedule(start)
	}
	c.JSON(http.StatusCreated, o)
}

// DELETE /orgs/:orgId/members/:userId/day-overrides/:id
func (h *StaffCalendarHandler) DeleteOverride(c *gin.Context) {
	m, ok := h.targetMember(c)
	if !ok {
		return
	}
	res := h.db.Where("id = ? AND organization_id = ? AND user_id = ?", c.Param("id"), m.OrganizationID, m.UserID).
		Delete(&models.StaffDayOverride{})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// GET /orgs/:orgId/calendar?user_id=&from=YYYY-MM-DD&to=YYYY-MM-DD
// Рабочие и выходные дни сотрудника. Помощник видит только свои; наниматель — любого.
func (h *StaffCalendarHandler) GetCalendar(c *gin.Context) {
	me := middleware.MustMembership(c)
	uid := me.UserID
	if s := c.Query("user_id"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
			return
		}
		uid = uint(n)
	}
	if uid != me.UserID && !middleware.Can(me, middleware.CapManageSchedule) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}
	from, err1 := parseDay(c.Query("from"))
	to, err2 := parseDay(c.Query("to"))
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from, to: YYYY-MM-DD"})
		return
	}
	if to.Before(from) || to.Sub(from) > 62*24*time.Hour {
		c.JSON(http.StatusBadRequest, gin.H{"error": "range: up to 62 days"})
		return
	}
	p, err := calendar.LoadPerson(h.db, me.OrganizationID, uid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	calendar.EnsureFresh(h.db, p.Country)
	days, err := calendar.Range(h.db, p, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, days)
}
