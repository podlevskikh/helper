package models

import (
	"time"

	"gorm.io/gorm"
)

// Recipe represents a meal recipe
type Recipe struct {
	ID             uint    `gorm:"primaryKey" json:"id"`
	OrganizationID uint    `gorm:"index;not null;default:1" json:"organization_id"`
	Name           string  `gorm:"not null" json:"name"`
	Description    string  `json:"description"`
	Ingredients    string  `gorm:"type:text" json:"ingredients"` // JSON or text list
	Instructions   string  `gorm:"type:text" json:"instructions"`
	PrepTime       int     `json:"prep_time"` // in minutes
	CookTime       int     `json:"cook_time"` // in minutes
	Servings       int     `json:"servings"`
	Category       string  `json:"category,omitempty"`            // DEPRECATED: use MealTimes relation instead
	FamilyMember   string  `json:"family_member"`                 // all, adult, baby, specific person
	Tags           string  `json:"tags"`                          // comma-separated tags
	ImageURL       string  `json:"image_url"`                     // URL to recipe image
	VideoURL       string  `json:"video_url"`                     // URL to recipe video
	Rating         float64 `gorm:"default:0" json:"rating"`       // 0-5 stars
	IsActive       bool    `gorm:"default:true" json:"is_active"` // whether recipe is active and can be scheduled
	// Кто последним правил рецепт (имя на момент правки) — видно в карточке: «Изменила Мария, 20 нояб.».
	UpdatedByID   *uint  `json:"updated_by_id,omitempty"`
	UpdatedByName string `json:"updated_by_name,omitempty"`
	// Тип блюда (свой у каждого дома: «Обед», «Суп»…) — по нему регулярная задача подбирает блюдо. Один тип у рецепта.
	DishTypeID *uint `gorm:"index" json:"dish_type_id,omitempty"`
	// Откуда рецепт (страница сайта) — в карточке «Источник». Видео — отдельно, в VideoURL.
	SourceURL string `json:"source_url"`
	// Загрузка списком, которой создан рецепт: «Отменить загрузку» удаляет созданные ею рецепты.
	ImportID  *uint     `gorm:"index" json:"import_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Relations
	MealTimes []MealTime `gorm:"many2many:recipe_meal_times;" json:"meal_times,omitempty"` // multiple meal types for this recipe
}

// MealTime represents configured meal times
type MealTime struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	OrganizationID uint   `gorm:"index;not null;default:1" json:"organization_id"`
	Name           string `gorm:"not null" json:"name"`           // breakfast, lunch, dinner, snack1, babyfood, etc
	DefaultTime    string `gorm:"not null" json:"default_time"`   // HH:MM format (primary time, kept for backward compatibility)
	DefaultTimes   string `gorm:"type:text" json:"default_times"` // JSON array of times ["09:00", "12:00", "15:00"]
	FamilyMember   string `json:"family_member"`                  // who this meal is for
	Active         bool   `gorm:"default:true" json:"active"`
	// Перенесено в регулярную задачу «Ритма дома»: старый генератор такую запись больше не ставит в день.
	MigratedRoutineID *uint     `gorm:"index" json:"migrated_routine_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`

	// Relations
	Recipes []Recipe `gorm:"many2many:recipe_meal_times;" json:"recipes,omitempty"` // recipes for this meal type
}

// CleaningZone represents a zone in the house that needs cleaning
type CleaningZone struct {
	ID               uint   `gorm:"primaryKey" json:"id"`
	OrganizationID   uint   `gorm:"index;not null;default:1" json:"organization_id"`
	Name             string `gorm:"not null" json:"name"` // bedroom, kitchen, bathroom, etc
	Description      string `json:"description"`
	FrequencyPerWeek int    `gorm:"not null" json:"frequency_per_week"` // how many times per week
	Priority         string `gorm:"default:'medium'" json:"priority"`   // high, medium, low
	// Перенесено в регулярную задачу «Ритма дома»: старый генератор такую запись больше не ставит в день.
	MigratedRoutineID *uint     `gorm:"index" json:"migrated_routine_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ChildcareSchedule represents daily childcare times (manually added each day)
type ChildcareSchedule struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	Date           time.Time `gorm:"not null;index" json:"date"` // date for this schedule
	StartTime      string    `gorm:"not null" json:"start_time"` // HH:MM format
	EndTime        string    `gorm:"not null" json:"end_time"`   // HH:MM format
	Notes          string    `json:"notes"`
	// Перенесено в регулярную задачу «Ритма дома»: старый генератор такую запись больше не ставит в день.
	MigratedRoutineID *uint     `gorm:"index" json:"migrated_routine_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// DailySchedule represents the generated daily schedule for the helper
type DailySchedule struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	Date           time.Time `gorm:"not null;index" json:"date"`
	Generated      bool      `gorm:"default:false" json:"generated"` // whether schedule was auto-generated
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Relations
	Tasks []ScheduleTask `gorm:"foreignKey:ScheduleID" json:"tasks"`
}

// TaskCategory — настраиваемая категория задач организации.
// Системные категории (is_default=true) создаются при миграции и не могут быть удалены.
type TaskCategory struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	OrganizationID uint   `gorm:"uniqueIndex:idx_org_category_name;index;not null" json:"organization_id"`
	Name           string `gorm:"uniqueIndex:idx_org_category_name;not null" json:"name"`
	Icon           string `json:"icon,omitempty"`                  // emoji или имя иконки
	Color          string `json:"color,omitempty"`                 // hex (#RRGGBB)
	IsDefault      bool   `gorm:"default:false" json:"is_default"` // системная — нельзя удалить
	// Постоянный ключ системной категории (meal | cleaning | childcare | custom):
	// по нему, а не по названию, планировщик находит категорию — название можно менять.
	SystemKey string `gorm:"index" json:"system_key,omitempty"`
	SortOrder int    `gorm:"default:0" json:"sort_order"`
	// Важность по умолчанию для задач категории: high | mid | normal
	Priority  string    `gorm:"default:'normal'" json:"priority"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ScheduleTask represents a single task in the daily schedule
type ScheduleTask struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	OrganizationID uint   `gorm:"index;not null;default:1" json:"organization_id"`
	ScheduleID     uint   `gorm:"not null;index" json:"schedule_id"`
	TaskType       string `gorm:"not null" json:"task_type"` // meal, cleaning, childcare, custom (legacy)
	Time           string `json:"time"`                      // HH:MM format (can be empty for flexible tasks like cleaning)
	EndTime        string `json:"end_time"`                  // HH:MM format (for childcare tasks with time range)
	Duration       int    `json:"duration"`                  // in minutes
	Title          string `gorm:"not null" json:"title"`
	Description    string `json:"description"`
	RecipeID       *uint  `json:"recipe_id,omitempty"` // if task_type is meal (deprecated, use Recipes relation)
	ZoneID         *uint  `json:"zone_id,omitempty"`   // if task_type is cleaning (deprecated, use Zones relation)
	// M2: категория и назначение
	TaskCategoryID   *uint `gorm:"index" json:"task_category_id,omitempty"`
	AssignedToUserID *uint `gorm:"index" json:"assigned_to_user_id,omitempty"`
	Completed        bool  `gorm:"default:false" json:"completed"`
	// Когда помощник отметил задачу выполненной (nil — не выполнена)
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Важность задачи: high | mid | normal; пусто — берётся из категории
	Priority string `json:"priority,omitempty"`
	// Задача создана из повторяющейся (RecurringTask) — nil для разовых
	RecurringTaskID *uint `gorm:"index" json:"recurring_task_id,omitempty"`
	// Задача собрана из регулярной задачи «Ритма дома» (Routine) — nil для разовых.
	RoutineID *uint `gorm:"index" json:"routine_id,omitempty"`
	// «Что входит»: пункты на момент сборки дня и отметки — JSON [{"text":"Плита","done":false}].
	Checklist string `gorm:"type:text" json:"checklist,omitempty"`
	// Сколько порций готовить в этот приём пищи; 0 — по умолчанию (DefaultMealServings).
	Servings  int       `gorm:"default:0" json:"servings"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Только в ответе (в базе нет): обычное число порций приёма пищи и замены блюд на этот день.
	// Заполняет FillMealInfo.
	UsualServings int            `gorm:"-" json:"usual_servings,omitempty"`
	Dishes        []TaskDishView `gorm:"-" json:"dishes,omitempty"`
	Swaps         []MealSwapView `gorm:"-" json:"swaps,omitempty"`

	// Relations
	Recipe       *Recipe        `gorm:"foreignKey:RecipeID" json:"recipe,omitempty"` // deprecated, use Recipes
	Recipes      []Recipe       `gorm:"many2many:meal_recipes;" json:"recipes,omitempty"`
	Zone         *CleaningZone  `gorm:"foreignKey:ZoneID" json:"zone,omitempty"` // deprecated, use Zones
	Zones        []CleaningZone `gorm:"many2many:task_zones;" json:"zones,omitempty"`
	TaskCategory *TaskCategory  `gorm:"foreignKey:TaskCategoryID" json:"task_category,omitempty"`
}

// ShoppingListItem represents items needed for shopping.
// Пункт либо «свободный» (старый плоский список, ListID = nil), либо входит в ShoppingList.
type ShoppingListItem struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	OrganizationID uint       `gorm:"index;not null;default:1" json:"organization_id"`
	ListID         *uint      `gorm:"index" json:"list_id,omitempty"`
	Item           string     `gorm:"not null" json:"item"`
	Quantity       string     `json:"quantity"`
	Category       string     `json:"category"` // produce, dairy, meat, etc
	Purchased      bool       `gorm:"default:false" json:"purchased"`
	PurchasedAt    *time.Time `json:"purchased_at,omitempty"`
	AddedBy        string     `json:"added_by"` // admin or helper
	AddedByUserID  *uint      `json:"added_by_user_id,omitempty"`
	Position       int        `gorm:"default:0" json:"position"`
	// Пункт собран из меню: сколько нужно по рецептам, сколько берём (помощник может уменьшить
	// или взять с запасом), единица («г», «мл», «шт»…) и для каких блюд — JSON []menu.Source.
	FromMenu   bool      `gorm:"default:false" json:"from_menu"`
	NeedAmount float64   `gorm:"default:0" json:"need_amount"`
	BuyAmount  float64   `gorm:"default:0" json:"buy_amount"`
	Unit       string    `gorm:"type:varchar(16)" json:"unit,omitempty"`
	Sources    string    `gorm:"type:text" json:"sources,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// DefaultMealServings — порций в приёме пищи, пока у задачи не задано своё число.
const DefaultMealServings = 2

// MealSwap — блюдо приёма пищи заменили на этот день. Одна строка на заменённое блюдо задачи:
// повторная замена меняет CurrentRecipeID, OriginalRecipeID остаётся первым — «Вернуть» возвращает его.
type MealSwap struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	OrganizationID   uint      `gorm:"index;not null" json:"organization_id"`
	TaskID           uint      `gorm:"index;not null" json:"task_id"`
	OriginalRecipeID uint      `gorm:"not null" json:"original_recipe_id"`
	CurrentRecipeID  uint      `gorm:"not null" json:"current_recipe_id"`
	CreatedByUserID  *uint     `json:"created_by_user_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// MealSwapView — замена в ответе задачи.
type MealSwapView struct {
	OriginalRecipeID uint   `json:"original_recipe_id"`
	OriginalName     string `json:"original_name"`
	CurrentRecipeID  uint   `json:"current_recipe_id"`
}

// MaxMealServings — больше порций на один приём пищи не ставим.
const MaxMealServings = 20

// FillMealInfo дописывает задачам еды обычное число порций и замены блюд (для ответа API).
func FillMealInfo(db *gorm.DB, tasks []ScheduleTask) {
	ids := []uint{}
	idx := map[uint]int{}
	for i := range tasks {
		if tasks[i].TaskType != "meal" {
			continue
		}
		tasks[i].UsualServings = DefaultMealServings
		ids = append(ids, tasks[i].ID)
		idx[tasks[i].ID] = i
	}
	if len(ids) == 0 {
		return
	}
	fillRoutineDishes(db, tasks, ids, idx)
	var swaps []MealSwap
	if db.Where("task_id IN ?", ids).Order("id").Find(&swaps).Error != nil || len(swaps) == 0 {
		return
	}
	rids := []uint{}
	for _, s := range swaps {
		rids = append(rids, s.OriginalRecipeID)
	}
	var recipes []Recipe
	db.Select("id", "name").Where("id IN ?", rids).Find(&recipes)
	names := map[uint]string{}
	for _, r := range recipes {
		names[r.ID] = r.Name
	}
	for _, s := range swaps {
		i := idx[s.TaskID]
		tasks[i].Swaps = append(tasks[i].Swaps, MealSwapView{OriginalRecipeID: s.OriginalRecipeID,
			OriginalName: names[s.OriginalRecipeID], CurrentRecipeID: s.CurrentRecipeID})
	}
}

// Статусы списка покупок.
const (
	ShoppingListDraft  = "draft"  // помощник собирает, нанимателю не виден
	ShoppingListOpen   = "open"   // во входящих у нанимателя
	ShoppingListClosed = "closed" // съездили в магазин, список закрыт
)

// ShoppingList — чек-лист покупок с датой, автором и статусом (раздел «Входящие»).
type ShoppingList struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	OrganizationID  uint       `gorm:"index;not null" json:"organization_id"`
	ListDate        string     `gorm:"type:varchar(10);not null" json:"list_date"` // YYYY-MM-DD
	Status          string     `gorm:"type:varchar(16);index;not null;default:'draft'" json:"status"`
	CreatedByUserID uint       `gorm:"index" json:"created_by_user_id"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
	ViewedAt        *time.Time `json:"viewed_at,omitempty"` // когда наниматель впервые открыл
	ClosedAt        *time.Time `json:"closed_at,omitempty"`
	ClosedByUserID  *uint      `json:"closed_by_user_id,omitempty"`
	ParentListID    *uint      `gorm:"index" json:"parent_list_id,omitempty"` // список, из которого перенесены пункты
	// Список «под меню»: какие дни покрывает (YYYY-MM-DD, включительно) и что из рецептов
	// обычно есть дома (соль, мука…) — показываем «проверьте», а не пунктами.
	MenuFrom   string    `gorm:"type:varchar(10)" json:"menu_from,omitempty"`
	MenuTo     string    `gorm:"type:varchar(10)" json:"menu_to,omitempty"`
	MenuPantry string    `gorm:"type:text" json:"menu_pantry,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	Items []ShoppingListItem `gorm:"foreignKey:ListID" json:"items,omitempty"`
}

// Settings represents per-organisation system settings
type Settings struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"uniqueIndex:idx_org_setting_key;index;not null;default:1" json:"organization_id"`
	Key            string    `gorm:"uniqueIndex:idx_org_setting_key;not null" json:"key"`
	Value          string    `json:"value"`
	Description    string    `json:"description"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// PublicHoliday — государственный праздник страны (общий справочник, без organization_id).
// Загружается из публичного календаря Google, запасной источник — Nager.Date (см. internal/calendar).
type PublicHoliday struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	CountryCode string    `gorm:"size:2;not null;uniqueIndex:idx_public_holiday" json:"country_code"`
	Date        time.Time `gorm:"type:date;not null;uniqueIndex:idx_public_holiday;index" json:"date"`
	Name        string    `gorm:"not null;uniqueIndex:idx_public_holiday" json:"name"`
	LocalName   string    `json:"local_name,omitempty"`
	Source      string    `json:"source"` // google | nager
	FetchedAt   time.Time `json:"fetched_at"`
}

// HolidaySync — когда и откуда последний раз загружали праздники страны.
type HolidaySync struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	CountryCode string    `gorm:"size:2;uniqueIndex;not null" json:"country_code"`
	Source      string    `json:"source"`
	Status      string    `json:"status"` // ok | error
	Error       string    `json:"error,omitempty"`
	Count       int       `json:"count"`
	FetchedAt   time.Time `json:"fetched_at"`
}

// Виды особых дней сотрудника.
const (
	DayOverrideVacation = "vacation" // отпуск (с–по), вычитается из дней отпуска
	DayOverrideDayOff   = "day_off"  // отгул / выходной по договорённости
	DayOverrideWork     = "work"     // рабочий день вместо выходного или праздника
)

// StaffDayOverride — особый день сотрудника, который отмечает наниматель.
type StaffDayOverride struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null" json:"organization_id"`
	UserID         uint      `gorm:"index;not null" json:"user_id"`
	Kind           string    `gorm:"not null" json:"kind"`
	StartDate      time.Time `gorm:"type:date;not null;index" json:"start_date"`
	EndDate        time.Time `gorm:"type:date;not null;index" json:"end_date"`
	Note           string    `json:"note,omitempty"`
	CreatedBy      uint      `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// RecipeComment represents a comment on a recipe
type RecipeComment struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OrganizationID uint      `gorm:"index;not null;default:1" json:"organization_id"`
	RecipeID       uint      `gorm:"not null" json:"recipe_id"`
	Comment        string    `gorm:"type:text" json:"comment"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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
	ID               uint   `gorm:"primaryKey" json:"id"`
	OrganizationID   uint   `gorm:"index;not null" json:"organization_id"`
	Title            string `gorm:"not null" json:"title"`
	Description      string `json:"description"`
	Time             string `json:"time"`     // HH:MM или пусто — «когда удобно»
	EndTime          string `json:"end_time"` // HH:MM — конец промежутка
	Priority         string `json:"priority,omitempty"`
	TaskCategoryID   *uint  `gorm:"index" json:"task_category_id,omitempty"`
	AssignedToUserID *uint  `gorm:"index" json:"assigned_to_user_id,omitempty"`
	// Дни недели через запятую, ISO: 1 = Пн … 7 = Вс
	Weekdays  string    `gorm:"not null" json:"weekdays"`
	StartDate time.Time `gorm:"not null" json:"start_date"`
	Active    bool      `gorm:"default:true" json:"active"`
	// Перенесено в регулярную задачу «Ритма дома»: старый генератор такую запись больше не ставит в день.
	MigratedRoutineID *uint     `gorm:"index" json:"migrated_routine_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`

	TaskCategory *TaskCategory `gorm:"foreignKey:TaskCategoryID" json:"task_category,omitempty"`
}

// RecurringTaskSkip — день, в который повторяющуюся задачу удалили вручную:
// больше не создаём её в этот день.
type RecurringTaskSkip struct {
	ID              uint      `gorm:"primaryKey"`
	RecurringTaskID uint      `gorm:"uniqueIndex:idx_recurring_skip"`
	Date            time.Time `gorm:"uniqueIndex:idx_recurring_skip"`
}
