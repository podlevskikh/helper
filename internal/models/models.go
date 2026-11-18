package models

import (
	"time"

	"gorm.io/gorm"
)

// Recipe represents a meal recipe
type Recipe struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	Name           string    `gorm:"not null" json:"name"`
	Description  string    `json:"description"`
	Ingredients  string    `gorm:"type:text" json:"ingredients"` // JSON or text list
	Instructions string    `gorm:"type:text" json:"instructions"`
	PrepTime     int       `json:"prep_time"` // in minutes
	CookTime     int       `json:"cook_time"` // in minutes
	Servings     int       `json:"servings"`
	Category     string    `json:"category,omitempty"` // DEPRECATED: use MealTimes relation instead
	FamilyMember string    `json:"family_member"` // all, adult, baby, specific person
	Tags         string    `json:"tags"` // comma-separated tags
	ImageURL     string    `json:"image_url"` // URL to recipe image
	VideoURL     string    `json:"video_url"` // URL to recipe video
	Rating       float64   `gorm:"default:0" json:"rating"` // 0-5 stars
	IsActive     bool      `gorm:"default:true" json:"is_active"` // whether recipe is active and can be scheduled
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Relations
	MealTimes []MealTime `gorm:"many2many:recipe_meal_times;" json:"meal_times,omitempty"` // multiple meal types for this recipe
}

// MealTime represents configured meal times
type MealTime struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	Name           string    `gorm:"not null" json:"name"` // breakfast, lunch, dinner, snack1, babyfood, etc
	DefaultTime  string    `gorm:"not null" json:"default_time"` // HH:MM format (primary time, kept for backward compatibility)
	DefaultTimes string    `gorm:"type:text" json:"default_times"` // JSON array of times ["09:00", "12:00", "15:00"]
	FamilyMember string    `json:"family_member"` // who this meal is for
	Active       bool      `gorm:"default:true" json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Relations
	Recipes []Recipe `gorm:"many2many:recipe_meal_times;" json:"recipes,omitempty"` // recipes for this meal type
}

// CleaningZone represents a zone in the house that needs cleaning
type CleaningZone struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	OrganizationID  uint      `gorm:"index;not null;default:1" json:"organization_id"`
	Name            string    `gorm:"not null" json:"name"` // bedroom, kitchen, bathroom, etc
	Description     string    `json:"description"`
	FrequencyPerWeek int      `gorm:"not null" json:"frequency_per_week"` // how many times per week
	Priority        string    `gorm:"default:'medium'" json:"priority"` // high, medium, low
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ChildcareSchedule represents daily childcare times (manually added each day)
type ChildcareSchedule struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	Date           time.Time `gorm:"not null;index" json:"date"` // date for this schedule
	StartTime   string    `gorm:"not null" json:"start_time"` // HH:MM format
	EndTime     string    `gorm:"not null" json:"end_time"`   // HH:MM format
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DailySchedule represents the generated daily schedule for the helper
type DailySchedule struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	Date           time.Time `gorm:"not null;index" json:"date"`
	Generated bool      `gorm:"default:false" json:"generated"` // whether schedule was auto-generated
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	
	// Relations
	Tasks []ScheduleTask `gorm:"foreignKey:ScheduleID" json:"tasks"`
}

// TaskCategory — настраиваемая категория задач организации.
// Системные категории (is_default=true) создаются при миграции и не могут быть удалены.
type TaskCategory struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"uniqueIndex:idx_org_category_name;index;not null" json:"organization_id"`
	Name           string    `gorm:"uniqueIndex:idx_org_category_name;not null" json:"name"`
	Icon           string    `json:"icon,omitempty"`            // emoji или имя иконки
	Color          string    `json:"color,omitempty"`           // hex (#RRGGBB)
	IsDefault      bool      `gorm:"default:false" json:"is_default"` // системная — нельзя удалить
	// Постоянный ключ системной категории (meal | cleaning | childcare | custom):
	// по нему, а не по названию, планировщик находит категорию — название можно менять.
	SystemKey      string    `gorm:"index" json:"system_key,omitempty"`
	SortOrder      int       `gorm:"default:0" json:"sort_order"`
	// Важность по умолчанию для задач категории: high | mid | normal
	Priority       string    `gorm:"default:'normal'" json:"priority"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ScheduleTask represents a single task in the daily schedule
type ScheduleTask struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	ScheduleID     uint      `gorm:"not null;index" json:"schedule_id"`
	TaskType    string    `gorm:"not null" json:"task_type"` // meal, cleaning, childcare, custom (legacy)
	Time        string    `json:"time"` // HH:MM format (can be empty for flexible tasks like cleaning)
	EndTime     string    `json:"end_time"` // HH:MM format (for childcare tasks with time range)
	Duration    int       `json:"duration"` // in minutes
	Title       string    `gorm:"not null" json:"title"`
	Description string    `json:"description"`
	RecipeID    *uint     `json:"recipe_id,omitempty"` // if task_type is meal (deprecated, use Recipes relation)
	ZoneID      *uint     `json:"zone_id,omitempty"`   // if task_type is cleaning (deprecated, use Zones relation)
	// M2: категория и назначение
	TaskCategoryID     *uint `gorm:"index" json:"task_category_id,omitempty"`
	AssignedToUserID   *uint `gorm:"index" json:"assigned_to_user_id,omitempty"`
	Completed   bool      `gorm:"default:false" json:"completed"`
	// Когда помощник отметил задачу выполненной (nil — не выполнена)
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Важность задачи: high | mid | normal; пусто — берётся из категории
	Priority    string    `json:"priority,omitempty"`
	// Задача создана из повторяющейся (RecurringTask) — nil для разовых
	RecurringTaskID *uint     `gorm:"index" json:"recurring_task_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Relations
	Recipe       *Recipe       `gorm:"foreignKey:RecipeID" json:"recipe,omitempty"` // deprecated, use Recipes
	Recipes      []Recipe      `gorm:"many2many:meal_recipes;" json:"recipes,omitempty"`
	Zone         *CleaningZone `gorm:"foreignKey:ZoneID" json:"zone,omitempty"` // deprecated, use Zones
	Zones        []CleaningZone `gorm:"many2many:task_zones;" json:"zones,omitempty"`
	TaskCategory *TaskCategory  `gorm:"foreignKey:TaskCategoryID" json:"task_category,omitempty"`
}

// ShoppingListItem represents items needed for shopping
type ShoppingListItem struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	Item           string    `gorm:"not null" json:"item"`
	Quantity    string    `json:"quantity"`
	Category    string    `json:"category"` // produce, dairy, meat, etc
	Purchased   bool      `gorm:"default:false" json:"purchased"`
	AddedBy     string    `json:"added_by"` // admin or helper
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Settings represents per-organisation system settings
type Settings struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"uniqueIndex:idx_org_setting_key;index;not null;default:1" json:"organization_id"`
	Key            string    `gorm:"uniqueIndex:idx_org_setting_key;not null" json:"key"`
	Value       string    `json:"value"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Holiday represents a public holiday or day off
type Holiday struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"not null" json:"name"`
	Date        time.Time `gorm:"not null" json:"date"` // Specific date
	IsRecurring bool      `gorm:"default:true" json:"is_recurring"` // Repeats every year
	Country     string    `json:"country"` // e.g., "Cyprus"
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RecipeComment represents a comment on a recipe
type RecipeComment struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	RecipeID       uint      `gorm:"not null" json:"recipe_id"`
	Comment   string    `gorm:"type:text" json:"comment"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}


// legacyTypeCategory — task_type задач планировщика, которым ставится системная категория.
var legacyTypeCategory = map[string]bool{
	"meal":      true,
	"cleaning":  true,
	"childcare": true,
}

// BeforeCreate проставляет категорию задачам планировщика (еда/уборка/ребёнок),
// чтобы новые задачи сразу имели иконку и важность категории.
func (t *ScheduleTask) BeforeCreate(tx *gorm.DB) error {
	if t.TaskCategoryID != nil {
		return nil
	}
	if !legacyTypeCategory[t.TaskType] {
		return nil
	}
	orgID := t.OrganizationID
	if orgID == 0 {
		orgID = 1
	}
	var cat TaskCategory
	if err := tx.Session(&gorm.Session{NewDB: true}).
		Where("organization_id = ? AND system_key = ?", orgID, t.TaskType).
		First(&cat).Error; err == nil {
		t.TaskCategoryID = &cat.ID
	}
	return nil
}

// RecurringTask — повторяющаяся задача нанимателя («полить цветы по средам»).
// В конкретный день она превращается в обычную ScheduleTask (task_type=custom)
// при первом открытии этого дня (см. handlers.materializeRecurring).
type RecurringTask struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	OrganizationID   uint      `gorm:"index;not null" json:"organization_id"`
	Title            string    `gorm:"not null" json:"title"`
	Description      string    `json:"description"`
	Time             string    `json:"time"`     // HH:MM или пусто — «когда удобно»
	EndTime          string    `json:"end_time"` // HH:MM — конец промежутка
	Priority         string    `json:"priority,omitempty"`
	TaskCategoryID   *uint     `gorm:"index" json:"task_category_id,omitempty"`
	AssignedToUserID *uint     `gorm:"index" json:"assigned_to_user_id,omitempty"`
	// Дни недели через запятую, ISO: 1 = Пн … 7 = Вс
	Weekdays  string    `gorm:"not null" json:"weekdays"`
	StartDate time.Time `gorm:"not null" json:"start_date"`
	Active    bool      `gorm:"default:true" json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	TaskCategory *TaskCategory `gorm:"foreignKey:TaskCategoryID" json:"task_category,omitempty"`
}

// RecurringTaskSkip — день, в который повторяющуюся задачу удалили вручную:
// больше не создаём её в этот день.
type RecurringTaskSkip struct {
	ID              uint      `gorm:"primaryKey"`
	RecurringTaskID uint      `gorm:"uniqueIndex:idx_recurring_skip"`
	Date            time.Time `gorm:"uniqueIndex:idx_recurring_skip"`
}
