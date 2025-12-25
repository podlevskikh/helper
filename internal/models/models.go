package models

import (
	"time"
)

// Recipe represents a meal recipe
type Recipe struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"not null" json:"name"`
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
	ID           uint      `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"not null" json:"name"` // breakfast, lunch, dinner, snack1, babyfood, etc
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
	Name            string    `gorm:"not null" json:"name"` // bedroom, kitchen, bathroom, etc
	Description     string    `json:"description"`
	FrequencyPerWeek int      `gorm:"not null" json:"frequency_per_week"` // how many times per week
	Priority        string    `gorm:"default:'medium'" json:"priority"` // high, medium, low
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ChildcareSchedule represents daily childcare times (manually added each day)
type ChildcareSchedule struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Date        time.Time `gorm:"not null;index" json:"date"` // date for this schedule
	StartTime   string    `gorm:"not null" json:"start_time"` // HH:MM format
	EndTime     string    `gorm:"not null" json:"end_time"`   // HH:MM format
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DailySchedule represents the generated daily schedule for the helper
type DailySchedule struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Date      time.Time `gorm:"not null;index" json:"date"`
	Generated bool      `gorm:"default:false" json:"generated"` // whether schedule was auto-generated
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	
	// Relations
	Tasks []ScheduleTask `gorm:"foreignKey:ScheduleID" json:"tasks"`
}

// ScheduleTask represents a single task in the daily schedule
type ScheduleTask struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ScheduleID  uint      `gorm:"not null;index" json:"schedule_id"`
	TaskType    string    `gorm:"not null" json:"task_type"` // meal, cleaning, childcare
	Time        string    `json:"time"` // HH:MM format (can be empty for flexible tasks like cleaning)
	EndTime     string    `json:"end_time"` // HH:MM format (for childcare tasks with time range)
	Duration    int       `json:"duration"` // in minutes
	Title       string    `gorm:"not null" json:"title"`
	Description string    `json:"description"`
	RecipeID    *uint     `json:"recipe_id,omitempty"` // if task_type is meal (deprecated, use Recipes relation)
	ZoneID      *uint     `json:"zone_id,omitempty"`   // if task_type is cleaning (deprecated, use Zones relation)
	Completed   bool      `gorm:"default:false" json:"completed"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Relations
	Recipe  *Recipe       `gorm:"foreignKey:RecipeID" json:"recipe,omitempty"` // deprecated, use Recipes
	Recipes []Recipe      `gorm:"many2many:meal_recipes;" json:"recipes,omitempty"` // multiple recipes for a meal
	Zone    *CleaningZone `gorm:"foreignKey:ZoneID" json:"zone,omitempty"` // deprecated, use Zones
	Zones   []CleaningZone `gorm:"many2many:task_zones;" json:"zones,omitempty"` // multiple zones for a cleaning task
}

// ShoppingListItem represents items needed for shopping
type ShoppingListItem struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Item        string    `gorm:"not null" json:"item"`
	Quantity    string    `json:"quantity"`
	Category    string    `json:"category"` // produce, dairy, meat, etc
	Purchased   bool      `gorm:"default:false" json:"purchased"`
	AddedBy     string    `json:"added_by"` // admin or helper
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Settings represents global system settings
type Settings struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Key         string    `gorm:"uniqueIndex;not null" json:"key"`
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
	ID        uint      `gorm:"primaryKey" json:"id"`
	RecipeID  uint      `gorm:"not null" json:"recipe_id"`
	Comment   string    `gorm:"type:text" json:"comment"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

