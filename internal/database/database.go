package database

import (
	"fmt"
	"log"
	"os"

	"podlevskikh/awesomeProject/internal/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// Initialize sets up the database connection and runs migrations
// It accepts a DATABASE_URL connection string for PostgreSQL
func Initialize(databaseURL string) error {
	var err error

	// If databaseURL is empty, try to get it from environment
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}

	// If still empty, return error
	if databaseURL == "" {
		log.Println("ERROR: DATABASE_URL is not set")
		log.Println("Please set DATABASE_URL environment variable with PostgreSQL connection string")
		log.Println("Example: postgres://user:password@host:port/database?sslmode=disable")
		return fmt.Errorf("DATABASE_URL is required")
	}

	log.Println("Attempting to connect to PostgreSQL database...")

	DB, err = gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		log.Printf("Failed to connect to database. Error: %v", err)
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	log.Println("✅ Database connection established successfully")

	// Редизайн v2: колонка важности категорий появляется впервые — после миграции
	// системной «Уход за ребёнком» ставим высокую важность (один раз, выбор владельца не трогаем).
	firstPriorityMigration := DB.Migrator().HasTable(&models.TaskCategory{}) &&
		!DB.Migrator().HasColumn(&models.TaskCategory{}, "priority")

	// Run migrations
	err = DB.AutoMigrate(
		// Идентичность и мультиарендность (M1)
		&models.User{},
		&models.Organization{},
		&models.Membership{},
		&models.Invite{},
		&models.RefreshToken{},
		&models.PasswordReset{},
		// Домен
		&models.Recipe{},
		&models.MealTime{},
		&models.CleaningZone{},
		&models.ChildcareSchedule{},
		&models.DailySchedule{},
		&models.TaskCategory{}, // M2: до ScheduleTask (FK)
		&models.ScheduleTask{},
		&models.RecurringTask{},
		&models.RecurringTaskSkip{},
		&models.ShoppingList{}, // до ShoppingListItem (list_id)
		&models.ShoppingListItem{},
		&models.Settings{},
		&models.PublicHoliday{},
		&models.HolidaySync{},
		&models.StaffDayOverride{},
		&models.RecipeComment{},
		// Пуш-уведомления
		&models.PushDevice{},
		&models.NotificationPref{},
		&models.Notification{},
		&models.PushTicket{},
	)

	if err != nil {
		return err
	}

	log.Println("Database migrations completed")

	if firstPriorityMigration {
		res := DB.Exec("UPDATE task_categories SET priority = 'high' WHERE is_default = true AND name = ?", "Уход за ребёнком")
		log.Printf("redesign-v2: важность «Уход за ребёнком» = high, категорий: %d", res.RowsAffected)
	}

	// Run data migrations
	runDataMigrations()

	// Initialize default settings
	initializeDefaultSettings()

	// M2: seed дефолтных категорий задач для всех организаций
	seedDefaultCategories()

	return nil
}

// runDataMigrations runs data migrations after schema migrations
func runDataMigrations() {
	// Set is_active to true for all existing recipes that don't have it set
	DB.Exec("UPDATE recipes SET is_active = true WHERE is_active IS NULL")

	// Settings: drop old single-column unique index (replaced by composite org+key)
	DB.Exec("DROP INDEX IF EXISTS uni_settings_key")

	// M1: seed organisation + owner user if none exist, then backfill organization_id
	seedOrgAndOwner()

	// Владелец дома — у старых домов owner_user_id мог остаться пустым: берём его из членства owner.
	res := DB.Exec(`UPDATE organizations o SET owner_user_id = m.user_id
		FROM (SELECT DISTINCT ON (organization_id) organization_id, user_id FROM memberships
		      WHERE role = 'owner' ORDER BY organization_id, id) m
		WHERE m.organization_id = o.id AND COALESCE(o.owner_user_id, 0) = 0`)
	if res.Error == nil && res.RowsAffected > 0 {
		log.Printf("organizations: заполнен владелец у %d домов", res.RowsAffected)
	}
}

// seedOrgAndOwner создаёт seed-организацию + владельца и заполняет organization_id
// во всех доменных таблицах. Идемпотентно: пропускает, если организации уже есть.
func seedOrgAndOwner() {
	var count int64
	DB.Model(&models.Organization{}).Count(&count)
	if count > 0 {
		log.Println("Seed: организации уже существуют, пропускаем")
		return
	}

	orgName := os.Getenv("SEED_ORG_NAME")
	if orgName == "" {
		orgName = "My Home"
	}
	ownerEmail := os.Getenv("SEED_OWNER_EMAIL")
	if ownerEmail == "" {
		ownerEmail = "owner@example.com"
	}
	ownerPassword := os.Getenv("SEED_OWNER_PASSWORD")
	if ownerPassword == "" {
		ownerPassword = "changeme123"
	}

	// 1. Создаём организацию
	org := models.Organization{Name: orgName}
	if err := DB.Create(&org).Error; err != nil {
		log.Printf("Seed: не удалось создать организацию: %v", err)
		return
	}

	// 2. Хэшируем пароль и создаём владельца
	hash, err := bcrypt.GenerateFromPassword([]byte(ownerPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Seed: не удалось захэшировать пароль: %v", err)
		return
	}
	user := models.User{
		Email:        ownerEmail,
		PasswordHash: string(hash),
		Name:         "Owner",
		Locale:       "en",
	}
	if err := DB.Create(&user).Error; err != nil {
		log.Printf("Seed: не удалось создать пользователя-владельца: %v", err)
		return
	}
	DB.Model(&org).Update("owner_user_id", user.ID)

	// 3. Создаём membership owner
	membership := models.Membership{
		UserID:         user.ID,
		OrganizationID: org.ID,
		Role:           models.RoleOwner,
		Status:         models.MembershipActive,
	}
	if err := DB.Create(&membership).Error; err != nil {
		log.Printf("Seed: не удалось создать membership: %v", err)
		return
	}

	log.Printf("✅ Seed: создана орг '%s' (id=%d), владелец '%s' (id=%d)", org.Name, org.ID, user.Email, user.ID)

	// 4. Backfill organization_id во всех доменных таблицах (0 = не задан Go-нулём)
	domainTables := []string{
		"recipes", "meal_times", "cleaning_zones", "childcare_schedules",
		"daily_schedules", "schedule_tasks", "shopping_list_items",
		"recipe_comments", "settings",
	}
	for _, table := range domainTables {
		res := DB.Exec(
			fmt.Sprintf("UPDATE %s SET organization_id = ? WHERE organization_id = 0 OR organization_id IS NULL", table),
			org.ID,
		)
		if res.Error != nil {
			log.Printf("Seed: backfill %s: %v", table, res.Error)
		} else if res.RowsAffected > 0 {
			log.Printf("Seed: backfill %s: %d строк", table, res.RowsAffected)
		}
	}
}

// seedDefaultCategories создаёт системные категории задач для каждой организации
// и делает backfill task_category_id для существующих задач по их task_type.
func seedDefaultCategories() {
	var orgs []models.Organization
	if err := DB.Find(&orgs).Error; err != nil {
		log.Printf("seedDefaultCategories: не удалось загрузить организации: %v", err)
		return
	}
	for _, org := range orgs {
		EnsureDefaultCategories(DB, org.ID)
	}
}

// EnsureDefaultCategories создаёт системные категории (Еда, Уборка, Уход за ребёнком, Другое)
// одного дома, если их ещё нет, и проставляет их задачам без категории.
// Вызывается при старте для всех домов и при регистрации — для нового дома.
func EnsureDefaultCategories(db *gorm.DB, orgID uint) {
	type defaultCat struct {
		Name      string
		Icon      string
		Color     string
		TaskType  string // соответствующий legacy task_type
		SortOrder int
		Priority  string
	}
	defaults := []defaultCat{
		{Name: "Еда", Icon: "🍽", Color: "#F59E0B", TaskType: "meal", SortOrder: 0},
		{Name: "Уборка", Icon: "🧹", Color: "#10B981", TaskType: "cleaning", SortOrder: 1},
		{Name: "Уход за ребёнком", Icon: "👶", Color: "#6366F1", TaskType: "childcare", SortOrder: 2, Priority: "high"},
		{Name: "Другое", Icon: "✏️", Color: "#6B7280", TaskType: "custom", SortOrder: 3},
	}

	org := models.Organization{ID: orgID}
	{
		for _, d := range defaults {
			// Ищем системную категорию по ключу; у старых данных ключа нет —
			// узнаём её по названию или (если переименована) по месту в списке.
			var cat models.TaskCategory
			err := db.Where("organization_id = ? AND system_key = ?", org.ID, d.TaskType).First(&cat).Error
			if err != nil {
				err = db.Where("organization_id = ? AND is_default = ? AND (system_key IS NULL OR system_key = '') AND name = ?", org.ID, true, d.Name).
					First(&cat).Error
				if err != nil {
					err = db.Where("organization_id = ? AND is_default = ? AND (system_key IS NULL OR system_key = '') AND sort_order = ?", org.ID, true, d.SortOrder).
						First(&cat).Error
				}
				if err == nil {
					db.Model(&cat).Update("system_key", d.TaskType)
				}
			}
			if err == nil {
				// уже существует — backfill задач
				db.Exec(
					"UPDATE schedule_tasks SET task_category_id = ? WHERE organization_id = ? AND task_type = ? AND task_category_id IS NULL",
					cat.ID, org.ID, d.TaskType,
				)
				continue
			}

			// создаём категорию
			cat = models.TaskCategory{
				OrganizationID: org.ID,
				Name:           d.Name,
				Icon:           d.Icon,
				Color:          d.Color,
				IsDefault:      true,
				SystemKey:      d.TaskType,
				SortOrder:      d.SortOrder,
				Priority:       map[bool]string{true: d.Priority, false: "normal"}[d.Priority != ""],
			}
			if err := db.Create(&cat).Error; err != nil {
				log.Printf("seedDefaultCategories: орг %d, категория '%s': %v", org.ID, d.Name, err)
				continue
			}
			// backfill
			res := db.Exec(
				"UPDATE schedule_tasks SET task_category_id = ? WHERE organization_id = ? AND task_type = ? AND task_category_id IS NULL",
				cat.ID, org.ID, d.TaskType,
			)
			log.Printf("seedDefaultCategories: орг %d, '%s' (id=%d), backfill задач: %d", org.ID, d.Name, cat.ID, res.RowsAffected)
		}
	}
}

// initializeDefaultSettings создаёт дефолтные настройки для seed-организации (id=1)
func initializeDefaultSettings() {
	const seedOrgID = 1
	defaultSettings := []models.Settings{
		{OrganizationID: seedOrgID, Key: "schedule_days_ahead", Value: "7", Description: "Number of days to generate schedule ahead"},
		{OrganizationID: seedOrgID, Key: "auto_generate_schedule", Value: "true", Description: "Automatically generate schedule daily"},
	}

	for _, setting := range defaultSettings {
		var existing models.Settings
		result := DB.Where("organization_id = ? AND key = ?", seedOrgID, setting.Key).First(&existing)
		if result.Error == gorm.ErrRecordNotFound {
			DB.Create(&setting)
			log.Printf("Created default setting: %s = %s", setting.Key, setting.Value)
		}
	}
}

// GetDB returns the database instance
func GetDB() *gorm.DB {
	return DB
}
