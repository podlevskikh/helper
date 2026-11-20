package models

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// «Ритм дома»: регулярная задача (Routine) — то, что повторяется в доме: обед, уборка кухни,
// лоток кота. Из неё каждый день собирается задача дня (ScheduleTask.RoutineID).
// Требования: helper/docs/work/routines/PRD.md.

// DishType — тип блюда дома («Обед», «Суп»…). Свой у каждого дома, заводит тот, кто ведёт дом.
type DishType struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"not null;uniqueIndex:idx_dish_type_org_name" json:"organization_id"`
	Name           string    `gorm:"not null;uniqueIndex:idx_dish_type_org_name" json:"name"`
	Position       int       `gorm:"default:0" json:"position"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Виды повторения.
const (
	RepeatWeekdays = "weekdays" // по дням недели (Weekdays: «1,3,5», ISO 1 = Пн)
	RepeatEveryN   = "every_n"  // каждые N дней от AnchorDate
)

// Как задача привязана ко времени — как у обычной задачи.
const (
	TimeExact = "exact" // Times: ["10:00","16:00"] — каждое время своя задача
	TimeSpan  = "span"  // Times: ["09:30","11:00"] — с и по
	TimeAny   = "any"   // когда удобно
)

// Категории «Ритма дома» — какие поля у задачи видны.
const (
	RoutineFood  = "food"
	RoutineClean = "clean"
	RoutineKid   = "kid"
	RoutinePet   = "pet"
	RoutineOther = "other"
)

// MaxRoutineDishes — сколько блюд может быть в приёме пищи.
const MaxRoutineDishes = 5

type Routine struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	OrganizationID uint   `gorm:"index;not null" json:"organization_id"`
	Kind           string `gorm:"type:varchar(16);not null" json:"kind"` // food | clean | kid | pet | other
	Title          string `gorm:"not null" json:"title"`
	Description    string `gorm:"type:text" json:"description"` // комментарий
	TaskCategoryID *uint  `gorm:"index" json:"task_category_id,omitempty"`
	Priority       string `json:"priority,omitempty"` // high | normal
	Active         bool   `gorm:"default:true" json:"active"`

	RepeatKind string    `gorm:"type:varchar(16);not null" json:"repeat_kind"`
	Weekdays   string    `json:"weekdays"`
	EveryN     int       `gorm:"default:1" json:"every_n"`
	StartDate  time.Time `gorm:"type:date;not null" json:"start_date"`
	// От какой даты считаются «каждые N дней»; после переноса на другой день — от него.
	AnchorDate time.Time `gorm:"type:date;not null" json:"anchor_date"`

	TimeMode string `gorm:"type:varchar(8);not null;default:'any'" json:"time_mode"`
	Times    string `gorm:"type:text" json:"times"` // JSON []string

	AssignedToUserID uint      `gorm:"index;not null" json:"assigned_to_user_id"`
	CreatedBy        uint      `gorm:"not null" json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	Dishes    []RoutineDish          `gorm:"foreignKey:RoutineID" json:"dishes"`
	Checklist []RoutineChecklistItem `gorm:"foreignKey:RoutineID" json:"checklist"`
}

// TimeList — времена задачи из JSON.
func (r Routine) TimeList() []string {
	var out []string
	if r.Times != "" {
		_ = json.Unmarshal([]byte(r.Times), &out)
	}
	return out
}

// RoutineDish — блюдо приёма пищи: номер, тип, порции.
type RoutineDish struct {
	ID         uint `gorm:"primaryKey" json:"id"`
	RoutineID  uint `gorm:"index;not null" json:"routine_id"`
	Position   int  `gorm:"not null" json:"position"` // 1..5
	DishTypeID uint `gorm:"not null" json:"dish_type_id"`
	Servings   int  `gorm:"not null;default:2" json:"servings"`
}

// RoutineChecklistItem — пункт «Что входит».
type RoutineChecklistItem struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	RoutineID uint   `gorm:"index;not null" json:"routine_id"`
	Position  int    `gorm:"not null" json:"position"`
	Text      string `gorm:"not null" json:"text"`
}

// RoutineSkip — задачу дня удалили вручную: в эту дату больше не ставим.
type RoutineSkip struct {
	ID        uint      `gorm:"primaryKey"`
	RoutineID uint      `gorm:"uniqueIndex:idx_routine_skip"`
	Date      time.Time `gorm:"type:date;uniqueIndex:idx_routine_skip"`
}

// Решения на день, когда исполнитель отдыхает.
const (
	DecisionMe   = "me"   // тому, кто поставил (по умолчанию)
	DecisionUser = "user" // другому работающему помощнику
	DecisionMove = "move" // на следующий рабочий день исполнителя (не еда)
	DecisionSkip = "skip" // не делать
)

// RoutineDayDecision — что делать с регулярной задачей в день, когда исполнитель отдыхает.
type RoutineDayDecision struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	RoutineID  uint       `gorm:"uniqueIndex:idx_routine_decision;not null" json:"routine_id"`
	Date       time.Time  `gorm:"type:date;uniqueIndex:idx_routine_decision;not null" json:"date"`
	Action     string     `gorm:"type:varchar(8);not null" json:"action"`
	UserID     *uint      `json:"user_id,omitempty"`
	TargetDate *time.Time `gorm:"type:date;index" json:"target_date,omitempty"` // для move
	DecidedBy  uint       `json:"decided_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// TaskDish — блюдо задачи-приёма пищи на день: номер и тип из регулярной задачи, выбранный рецепт и
// свои порции (0 — как у задачи). Нужно, когда у блюд разные порции (суп ×1 при остальных ×2).
type TaskDish struct {
	ID         uint  `gorm:"primaryKey" json:"id"`
	TaskID     uint  `gorm:"index;not null" json:"task_id"`
	Position   int   `gorm:"not null" json:"position"`
	RecipeID   *uint `gorm:"index" json:"recipe_id,omitempty"`
	DishTypeID *uint `json:"dish_type_id,omitempty"`
	Servings   int   `gorm:"default:0" json:"servings"`
}

// TaskDishView — блюдо задачи в ответе API.
type TaskDishView struct {
	Position   int    `json:"position"`
	RecipeID   *uint  `json:"recipe_id,omitempty"`
	DishTypeID *uint  `json:"dish_type_id,omitempty"`
	DishType   string `json:"dish_type,omitempty"`
	Servings   int    `json:"servings"` // уже с учётом задачи: 0 у блюда → порции задачи
}

// ChecklistEntry — пункт «Что входит» в задаче дня.
type ChecklistEntry struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// StandardServings — «стандартное» число порций приёма пищи: самое частое у блюд (при равенстве — большее).
func StandardServings(dishes []RoutineDish) int {
	cnt := map[int]int{}
	best, bestN := DefaultMealServings, 0
	for _, d := range dishes {
		cnt[d.Servings]++
	}
	for s, n := range cnt {
		if n > bestN || (n == bestN && s > best) {
			best, bestN = s, n
		}
	}
	return best
}

// fillRoutineDishes — у задач еды из «Ритма дома»: обычные порции — из регулярной задачи,
// блюда по номерам с типом и порциями.
func fillRoutineDishes(db *gorm.DB, tasks []ScheduleTask, ids []uint, idx map[uint]int) {
	routineIDs := []uint{}
	for _, id := range ids {
		if t := tasks[idx[id]]; t.RoutineID != nil {
			routineIDs = append(routineIDs, *t.RoutineID)
		}
	}
	if len(routineIDs) > 0 {
		var rd []RoutineDish
		db.Where("routine_id IN ?", routineIDs).Find(&rd)
		by := map[uint][]RoutineDish{}
		for _, d := range rd {
			by[d.RoutineID] = append(by[d.RoutineID], d)
		}
		for _, id := range ids {
			t := &tasks[idx[id]]
			if t.RoutineID != nil && len(by[*t.RoutineID]) > 0 {
				t.UsualServings = StandardServings(by[*t.RoutineID])
			}
		}
	}
	var dishes []TaskDish
	db.Where("task_id IN ?", ids).Order("position").Find(&dishes)
	if len(dishes) == 0 {
		return
	}
	typeIDs := []uint{}
	for _, d := range dishes {
		if d.DishTypeID != nil {
			typeIDs = append(typeIDs, *d.DishTypeID)
		}
	}
	names := map[uint]string{}
	if len(typeIDs) > 0 {
		var types []DishType
		db.Where("id IN ?", typeIDs).Find(&types)
		for _, t := range types {
			names[t.ID] = t.Name
		}
	}
	for _, d := range dishes {
		t := &tasks[idx[d.TaskID]]
		sv := d.Servings
		if sv <= 0 {
			sv = t.Servings
			if sv <= 0 {
				sv = t.UsualServings
			}
		}
		v := TaskDishView{Position: d.Position, RecipeID: d.RecipeID, DishTypeID: d.DishTypeID, Servings: sv}
		if d.DishTypeID != nil {
			v.DishType = names[*d.DishTypeID]
		}
		t.Dishes = append(t.Dishes, v)
	}
}
