package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/models"
)

// ── Повторяющиеся задачи нанимателя ─────────────────────────────────────────

// isoWeekday: 1 = Пн … 7 = Вс.
func isoWeekday(d time.Time) int {
	wd := int(d.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

func parseWeekdays(s string) map[int]bool {
	out := map[int]bool{}
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n >= 1 && n <= 7 {
			out[n] = true
		}
	}
	return out
}

func utcDay(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// recurringFor возвращает активные повторяющиеся задачи организации на этот день.
func recurringFor(db *gorm.DB, orgID uint, date time.Time) []models.RecurringTask {
	day := utcDay(date)
	var list []models.RecurringTask
	if err := db.Where("organization_id = ? AND active = ? AND start_date <= ?", orgID, true, day).
		Find(&list).Error; err != nil {
		return nil
	}
	wd := isoWeekday(day)
	out := list[:0]
	for _, r := range list {
		if parseWeekdays(r.Weekdays)[wd] {
			out = append(out, r)
		}
	}
	return out
}

// materializeRecurring создаёт в расписании дня задачи из повторяющихся,
// которых там ещё нет (и которые не удалили вручную). Возвращает true, если что-то создано.
func materializeRecurring(db *gorm.DB, orgID uint, schedule *models.DailySchedule, date time.Time) bool {
	day := utcDay(date)
	templates := recurringFor(db, orgID, day)
	if len(templates) == 0 {
		return false
	}
	created := false
	for _, r := range templates {
		var n int64
		db.Model(&models.ScheduleTask{}).
			Where("schedule_id = ? AND recurring_task_id = ?", schedule.ID, r.ID).Count(&n)
		if n > 0 {
			continue
		}
		db.Model(&models.RecurringTaskSkip{}).
			Where("recurring_task_id = ? AND date = ?", r.ID, day).Count(&n)
		if n > 0 {
			continue
		}
		// Исполнитель в этот день отдыхает — задачу на него не ставим.
		if r.AssignedToUserID != nil && !calendar.IsWorking(db, orgID, *r.AssignedToUserID, day) {
			continue
		}
		id := r.ID
		task := models.ScheduleTask{
			OrganizationID:   orgID,
			ScheduleID:       schedule.ID,
			TaskType:         "custom",
			Time:             r.Time,
			EndTime:          r.EndTime,
			Priority:         r.Priority,
			Title:            r.Title,
			Description:      r.Description,
			TaskCategoryID:   r.TaskCategoryID,
			AssignedToUserID: r.AssignedToUserID,
			RecurringTaskID:  &id,
		}
		if db.Create(&task).Error == nil {
			created = true
		}
	}
	return created
}

// GET /admin/api/recurring-tasks
func (h *AdminHandler) GetRecurringTasks(c *gin.Context) {
	var list []models.RecurringTask
	if err := h.orgDB(c).Preload("TaskCategory").Where("active = ?", true).
		Order("created_at").Find(&list).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

// POST /admin/api/recurring-tasks
func (h *AdminHandler) CreateRecurringTask(c *gin.Context) {
	var input struct {
		Title            string `json:"title"`
		Description      string `json:"description"`
		Time             string `json:"time"`
		EndTime          string `json:"end_time"`
		Priority         string `json:"priority"`
		TaskCategoryID   *uint  `json:"task_category_id"`
		AssignedToUserID *uint  `json:"assigned_to_user_id"`
		Weekdays         []int  `json:"weekdays"`
		StartDate        string `json:"start_date"` // YYYY-MM-DD, по умолчанию — сегодня
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
		return
	}
	days := map[int]bool{}
	for _, d := range input.Weekdays {
		if d >= 1 && d <= 7 {
			days[d] = true
		}
	}
	if len(days) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "weekdays: выберите хотя бы один день (1 = Пн … 7 = Вс)"})
		return
	}
	sorted := make([]int, 0, len(days))
	for d := range days {
		sorted = append(sorted, d)
	}
	sort.Ints(sorted)
	parts := make([]string, len(sorted))
	for i, d := range sorted {
		parts[i] = strconv.Itoa(d)
	}

	start := utcDay(time.Now())
	if input.StartDate != "" {
		d, err := time.Parse("2006-01-02", input.StartDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start_date, use YYYY-MM-DD"})
			return
		}
		start = utcDay(d)
	}
	priority := input.Priority
	if priority != "" {
		priority = normalizePriority(priority)
	}
	if !checkTaskRefs(c, h.db, h.orgID(c), input.TaskCategoryID, input.AssignedToUserID) {
		return
	}

	r := models.RecurringTask{
		OrganizationID:   h.orgID(c),
		Title:            title,
		Description:      strings.TrimSpace(input.Description),
		Time:             input.Time,
		EndTime:          input.EndTime,
		Priority:         priority,
		TaskCategoryID:   input.TaskCategoryID,
		AssignedToUserID: input.AssignedToUserID,
		Weekdays:         strings.Join(parts, ","),
		StartDate:        start,
		Active:           true,
	}
	if err := h.db.Create(&r).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Дни, которые уже есть в расписании (от старта), получают задачу сразу.
	var schedules []models.DailySchedule
	h.orgDB(c).Where("date >= ?", start).Find(&schedules)
	for i := range schedules {
		materializeRecurring(h.db, r.OrganizationID, &schedules[i], schedules[i].Date)
	}

	c.JSON(http.StatusCreated, r)
}

// DELETE /admin/api/recurring-tasks/:id — больше не повторять.
// Прошлые и уже выполненные задачи остаются, будущие невыполненные удаляются.
func (h *AdminHandler) DeleteRecurringTask(c *gin.Context) {
	var r models.RecurringTask
	if err := h.orgDB(c).First(&r, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recurring task not found"})
		return
	}
	r.Active = false
	if err := h.db.Save(&r).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	today := utcDay(time.Now())
	h.db.Exec(`DELETE FROM schedule_tasks WHERE recurring_task_id = ? AND completed = false
		AND schedule_id IN (SELECT id FROM daily_schedules WHERE date >= ?)`, r.ID, today)
	c.JSON(http.StatusOK, gin.H{"message": "Recurring task stopped"})
}
