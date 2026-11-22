package database

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

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
		Logger: dbLogger(),
	})

	if err != nil {
		log.Printf("Failed to connect to database. Error: %v", err)
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	log.Println("✅ Database connection established successfully")
	configurePool(DB)

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
		&models.EmailChange{},
		// Домен
		&models.Recipe{},
		&models.MealTime{},
		&models.CleaningZone{},
		&models.ChildcareSchedule{},
		&models.DailySchedule{},
		&models.TaskCategory{}, // M2: до ScheduleTask (FK)
		&models.ScheduleTask{},
		&models.MealSwap{},
		&models.RecurringTask{},
		&models.RecurringTaskSkip{},
		&models.ShoppingList{}, // до ShoppingListItem (list_id)
		&models.ShoppingListItem{},
		&models.Settings{},
		&models.PublicHoliday{},
		&models.HolidaySync{},
		&models.StaffDayOverride{},
		&models.RecipeComment{},
		// «Ритм дома»: регулярные задачи
		&models.DishType{},
		&models.Routine{},
		&models.RoutineDish{},
		&models.RoutineChecklistItem{},
		&models.RoutineSkip{},
		&models.RoutineDayDecision{},
		&models.TaskDish{},
		&models.RecipeImport{}, // загрузка рецептов списком
		// Коннектор для Claude (OAuth)
		&models.OAuthClient{},
		&models.OAuthRequest{},
		&models.OAuthCode{},
		&models.OAuthGrant{},
		&models.OAuthToken{},
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

	// Сид и владельцы домов — до версионированных шагов (они проверяют, есть ли дома).
	runDataMigrations()

	// Версионированные шаги, меняющие данные: один раз, в транзакции; ошибка — сервер не стартует.
	if err := runMigrations(DB, migrationsState{categoryPriorityAppeared: firstPriorityMigration}); err != nil {
		return err
	}

	// Initialize default settings
	initializeDefaultSettings()

	// M2: seed дефолтных категорий задач для всех организаций
	seedDefaultCategories()

	return nil
}

// configurePool: по умолчанию database/sql держит только 2 свободных соединения — при всплеске
// запросов каждый открывает новое соединение к базе, а число открытых ничем не ограничено
// (у Postgres на Railway лимит около 100). DB_MAX_CONNS — потолок открытых соединений.
func configurePool(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	maxOpen := 25
	if v, err := strconv.Atoi(os.Getenv("DB_MAX_CONNS")); err == nil && v > 0 {
		maxOpen = v
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(min(10, maxOpen))
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
}

// runDataMigrations runs data migrations after schema migrations
func runDataMigrations() {
	// M1: seed organisation + owner user if none exist, then backfill organization_id
	seedOrgAndOwner()
	warnDefaultOwnerPassword()

	// Владелец дома — у старых домов owner_user_id мог остаться пустым: берём его из членства owner.
	res := DB.Exec(`UPDATE organizations o SET owner_user_id = m.user_id
		FROM (SELECT DISTINCT ON (organization_id) organization_id, user_id FROM memberships
		      WHERE role = 'owner' ORDER BY organization_id, id) m
		WHERE m.organization_id = o.id AND COALESCE(o.owner_user_id, 0) = 0`)
	if res.Error == nil && res.RowsAffected > 0 {
		log.Printf("organizations: заполнен владелец у %d домов", res.RowsAffected)
	}

	repairLeakedSchedulerTasks()
}

// repairLeakedSchedulerTasks: планировщик (он собирает дни только дома 1) раньше искал день
// без учёта дома и мог положить блюда, уборку и ребёнка дома 1 в день другого дома —
// чужие рецепты и зоны были видны в другой семье. Возвращаем такие задачи в день дома 1
// (или удаляем, если у дома 1 этот день уже собран). Идемпотентно.
func repairLeakedSchedulerTasks() {
	const leaked = "organization_id = 1 AND task_type IN ('meal','cleaning','childcare')"
	type row struct {
		ScheduleID uint
		Date       time.Time
	}
	var rows []row
	DB.Raw(`SELECT DISTINCT ds.id AS schedule_id, ds.date FROM daily_schedules ds
		JOIN schedule_tasks st ON st.schedule_id = ds.id
		WHERE ds.organization_id <> 1 AND st.organization_id = 1
		  AND st.task_type IN ('meal','cleaning','childcare')`).Scan(&rows)
	for _, r := range rows {
		err := DB.Transaction(func(tx *gorm.DB) error {
			var own models.DailySchedule
			found := tx.Where("organization_id = 1 AND date = ?", r.Date).First(&own).Error == nil
			if found && own.Generated {
				ids := tx.Model(&models.ScheduleTask{}).Select("id").Where("schedule_id = ? AND "+leaked, r.ScheduleID)
				if err := tx.Exec("DELETE FROM meal_recipes WHERE schedule_task_id IN (?)", ids).Error; err != nil {
					return err
				}
				if err := tx.Exec("DELETE FROM task_zones WHERE schedule_task_id IN (?)", ids).Error; err != nil {
					return err
				}
				if err := tx.Where("schedule_id = ? AND "+leaked, r.ScheduleID).Delete(&models.ScheduleTask{}).Error; err != nil {
					return err
				}
			} else {
				if !found {
					own = models.DailySchedule{OrganizationID: 1, Date: r.Date, Generated: true}
					if err := tx.Create(&own).Error; err != nil {
						return err
					}
				} else if err := tx.Model(&own).Update("generated", true).Error; err != nil {
					return err
				}
				if err := tx.Model(&models.ScheduleTask{}).Where("schedule_id = ? AND "+leaked, r.ScheduleID).
					Update("schedule_id", own.ID).Error; err != nil {
					return err
				}
			}
			return tx.Model(&models.DailySchedule{}).Where("id = ?", r.ScheduleID).Update("generated", false).Error
		})
		if err != nil {
			log.Printf("repairLeakedSchedulerTasks: день %d (%s): %v", r.ScheduleID, r.Date.Format("2006-01-02"), err)
		} else {
			log.Printf("repairLeakedSchedulerTasks: задачи дома 1 убраны из дня %d (%s)", r.ScheduleID, r.Date.Format("2006-01-02"))
		}
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
	// Пароль по умолчанию из публичного кода = чужой вход в дом. Без SEED_OWNER_PASSWORD
	// владельцу ставится случайный пароль (в лог не пишется): войти — через «Забыли пароль?».
	ownerPassword := os.Getenv("SEED_OWNER_PASSWORD")
	if ownerPassword == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			log.Printf("Seed: не удалось создать пароль: %v", err)
			return
		}
		ownerPassword = hex.EncodeToString(b)
		log.Printf("Seed: SEED_OWNER_PASSWORD не задан — у %s случайный пароль, войти через «Забыли пароль?»", ownerEmail)
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

// dbLogger — SQL в лог без значений параметров (там почты, хэши, детские заметки) и только
// медленные запросы и ошибки. DB_LOG=info — подробный лог для локальной отладки.
func dbLogger() logger.Interface {
	level := logger.Warn
	if os.Getenv("DB_LOG") == "info" {
		level = logger.Info
	}
	return logger.New(log.New(os.Stdout, "", log.LstdFlags), logger.Config{
		SlowThreshold:             500 * time.Millisecond,
		LogLevel:                  level,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
	})
}

// warnDefaultOwnerPassword: громкое предупреждение, если у owner@example.com всё ещё пароль из старого кода.
func warnDefaultOwnerPassword() {
	var u models.User
	if DB.Where("email = ?", "owner@example.com").First(&u).Error != nil {
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("changeme123")) == nil {
		log.Println("⚠️  SECURITY: у owner@example.com пароль по умолчанию из публичного кода — смените его в профиле!")
	}
}

// EnsureDefaultMealTimes — завтрак, обед и ужин у нового дома: без приёмов пищи рецепты не попадают в меню.
// Наниматель потом меняет время, выключает или добавляет свои («Регулярное → Еда»).
func EnsureDefaultMealTimes(db *gorm.DB, orgID uint) error {
	var n int64
	db.Model(&models.MealTime{}).Where("organization_id = ?", orgID).Count(&n)
	if n > 0 {
		return nil
	}
	for _, m := range []models.MealTime{
		{OrganizationID: orgID, Name: "breakfast", DefaultTime: "08:30", FamilyMember: "all", Active: true},
		{OrganizationID: orgID, Name: "lunch", DefaultTime: "13:00", FamilyMember: "all", Active: true},
		{OrganizationID: orgID, Name: "dinner", DefaultTime: "18:30", FamilyMember: "all", Active: true},
	} {
		m := m
		if err := db.Create(&m).Error; err != nil {
			return err
		}
	}
	return nil
}
