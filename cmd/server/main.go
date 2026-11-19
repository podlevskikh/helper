package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/handlers"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/notify"
	"podlevskikh/awesomeProject/internal/scheduler"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Get database URL from environment
	databaseURL := os.Getenv("DATABASE_URL")

	// Debug logging for Railway
	if databaseURL == "" {
		log.Println("WARNING: DATABASE_URL environment variable is not set")
		log.Println("Available environment variables:")
		for _, env := range os.Environ() {
			log.Println("  ", env)
		}
	} else {
		// Log that we have DATABASE_URL (but don't log the actual value for security)
		log.Println("DATABASE_URL is set (length:", len(databaseURL), ")")
	}

	// Initialize database
	if err := database.Initialize(databaseURL); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	db := database.GetDB()
	database.EnsureJWTSecret(db)
	database.EnsureSealingKeys(db)
	database.LoadSealedSecrets(database.SealedFile) // пароль почты и т.п. — см. scripts/seal-mail.sh

	// Initialize scheduler
	sched := scheduler.NewScheduler(db)

	// Праздники стран: загружаем при старте (если ещё не загружены) и обновляем раз в сутки
	calendar.StartRefresher(db)

	// Generate initial schedules for the next 7 days
	log.Println("Generating initial schedules...")
	if err := sched.GenerateScheduleForNextDays(7); err != nil {
		log.Printf("Warning: Failed to generate initial schedules: %v", err)
	}

	// Пуш-уведомления: очередь → Expo (PUSH_DRY_RUN=1 — только в лог)
	var pushSender notify.Sender = notify.NewExpoClientFromEnv()
	if os.Getenv("PUSH_DRY_RUN") == "1" {
		pushSender = notify.LogSender{Printf: log.Printf}
	}
	pushWorker := &notify.Worker{DB: db, Sender: pushSender, EnsureDay: handlers.EnsureDay(db)}
	go pushWorker.Run(context.Background())

	// Start daily schedule generation goroutine
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			log.Println("Running daily schedule generation...")
			if err := sched.GenerateScheduleForNextDays(7); err != nil {
				log.Printf("Error in daily schedule generation: %v", err)
			}
		}
	}()

	// Initialize Gin router
	router := gin.Default()

	// CORS — разрешаем web-origin Expo (WEB_ORIGIN env, через запятую; dev: http://localhost:8081).
	// Первый адрес используется в ссылках-приглашениях.
	var webOrigins []string
	for _, o := range strings.Split(os.Getenv("WEB_ORIGIN"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			webOrigins = append(webOrigins, o)
		}
	}
	if len(webOrigins) == 0 {
		webOrigins = []string{"http://localhost:8081"}
	}
	router.Use(cors.New(cors.Config{
		AllowOrigins:     webOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Org-Id"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	// Load HTML templates from both web and web2 directories
	router.LoadHTMLGlob("web*/templates/*")

	// Serve static files
	router.Static("/static", "./web/static")
	router.Static("/static2", "./web2/static")

	// Initialize handlers
	adminHandler := handlers.NewAdminHandler(db)
	helperHandler := handlers.NewHelperHandler(db)
	authHandler := handlers.NewAuthHandler(db)
	inviteHandler := handlers.NewInviteHandler(db)
	orgHandler := handlers.NewOrgHandler(db)
	staffCalHandler := handlers.NewStaffCalendarHandler(db)
	staffCalHandler.EnsureSchedule = func(date time.Time) {
		if err := sched.GenerateScheduleForDate(date); err != nil {
			log.Printf("Warning: schedule for %s: %v", date.Format("2006-01-02"), err)
		}
	}
	notificationsHandler := handlers.NewNotificationsHandler(db)

	// Auth routes
	authMw := middleware.Auth()
	orgMw := middleware.OrgContext(db)

	// Защита от перебора паролей: неудачные попытки с одного адреса.
	loginLimit := middleware.RateLimit(middleware.RateLimitOptions{Max: 10, Per: 15 * time.Minute, OnlyFailures: true})
	registerLimit := middleware.RateLimit(middleware.RateLimitOptions{Max: 10, Per: time.Hour})
	forgotLimit := middleware.RateLimit(middleware.RateLimitOptions{Max: 5, Per: 15 * time.Minute})

	authGroup := router.Group("/auth")
	{
		authGroup.POST("/register", registerLimit, authHandler.Register)
		authGroup.POST("/login", loginLimit, authHandler.Login)
		authGroup.POST("/refresh", authHandler.Refresh)
		authGroup.POST("/logout", authHandler.Logout)
		authGroup.GET("/me", authMw, authHandler.Me)
		authGroup.PATCH("/me", authMw, authHandler.UpdateMe)
		authGroup.DELETE("/me", authMw, loginLimit, authHandler.DeleteMe)
		authGroup.POST("/password", authMw, loginLimit, authHandler.ChangePassword)
		authGroup.POST("/password/forgot", forgotLimit, authHandler.ForgotPassword)
		authGroup.POST("/password/reset", loginLimit, authHandler.ResetPassword)
	}

	// Устройства для пушей (без контекста дома: токен — на пользователя)
	meGroup := router.Group("/me", authMw)
	{
		meGroup.POST("/devices", notificationsHandler.RegisterDevice)
		meGroup.DELETE("/devices/:token", notificationsHandler.UnregisterDevice)
	}

	// Открытый ключ для запечатанных секретов (scripts/seal-mail.sh)
	router.GET("/system/sealing-key", func(c *gin.Context) {
		c.JSON(200, gin.H{"public_key": database.SealingPublicKey()})
	})

	// Invite routes
	router.GET("/invites/:token", inviteHandler.GetInvite)
	router.POST("/invites/:token/accept", loginLimit, inviteHandler.AcceptInvite)

	// Org routes (auth + org context required)
	orgsGroup := router.Group("/orgs", authMw, orgMw)
	{
		orgsGroup.POST("/:orgId/invites", middleware.Require(middleware.CapManageTeam), inviteHandler.CreateInvite)
		orgsGroup.GET("/:orgId/members", middleware.Require(middleware.CapManageTeam), orgHandler.GetMembers)

		// Выходные и праздники сотрудников
		manageTeam := middleware.Require(middleware.CapManageTeam)
		orgsGroup.GET("/:orgId/calendar", staffCalHandler.GetCalendar)
		orgsGroup.GET("/:orgId/members/:userId/schedule-settings", manageTeam, staffCalHandler.GetSettings)
		orgsGroup.PATCH("/:orgId/members/:userId/schedule-settings", manageTeam, staffCalHandler.UpdateSettings)
		orgsGroup.GET("/:orgId/members/:userId/day-overrides", manageTeam, staffCalHandler.ListOverrides)
		orgsGroup.POST("/:orgId/members/:userId/day-overrides", manageTeam, staffCalHandler.CreateOverride)
		orgsGroup.DELETE("/:orgId/members/:userId/day-overrides/:id", manageTeam, staffCalHandler.DeleteOverride)

		// M2: task categories
		orgsGroup.GET("/:orgId/task-categories", orgHandler.GetTaskCategories)
		orgsGroup.POST("/:orgId/task-categories", middleware.Require(middleware.CapManageSettings), orgHandler.CreateTaskCategory)
		orgsGroup.PUT("/:orgId/task-categories/:id", middleware.Require(middleware.CapManageSettings), orgHandler.UpdateTaskCategory)
		orgsGroup.DELETE("/:orgId/task-categories/:id", middleware.Require(middleware.CapManageSettings), orgHandler.DeleteTaskCategory)

		// Redesign v2: настройки организации (лимит задач высокой важности)
		orgsGroup.GET("/:orgId/settings", orgHandler.GetOrgSettings)
		orgsGroup.PUT("/:orgId/settings", middleware.Require(middleware.CapManageSettings), orgHandler.UpdateOrgSettings)

		// Уведомления: свои настройки в этом доме
		orgsGroup.GET("/:orgId/notification-prefs", notificationsHandler.GetPrefs)
		orgsGroup.PUT("/:orgId/notification-prefs", notificationsHandler.UpdatePrefs)
		orgsGroup.POST("/:orgId/notification-prefs/test", notificationsHandler.SendTest)
	}

	// Веб-версия приложения (Expo) — по адресу /app. Старые интерфейсы /admin2 и /helper2
	// работали без входа и с новым API (нужна авторизация) уже не работают — ведём в новый.
	hasWebApp := handlers.RegisterWebApp(router, "./webapp")
	if hasWebApp {
		toApp := func(c *gin.Context) { c.Redirect(302, handlers.WebAppPrefix+"/") }
		router.GET("/admin2/", toApp)
		router.GET("/helper2/", toApp)
	} else {
		router.GET("/admin2/", func(c *gin.Context) {
			c.HTML(200, "admin2.html", nil)
		})
		router.GET("/helper2/", func(c *gin.Context) {
			c.HTML(200, "helper2.html", nil)
		})
	}

	// Admin routes
	admin := router.Group("/admin")
	{
		// Web pages
		admin.GET("/", func(c *gin.Context) {
			c.HTML(200, "admin.html", nil)
		})

		// API routes (auth + org required; RBAC через Require() на уровне хендлера)
		api := admin.Group("/api", authMw, orgMw)
		{
			// Recipes
			api.GET("/recipes", adminHandler.GetRecipes)
			api.GET("/recipes/:id", adminHandler.GetRecipe)
			api.POST("/recipes", middleware.Require(middleware.CapManageRecipes), adminHandler.CreateRecipe)
			api.PUT("/recipes/:id", middleware.Require(middleware.CapManageRecipes), adminHandler.UpdateRecipe)
			api.DELETE("/recipes/:id", middleware.Require(middleware.CapManageRecipes), adminHandler.DeleteRecipe)
			api.POST("/recipes/upload-image", adminHandler.UploadRecipeImage)

			// Recipe Comments
			api.GET("/recipes/:id/comments", adminHandler.GetRecipeComments)
			api.POST("/recipes/:id/comments", adminHandler.CreateRecipeComment)
			api.DELETE("/comments/:id", adminHandler.DeleteRecipeComment)

			// Meal times
			api.GET("/mealtimes", adminHandler.GetMealTimes)
			api.GET("/mealtimes/:id", adminHandler.GetMealTime)
			api.POST("/mealtimes", adminHandler.CreateMealTime)
			api.PUT("/mealtimes/:id", adminHandler.UpdateMealTime)
			api.DELETE("/mealtimes/:id", adminHandler.DeleteMealTime)

			// Cleaning zones
			api.GET("/zones", adminHandler.GetCleaningZones)
			api.GET("/zones/:id", adminHandler.GetCleaningZone)
			api.POST("/zones", middleware.Require(middleware.CapManageSchedule), adminHandler.CreateCleaningZone)
			api.PUT("/zones/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.UpdateCleaningZone)
			api.DELETE("/zones/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.DeleteCleaningZone)

			// Childcare schedules
			api.GET("/childcare", adminHandler.GetChildcareSchedules)
			api.GET("/childcare/:id", adminHandler.GetChildcareSchedule)
			api.POST("/childcare", adminHandler.CreateChildcareSchedule)
			api.PUT("/childcare/:id", adminHandler.UpdateChildcareSchedule)
			api.DELETE("/childcare/:id", adminHandler.DeleteChildcareSchedule)

			// Task management
			api.GET("/tasks/:id", adminHandler.GetTask)
			api.PUT("/tasks/:id", adminHandler.UpdateTask)
			api.POST("/tasks/:id/recipes", adminHandler.AddRecipeToTask)
			api.DELETE("/tasks/:id/recipes/:recipe_id", adminHandler.RemoveRecipeFromTask)
			api.POST("/tasks/:id/zones", adminHandler.AddZoneToTask)
			api.DELETE("/tasks/:id/zones/:zone_id", adminHandler.RemoveZoneFromTask)

			// Custom one-off tasks
			api.POST("/custom-tasks", adminHandler.CreateCustomTask)
			api.DELETE("/custom-tasks/:id", adminHandler.DeleteCustomTask)
			api.GET("/recurring-tasks", adminHandler.GetRecurringTasks)
			api.POST("/recurring-tasks", middleware.Require(middleware.CapManageSchedule), adminHandler.CreateRecurringTask)
			api.DELETE("/recurring-tasks/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.DeleteRecurringTask)

			// Schedule management
			// Пересборка дней — только для дома, который ведёт планировщик (организация 1),
			// и только для его данных: раньше любой вошедший стирал задачи всех домов.
			api.POST("/regenerate-schedule", middleware.Require(middleware.CapManageSchedule), func(c *gin.Context) {
				if middleware.MustMembership(c).OrganizationID != scheduler.OrgID {
					c.JSON(403, gin.H{"error": "schedule regeneration is not available for this home"})
					return
				}
				days := 7

				// Normalize to start of day so today's schedule is always included
				now := time.Now()
				today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
				endDate := today.AddDate(0, 0, days)

				log.Println("Clearing schedules from", today.Format("2006-01-02"), "to", endDate.Format("2006-01-02"))

				// Delete in correct order, but KEEP custom tasks (task_type = 'custom')
				// 1. Delete meal_recipes for non-custom tasks
				if err := db.Exec(`DELETE FROM meal_recipes WHERE schedule_task_id IN (
					SELECT st.id FROM schedule_tasks st
					JOIN daily_schedules ds ON ds.id = st.schedule_id
					WHERE ds.organization_id = 1 AND ds.date >= ? AND ds.date < ? AND st.task_type != 'custom'
				)`, today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete meal_recipes: %v", err)
				}

				// 2. Delete task_zones for non-custom tasks
				if err := db.Exec(`DELETE FROM task_zones WHERE schedule_task_id IN (
					SELECT st.id FROM schedule_tasks st
					JOIN daily_schedules ds ON ds.id = st.schedule_id
					WHERE ds.organization_id = 1 AND ds.date >= ? AND ds.date < ? AND st.task_type != 'custom'
				)`, today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete task_zones: %v", err)
				}

				// 3. Delete non-custom schedule_tasks
				if err := db.Exec(`DELETE FROM schedule_tasks
					WHERE task_type != 'custom'
					AND schedule_id IN (SELECT id FROM daily_schedules WHERE organization_id = 1 AND date >= ? AND date < ?)`,
					today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete schedule tasks: %v", err)
				}

				// 4. Delete daily_schedules that have no tasks left
				if err := db.Exec(`DELETE FROM daily_schedules
					WHERE organization_id = 1 AND date >= ? AND date < ?
					AND id NOT IN (SELECT DISTINCT schedule_id FROM schedule_tasks)`,
					today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete empty daily schedules: %v", err)
				}

				// 5. Reset generated=false on schedules that still exist (have custom tasks only)
				if err := db.Exec(`UPDATE daily_schedules SET generated = false
					WHERE organization_id = 1 AND date >= ? AND date < ?`, today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to reset generated flag: %v", err)
				}

				log.Println("Old schedules cleared, generating new schedules...")

				// Generate new schedules
				if err := sched.GenerateScheduleForNextDays(days); err != nil {
					c.JSON(500, gin.H{"error": err.Error()})
					return
				}
				c.JSON(200, gin.H{"message": "Schedule regenerated successfully"})
			})
		}
	}

	// Helper routes
	helper := router.Group("/helper")
	{
		// Web pages
		helper.GET("/", func(c *gin.Context) {
			c.HTML(200, "helper.html", nil)
		})

		// API routes (auth + org required)
		api := helper.Group("/api", authMw, orgMw)
		{
			// Schedule
			api.GET("/schedule/today", helperHandler.GetTodaySchedule)
			api.GET("/schedule/date/:date", helperHandler.GetScheduleByDate)
			api.GET("/schedule/upcoming", helperHandler.GetUpcomingSchedules)

			// Tasks
			api.POST("/tasks/:id/complete", helperHandler.CompleteTask)
			api.POST("/tasks/:id/uncomplete", helperHandler.UncompleteTask)

			// Shopping list
			api.GET("/shopping", helperHandler.GetShoppingList)
			api.POST("/shopping", helperHandler.AddShoppingListItem)
			api.POST("/shopping/:id/purchased", helperHandler.MarkItemPurchased)
			api.DELETE("/shopping/:id", helperHandler.DeleteShoppingListItem)

			// Списки покупок (раздел «Входящие»)
			viewShop := middleware.Require(middleware.CapViewShopping)
			manageShop := middleware.Require(middleware.CapManageShopping)
			api.GET("/shopping-lists", viewShop, helperHandler.GetShoppingLists)
			api.POST("/shopping-lists", manageShop, helperHandler.CreateShoppingList)
			api.GET("/shopping-lists/suggestions", viewShop, helperHandler.GetShoppingSuggestions)
			api.GET("/shopping-lists/:id", viewShop, helperHandler.GetShoppingListByID)
			api.DELETE("/shopping-lists/:id", manageShop, helperHandler.DeleteShoppingList)
			api.POST("/shopping-lists/:id/send", manageShop, helperHandler.SendShoppingList)
			api.POST("/shopping-lists/:id/viewed", viewShop, helperHandler.MarkShoppingListViewed)
			api.POST("/shopping-lists/:id/close", manageShop, helperHandler.CloseShoppingList)
			api.POST("/shopping-lists/:id/items", manageShop, helperHandler.AddShoppingListEntry)
			api.PATCH("/shopping-lists/:id/items/:itemId", manageShop, helperHandler.UpdateShoppingListEntry)
			api.DELETE("/shopping-lists/:id/items/:itemId", manageShop, helperHandler.DeleteShoppingListEntry)

			// Recipe details
			api.GET("/recipes/:id", helperHandler.GetRecipeDetails)

			// Childcare
			api.GET("/childcare/today", helperHandler.GetTodayChildcare)
			api.POST("/childcare/today", helperHandler.SaveTodayChildcare)
			api.DELETE("/childcare/today", helperHandler.DeleteTodayChildcare)
		}
	}

	// Root redirect
	router.GET("/", func(c *gin.Context) {
		if hasWebApp {
			c.Redirect(302, handlers.WebAppPrefix+"/")
			return
		}
		c.Redirect(302, "/helper")
	})

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Start server
	log.Printf("Starting server on :%s", port)
	log.Printf("Admin interface: http://localhost:%s/admin", port)
	log.Printf("Helper interface: http://localhost:%s/helper", port)

	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
