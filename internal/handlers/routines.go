package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/routines"
)

// «Ритм дома»: регулярные задачи, типы блюд, решения на день, когда помощник отдыхает.
// Требования — helper/docs/work/routines/PRD.md.

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

type routineDishInput struct {
	DishTypeID uint `json:"dish_type_id"`
	Servings   int  `json:"servings"`
}

type routineInput struct {
	Kind             string             `json:"kind"`
	Title            string             `json:"title"`
	Description      string             `json:"description"`
	TaskCategoryID   *uint              `json:"task_category_id"`
	Priority         string             `json:"priority"`
	Active           *bool              `json:"active"`
	RepeatKind       string             `json:"repeat_kind"`
	Weekdays         []int              `json:"weekdays"`
	EveryN           int                `json:"every_n"`
	StartDate        string             `json:"start_date"`
	TimeMode         string             `json:"time_mode"`
	Times            []string           `json:"times"`
	AssignedToUserID uint               `json:"assigned_to_user_id"`
	Dishes           []routineDishInput `json:"dishes"`
	Checklist        []string           `json:"checklist"`
}

func bad(c *gin.Context, code, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg, "code": code})
}

// build проверяет ввод и собирает регулярную задачу (без id, дома и автора).
func (h *AdminHandler) buildRoutine(c *gin.Context, in routineInput, r *models.Routine) ([]models.RoutineDish, []models.RoutineChecklistItem, bool) {
	orgID := h.orgID(c)
	switch in.Kind {
	case models.RoutineFood, models.RoutineClean, models.RoutineKid, models.RoutinePet, models.RoutineOther:
	default:
		bad(c, "bad_kind", "kind must be food|clean|kid|pet|other")
		return nil, nil, false
	}
	title := strings.TrimSpace(in.Title)
	if title == "" || utf8.RuneCountInString(title) > 120 {
		bad(c, "bad_title", "title is required, up to 120 characters")
		return nil, nil, false
	}
	desc := strings.TrimSpace(in.Description)
	if utf8.RuneCountInString(desc) > 2000 {
		bad(c, "bad_description", "description is too long")
		return nil, nil, false
	}
	prio := ""
	if in.Priority == "high" {
		prio = "high"
	}
	// Как часто
	switch in.RepeatKind {
	case models.RepeatWeekdays:
		days := routines.SortedWeekdays(in.Weekdays)
		if days == "" {
			bad(c, "bad_weekdays", "choose at least one weekday (1 = Mon … 7 = Sun)")
			return nil, nil, false
		}
		r.Weekdays, r.EveryN = days, 1
	case models.RepeatEveryN:
		if in.EveryN < 1 || in.EveryN > 60 {
			bad(c, "bad_every_n", "every_n must be 1..60")
			return nil, nil, false
		}
		r.EveryN, r.Weekdays = in.EveryN, ""
	default:
		bad(c, "bad_repeat", "repeat_kind must be weekdays|every_n")
		return nil, nil, false
	}
	start := clock.OrgToday(h.db, orgID)
	if in.StartDate != "" {
		d, err := time.Parse("2006-01-02", in.StartDate)
		if err != nil {
			bad(c, "bad_start_date", "start_date: YYYY-MM-DD")
			return nil, nil, false
		}
		start = clock.Day(d)
	}
	// Время — как у обычной задачи
	times := []string{}
	for _, t := range in.Times {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !hhmm.MatchString(t) {
			bad(c, "bad_time", "times: HH:MM")
			return nil, nil, false
		}
		times = append(times, t)
	}
	switch in.TimeMode {
	case models.TimeExact:
		if len(times) < 1 || len(times) > 6 {
			bad(c, "bad_times", "exact time: 1..6 times")
			return nil, nil, false
		}
		sort.Strings(times)
	case models.TimeSpan:
		if len(times) != 2 || times[0] >= times[1] {
			bad(c, "bad_times", "time span: from < to")
			return nil, nil, false
		}
	case models.TimeAny, "":
		in.TimeMode, times = models.TimeAny, []string{}
	default:
		bad(c, "bad_time_mode", "time_mode must be exact|span|any")
		return nil, nil, false
	}
	rawTimes, _ := json.Marshal(times)
	// Кто делает — один человек из дома
	var n int64
	h.db.Model(&models.Membership{}).Where("user_id = ? AND organization_id = ? AND status = ?",
		in.AssignedToUserID, orgID, models.MembershipActive).Count(&n)
	if in.AssignedToUserID == 0 || n == 0 {
		bad(c, "bad_assignee", "assigned_to_user_id: a member of this home")
		return nil, nil, false
	}
	if !checkTaskRefs(c, h.db, orgID, in.TaskCategoryID, nil) {
		return nil, nil, false
	}
	// Еда: блюда 1..5 с типом и порциями
	var dishes []models.RoutineDish
	if in.Kind == models.RoutineFood {
		if len(in.Dishes) < 1 || len(in.Dishes) > models.MaxRoutineDishes {
			bad(c, "bad_dishes", "a meal has 1..5 dishes")
			return nil, nil, false
		}
		for i, d := range in.Dishes {
			if d.Servings < 1 || d.Servings > models.MaxMealServings {
				bad(c, "bad_servings", "servings must be 1..20")
				return nil, nil, false
			}
			h.db.Model(&models.DishType{}).Where("id = ? AND organization_id = ?", d.DishTypeID, orgID).Count(&n)
			if n == 0 {
				bad(c, "bad_dish_type", "unknown dish_type_id")
				return nil, nil, false
			}
			dishes = append(dishes, models.RoutineDish{Position: i + 1, DishTypeID: d.DishTypeID, Servings: d.Servings})
		}
	}
	// Что входит
	var items []models.RoutineChecklistItem
	if in.Kind != models.RoutineFood {
		if len(in.Checklist) > 15 {
			bad(c, "bad_checklist", "up to 15 items")
			return nil, nil, false
		}
		for _, t := range in.Checklist {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if utf8.RuneCountInString(t) > 80 {
				bad(c, "bad_checklist", "item up to 80 characters")
				return nil, nil, false
			}
			items = append(items, models.RoutineChecklistItem{Position: len(items) + 1, Text: t})
		}
	}
	repeatChanged := r.RepeatKind != in.RepeatKind || r.EveryN != max(1, in.EveryN) || !r.StartDate.Equal(start)
	r.Kind, r.Title, r.Description, r.TaskCategoryID, r.Priority = in.Kind, title, desc, in.TaskCategoryID, prio
	r.RepeatKind, r.StartDate = in.RepeatKind, start
	if repeatChanged || r.AnchorDate.IsZero() {
		r.AnchorDate = start
	}
	r.TimeMode, r.Times = in.TimeMode, string(rawTimes)
	r.AssignedToUserID = in.AssignedToUserID
	if in.Active != nil {
		r.Active = *in.Active
	}
	return dishes, items, true
}

func (h *AdminHandler) loadRoutine(c *gin.Context) (*models.Routine, bool) {
	var r models.Routine
	if err := h.orgDB(c).Preload("Dishes", func(q *gorm.DB) *gorm.DB { return q.Order("position") }).
		Preload("Checklist", func(q *gorm.DB) *gorm.DB { return q.Order("position") }).
		First(&r, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Routine not found"})
		return nil, false
	}
	return &r, true
}

// refreshRoutine — после правки: будущие невыполненные задачи — заново по новым правилам.
func (h *AdminHandler) refreshRoutine(orgID, routineID uint) {
	_ = routines.ClearFuture(h.db, orgID, routineID)
	routines.EnsureDays(h.db, orgID, routines.Days)
}

func saveParts(tx *gorm.DB, id uint, dishes []models.RoutineDish, items []models.RoutineChecklistItem) error {
	if err := tx.Where("routine_id = ?", id).Delete(&models.RoutineDish{}).Error; err != nil {
		return err
	}
	if err := tx.Where("routine_id = ?", id).Delete(&models.RoutineChecklistItem{}).Error; err != nil {
		return err
	}
	for i := range dishes {
		dishes[i].ID, dishes[i].RoutineID = 0, id
		if err := tx.Create(&dishes[i]).Error; err != nil {
			return err
		}
	}
	for i := range items {
		items[i].ID, items[i].RoutineID = 0, id
		if err := tx.Create(&items[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

// GET /admin/api/routines
func (h *AdminHandler) GetRoutines(c *gin.Context) {
	var list []models.Routine
	if err := h.orgDB(c).Preload("Dishes", func(q *gorm.DB) *gorm.DB { return q.Order("position") }).
		Preload("Checklist", func(q *gorm.DB) *gorm.DB { return q.Order("position") }).
		Order("id").Find(&list).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

// GET /admin/api/routines/:id
func (h *AdminHandler) GetRoutine(c *gin.Context) {
	if r, ok := h.loadRoutine(c); ok {
		c.JSON(http.StatusOK, r)
	}
}

// POST /admin/api/routines
func (h *AdminHandler) CreateRoutine(c *gin.Context) {
	var in routineInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "bad_json", err.Error())
		return
	}
	r := models.Routine{Active: true}
	dishes, items, ok := h.buildRoutine(c, in, &r)
	if !ok {
		return
	}
	r.OrganizationID = h.orgID(c)
	r.CreatedBy = middleware.MustMembership(c).UserID
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Dishes", "Checklist").Create(&r).Error; err != nil {
			return err
		}
		return saveParts(tx, r.ID, dishes, items)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	routines.EnsureDays(h.db, r.OrganizationID, routines.Days)
	c.Params = append(c.Params, gin.Param{Key: "id", Value: strconv.FormatUint(uint64(r.ID), 10)})
	if out, ok := h.loadRoutine(c); ok {
		c.JSON(http.StatusCreated, out)
	}
}

// PUT /admin/api/routines/:id
func (h *AdminHandler) UpdateRoutine(c *gin.Context) {
	r, ok := h.loadRoutine(c)
	if !ok {
		return
	}
	var in routineInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "bad_json", err.Error())
		return
	}
	dishes, items, ok := h.buildRoutine(c, in, r)
	if !ok {
		return
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Dishes", "Checklist").Save(r).Error; err != nil {
			return err
		}
		return saveParts(tx, r.ID, dishes, items)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.refreshRoutine(r.OrganizationID, r.ID)
	if out, ok := h.loadRoutine(c); ok {
		c.JSON(http.StatusOK, out)
	}
}

// PATCH /admin/api/routines/:id/active {active} — пауза без удаления.
func (h *AdminHandler) SetRoutineActive(c *gin.Context) {
	r, ok := h.loadRoutine(c)
	if !ok {
		return
	}
	var in struct {
		Active *bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Active == nil {
		bad(c, "bad_active", "active is required")
		return
	}
	if err := h.db.Model(&models.Routine{}).Where("id = ?", r.ID).Update("active", *in.Active).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.refreshRoutine(r.OrganizationID, r.ID)
	r.Active = *in.Active
	c.JSON(http.StatusOK, r)
}

// DELETE /admin/api/routines/:id — будущие невыполненные задачи тоже; прошлые и сделанные остаются.
func (h *AdminHandler) DeleteRoutine(c *gin.Context) {
	r, ok := h.loadRoutine(c)
	if !ok {
		return
	}
	_ = routines.ClearFuture(h.db, r.OrganizationID, r.ID)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{&models.RoutineDish{}, &models.RoutineChecklistItem{}, &models.RoutineSkip{}, &models.RoutineDayDecision{}} {
			if err := tx.Where("routine_id = ?", r.ID).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&models.Routine{}, r.ID).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Routine deleted"})
}

// ── Типы блюд ─────────────────────────────────────────────────────────────────

// GET /admin/api/dish-types
func (h *AdminHandler) GetDishTypes(c *gin.Context) {
	var list []models.DishType
	if err := h.orgDB(c).Order("position, id").Find(&list).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

func dishTypeName(c *gin.Context) (string, bool) {
	var in struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "bad_json", err.Error())
		return "", false
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 40 {
		bad(c, "bad_name", "name is required, up to 40 characters")
		return "", false
	}
	return name, true
}

func (h *AdminHandler) dishTypeTaken(orgID uint, name string, except uint) bool {
	var n int64
	h.db.Model(&models.DishType{}).Where("organization_id = ? AND lower(name) = lower(?) AND id <> ?", orgID, name, except).Count(&n)
	return n > 0
}

// POST /admin/api/dish-types {name}
func (h *AdminHandler) CreateDishType(c *gin.Context) {
	name, ok := dishTypeName(c)
	if !ok {
		return
	}
	orgID := h.orgID(c)
	if h.dishTypeTaken(orgID, name, 0) {
		c.JSON(http.StatusConflict, gin.H{"error": "dish type exists", "code": "dish_type_exists"})
		return
	}
	var maxPos int
	h.db.Model(&models.DishType{}).Where("organization_id = ?", orgID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
	t := models.DishType{OrganizationID: orgID, Name: name, Position: maxPos + 1}
	if err := h.db.Create(&t).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, t)
}

// PUT /admin/api/dish-types/:id {name}
func (h *AdminHandler) UpdateDishType(c *gin.Context) {
	var t models.DishType
	if err := h.orgDB(c).First(&t, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Dish type not found"})
		return
	}
	name, ok := dishTypeName(c)
	if !ok {
		return
	}
	if h.dishTypeTaken(t.OrganizationID, name, t.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "dish type exists", "code": "dish_type_exists"})
		return
	}
	if err := h.db.Model(&t).Update("name", name).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, t)
}

// DELETE /admin/api/dish-types/:id — только неиспользуемый тип.
func (h *AdminHandler) DeleteDishType(c *gin.Context) {
	var t models.DishType
	if err := h.orgDB(c).First(&t, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Dish type not found"})
		return
	}
	var a, b int64
	h.db.Model(&models.Recipe{}).Where("dish_type_id = ?", t.ID).Count(&a)
	h.db.Model(&models.RoutineDish{}).Where("dish_type_id = ?", t.ID).Count(&b)
	if a+b > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "dish type is used by recipes or routines", "code": "dish_type_in_use"})
		return
	}
	if err := h.db.Delete(&t).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Dish type deleted"})
}

// ── Помощник отдыхает ────────────────────────────────────────────────────────

type dayOffItem struct {
	RoutineID  uint     `json:"routine_id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	TimeMode   string   `json:"time_mode"`
	Times      []string `json:"times"`
	AssigneeID uint     `json:"assignee_id"`
	Assignee   string   `json:"assignee"`
	Reason     string   `json:"reason"`
	Holiday    string   `json:"holiday,omitempty"`
	Action     string   `json:"action"` // me | user | move | skip
	UserID     *uint    `json:"user_id,omitempty"`
	TargetDate string   `json:"target_date,omitempty"`
	CanMove    bool     `json:"can_move"`
	MoveDate   string   `json:"move_date,omitempty"` // ближайший рабочий день исполнителя
}

type personRef struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// nextWorkday — ближайший рабочий день человека после date (в пределах двух недель).
func nextWorkday(db *gorm.DB, orgID, userID uint, date time.Time) (time.Time, bool) {
	for i := 1; i <= 14; i++ {
		d := date.AddDate(0, 0, i)
		if calendar.IsWorking(db, orgID, userID, d) {
			return d, true
		}
	}
	return time.Time{}, false
}

func memberName(db *gorm.DB, id uint) string {
	var u models.User
	if db.Select("id", "name", "email").First(&u, id).Error != nil {
		return ""
	}
	if strings.TrimSpace(u.Name) != "" {
		return u.Name
	}
	if i := strings.Index(u.Email, "@"); i > 0 {
		return u.Email[:i]
	}
	return u.Email
}

func parseDayParam(c *gin.Context, raw string) (time.Time, bool) {
	d, err := time.Parse("2006-01-02", raw)
	if err != nil {
		bad(c, "bad_date", "date: YYYY-MM-DD")
		return time.Time{}, false
	}
	return clock.Day(d), true
}

// GET /admin/api/routines/day-off?date= — регулярные задачи отдыхающих в этот день и работающие помощники.
func (h *AdminHandler) GetRoutinesDayOff(c *gin.Context) {
	date, ok := parseDayParam(c, c.Query("date"))
	if !ok {
		return
	}
	orgID := h.orgID(c)
	var list []models.Routine
	h.db.Where("organization_id = ? AND active = ? AND start_date <= ?", orgID, true, date).Order("id").Find(&list)
	items := []dayOffItem{}
	for _, r := range list {
		if !routines.DueByRule(r, date) {
			continue
		}
		st, _ := calendar.StatusOn(h.db, orgID, r.AssignedToUserID, date)
		if st.Working {
			continue
		}
		it := dayOffItem{RoutineID: r.ID, Title: r.Title, Kind: r.Kind, TimeMode: r.TimeMode, Times: r.TimeList(),
			AssigneeID: r.AssignedToUserID, Assignee: memberName(h.db, r.AssignedToUserID), Reason: st.Reason,
			Holiday: st.Holiday, Action: models.DecisionMe, CanMove: r.Kind != models.RoutineFood}
		if it.Times == nil {
			it.Times = []string{}
		}
		if d, ok := nextWorkday(h.db, orgID, r.AssignedToUserID, date); ok && it.CanMove {
			it.MoveDate = d.Format("2006-01-02")
		} else {
			it.CanMove = false
		}
		var dec models.RoutineDayDecision
		if h.db.Where("routine_id = ? AND date = ?", r.ID, date).First(&dec).Error == nil {
			it.Action, it.UserID = dec.Action, dec.UserID
			if dec.TargetDate != nil {
				it.TargetDate = dec.TargetDate.Format("2006-01-02")
			}
		}
		items = append(items, it)
	}
	// Кто работает в этот день — кому можно передать.
	var ms []models.Membership
	h.db.Where("organization_id = ? AND status = ? AND role IN ?", orgID, models.MembershipActive,
		[]models.Role{models.RoleHelper, models.RoleManager}).Order("id").Find(&ms)
	working := []personRef{}
	for _, m := range ms {
		if calendar.IsWorking(h.db, orgID, m.UserID, date) {
			working = append(working, personRef{ID: m.UserID, Name: memberName(h.db, m.UserID)})
		}
	}
	c.JSON(http.StatusOK, gin.H{"date": date.Format("2006-01-02"), "items": items, "working": working})
}

// PUT /admin/api/routines/:id/day-decision {date, action, user_id} — что делать в день, когда исполнитель отдыхает.
func (h *AdminHandler) SetRoutineDayDecision(c *gin.Context) {
	r, ok := h.loadRoutine(c)
	if !ok {
		return
	}
	var in struct {
		Date   string `json:"date"`
		Action string `json:"action"`
		UserID *uint  `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "bad_json", err.Error())
		return
	}
	date, ok := parseDayParam(c, in.Date)
	if !ok {
		return
	}
	orgID := r.OrganizationID
	if date.Before(clock.OrgToday(h.db, orgID)) {
		bad(c, "past_date", "the day has passed")
		return
	}
	if !routines.DueByRule(*r, date) {
		bad(c, "not_due", "the routine is not scheduled on this day")
		return
	}
	if calendar.IsWorking(h.db, orgID, r.AssignedToUserID, date) {
		bad(c, "assignee_works", "the assignee works on this day")
		return
	}
	dec := models.RoutineDayDecision{RoutineID: r.ID, Date: date, Action: in.Action,
		DecidedBy: middleware.MustMembership(c).UserID}
	switch in.Action {
	case models.DecisionMe, models.DecisionSkip:
	case models.DecisionUser:
		if in.UserID == nil {
			bad(c, "bad_user", "user_id is required")
			return
		}
		var n int64
		h.db.Model(&models.Membership{}).Where("user_id = ? AND organization_id = ? AND status = ?",
			*in.UserID, orgID, models.MembershipActive).Count(&n)
		if n == 0 || !calendar.IsWorking(h.db, orgID, *in.UserID, date) {
			bad(c, "bad_user", "the person is not in this home or does not work on this day")
			return
		}
		dec.UserID = in.UserID
	case models.DecisionMove:
		if r.Kind == models.RoutineFood {
			bad(c, "cannot_move_meal", "meals are not moved to another day")
			return
		}
		t, ok := nextWorkday(h.db, orgID, r.AssignedToUserID, date)
		if !ok {
			bad(c, "no_workday", "no working day in the next two weeks")
			return
		}
		dec.TargetDate = &t
	default:
		bad(c, "bad_action", "action must be me|user|move|skip")
		return
	}
	var old models.RoutineDayDecision
	hadOld := h.db.Where("routine_id = ? AND date = ?", r.ID, date).First(&old).Error == nil
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if hadOld {
			if err := tx.Delete(&old).Error; err != nil {
				return err
			}
		}
		if in.Action != models.DecisionMe {
			if err := tx.Create(&dec).Error; err != nil {
				return err
			}
		}
		// «Каждые N дней»: после переноса следующий раз считается от нового дня.
		if in.Action == models.DecisionMove && r.RepeatKind == models.RepeatEveryN {
			if err := tx.Model(&models.Routine{}).Where("id = ?", r.ID).Update("anchor_date", *dec.TargetDate).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = routines.ClearDay(h.db, r.ID, date)
	if hadOld && old.TargetDate != nil {
		_ = routines.ClearDay(h.db, r.ID, *old.TargetDate)
	}
	if in.Action == models.DecisionMove && r.RepeatKind == models.RepeatEveryN {
		_ = routines.ClearFuture(h.db, orgID, r.ID)
	}
	for _, d := range []*time.Time{&date, dec.TargetDate} {
		if d == nil {
			continue
		}
		if s, err := models.EnsureDailySchedule(h.db, orgID, *d, false); err == nil {
			routines.MaterializeDay(h.db, orgID, &s, *d)
		}
	}
	routines.EnsureDays(h.db, orgID, routines.Days)
	c.JSON(http.StatusOK, gin.H{"routine_id": r.ID, "date": date.Format("2006-01-02"), "action": in.Action,
		"user_id": dec.UserID, "target_date": dec.TargetDate})
}

// ── «Что входит» в задаче дня ────────────────────────────────────────────────

// POST /helper/api/tasks/:id/checklist/:index {done} — отметить пункт. Исполнитель или тот, кто ведёт расписание.
func (h *HelperHandler) SetChecklistItem(c *gin.Context) {
	var task models.ScheduleTask
	if err := h.orgDB(c).First(&task, c.Param("id")).Error; err != nil || hiddenFromMember(c, &task) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}
	idx, err := strconv.Atoi(c.Param("index"))
	var items []models.ChecklistEntry
	if task.Checklist != "" {
		_ = json.Unmarshal([]byte(task.Checklist), &items)
	}
	if err != nil || idx < 0 || idx >= len(items) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Checklist item not found"})
		return
	}
	var in struct {
		Done *bool `json:"done"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Done == nil {
		bad(c, "bad_done", "done is required")
		return
	}
	items[idx].Done = *in.Done
	raw, _ := json.Marshal(items)
	if err := h.db.Model(&models.ScheduleTask{}).Where("id = ?", task.ID).Update("checklist", string(raw)).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	task.Checklist = string(raw)
	c.JSON(http.StatusOK, task)
}
