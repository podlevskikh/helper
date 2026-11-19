package server

import (
	"log"
	"os"
	"podlevskikh/awesomeProject/internal/observability"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/handlers"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/scheduler"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Deps — всё, что нужно роутеру. Тесты собирают тот же роутер, что и прод (с правами и лимитами).
type Deps struct {
	DB    *gorm.DB
	Sched *scheduler.Scheduler
}

// NewRouter собирает все маршруты приложения.
func NewRouter(d Deps) *gin.Engine {
	db, sched := d.DB, d.Sched
	// Initialize Gin router
	// Как gin.Default, но журнал без строки запроса и токенов приглашений (observability.Logger).
	router := gin.New()
	router.Use(observability.Logger(), gin.Recovery())
	// gin.ClientIP не используем для лимитов (см. middleware.clientKey): не доверяем X-Forwarded-For по умолчанию.
	if err := router.SetTrustedProxies(nil); err != nil {
		log.Printf("trusted proxies: %v", err)
	}
	router.Use(observability.Middleware()...)
	router.Use(securityHeaders())
	router.Use(middleware.AppVersion())
	registerHealth(router, db)

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
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Org-Id", "X-App-Version"},
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
	adminHandler.FillDays = func(orgID uint) {
		if err := sched.GenerateScheduleForOrgNextDays(orgID, 7); err != nil {
			log.Printf("Warning: schedule for org %d: %v", orgID, err)
		}
	}
	refreshCleaning := func(orgID uint) {
		if sched == nil {
			return
		}
		if err := sched.RefreshCleaning(orgID, 7); err != nil {
			log.Printf("Warning: refresh cleaning for org %d: %v", orgID, err)
		}
	}
	adminHandler.RefreshCleaning = refreshCleaning
	adminHandler.RefreshMeals = func(orgID uint) {
		if sched == nil {
			return
		}
		if err := sched.RefreshMeals(orgID, 7); err != nil {
			log.Printf("Warning: refresh meals for org %d: %v", orgID, err)
		}
	}
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
	staffCalHandler.RefreshCleaning = refreshCleaning
	notificationsHandler := handlers.NewNotificationsHandler(db)

	// Auth routes
	middleware.TokenVersionLookup = func(userID uint) (int, bool) {
		var v int
		res := db.Raw("SELECT token_version FROM users WHERE id = ?", userID).Scan(&v)
		return v, res.Error == nil && res.RowsAffected > 0
	}
	authMw := middleware.Auth()
	orgMw := middleware.OrgContext(db)

	// Защита от перебора паролей: неудачные попытки с одного адреса.
	loginLimit := middleware.RateLimit(middleware.RateLimitOptions{Max: 10, Per: 15 * time.Minute, OnlyFailures: true})
	registerLimit := middleware.RateLimit(middleware.RateLimitOptions{Max: 10, Per: time.Hour})
	forgotLimit := middleware.RateLimit(middleware.RateLimitOptions{Max: 5, Per: 15 * time.Minute})
	// Письма при смене почты (запрос и «отправить ещё раз») — отдельный счётчик.
	emailLimit := middleware.RateLimit(middleware.RateLimitOptions{Max: 10, Per: 15 * time.Minute})
	// Ссылки из писем (проверка, подтверждение, отмена, откат) — свой счётчик неудач: истёкшая ссылка
	// или повторный клик не съедают попытки входа у всех за тем же адресом.
	linkLimit := middleware.RateLimit(middleware.RateLimitOptions{Max: 20, Per: 15 * time.Minute, OnlyFailures: true})

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
		// Смена почты — с подтверждением по ссылке (internal/handlers/email_change.go).
		authGroup.GET("/email", authMw, authHandler.GetEmailChange)
		authGroup.POST("/email", authMw, emailLimit, loginLimit, authHandler.RequestEmailChange)
		authGroup.DELETE("/email", authMw, authHandler.CancelOwnEmailChange)
		authGroup.POST("/email/resend", authMw, emailLimit, authHandler.ResendEmailChange)
		authGroup.POST("/email/check", linkLimit, authHandler.CheckEmailChange)
		authGroup.POST("/email/confirm", linkLimit, authHandler.ConfirmEmailChange)
		authGroup.POST("/email/cancel", linkLimit, authHandler.CancelEmailChange)
		authGroup.POST("/email/revert", linkLimit, authHandler.RevertEmailChange)
		authGroup.POST("/password/forgot", forgotLimit, authHandler.ForgotPassword)
		authGroup.POST("/password/reset", loginLimit, authHandler.ResetPassword)
		authGroup.POST("/password/reset/check", loginLimit, authHandler.CheckResetToken)
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
		orgsGroup.GET("/:orgId/invites", middleware.Require(middleware.CapManageTeam), inviteHandler.ListInvites)
		orgsGroup.DELETE("/:orgId/invites/:id", middleware.Require(middleware.CapManageTeam), inviteHandler.RevokeInvite)

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
		orgsGroup.POST("/:orgId/notification-prefs/test", middleware.RateLimit(middleware.RateLimitOptions{Max: 5, Per: 10 * time.Minute}), notificationsHandler.SendTest)
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
			// Загрузка фото рецептов убрана: рецепты без фото (решение владелицы), а старый обработчик не проверял размер и содержимое.

			// Recipe Comments
			api.GET("/recipes/:id/comments", adminHandler.GetRecipeComments)
			api.POST("/recipes/:id/comments", adminHandler.CreateRecipeComment)
			api.DELETE("/comments/:id", middleware.Require(middleware.CapManageRecipes), adminHandler.DeleteRecipeComment)

			// Meal times
			api.GET("/mealtimes", adminHandler.GetMealTimes)
			api.GET("/mealtimes/:id", adminHandler.GetMealTime)
			api.POST("/mealtimes", middleware.Require(middleware.CapManageSchedule), adminHandler.CreateMealTime)
			api.PUT("/mealtimes/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.UpdateMealTime)
			api.DELETE("/mealtimes/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.DeleteMealTime)

			// Cleaning zones
			api.GET("/zones", adminHandler.GetCleaningZones)
			api.GET("/zones/:id", adminHandler.GetCleaningZone)
			api.POST("/zones", middleware.Require(middleware.CapManageSchedule), adminHandler.CreateCleaningZone)
			api.PUT("/zones/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.UpdateCleaningZone)
			api.DELETE("/zones/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.DeleteCleaningZone)

			// Childcare schedules
			api.GET("/childcare", adminHandler.GetChildcareSchedules)
			api.GET("/childcare/:id", adminHandler.GetChildcareSchedule)
			api.POST("/childcare", middleware.Require(middleware.CapManageSchedule), adminHandler.CreateChildcareSchedule)
			api.PUT("/childcare/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.UpdateChildcareSchedule)
			api.DELETE("/childcare/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.DeleteChildcareSchedule)

			// Task management (менять расписание может только тот, у кого manage_schedule — не помощник)
			api.GET("/tasks/:id", adminHandler.GetTask)
			api.PUT("/tasks/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.UpdateTask)
			api.POST("/tasks/:id/recipes", middleware.Require(middleware.CapManageSchedule), adminHandler.AddRecipeToTask)
			api.DELETE("/tasks/:id/recipes/:recipe_id", middleware.Require(middleware.CapManageSchedule), adminHandler.RemoveRecipeFromTask)
			api.POST("/tasks/:id/zones", middleware.Require(middleware.CapManageSchedule), adminHandler.AddZoneToTask)
			api.DELETE("/tasks/:id/zones/:zone_id", middleware.Require(middleware.CapManageSchedule), adminHandler.RemoveZoneFromTask)

			// Custom one-off tasks
			api.POST("/custom-tasks", middleware.Require(middleware.CapManageSchedule), adminHandler.CreateCustomTask)
			api.DELETE("/custom-tasks/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.DeleteCustomTask)
			api.GET("/recurring-tasks", adminHandler.GetRecurringTasks)
			api.POST("/recurring-tasks", middleware.Require(middleware.CapManageSchedule), adminHandler.CreateRecurringTask)
			api.DELETE("/recurring-tasks/:id", middleware.Require(middleware.CapManageSchedule), adminHandler.DeleteRecurringTask)

			// Schedule management
			// Пересборка ближайших дней своего дома (раньше любой вошедший стирал задачи всех домов).
			api.POST("/regenerate-schedule", middleware.Require(middleware.CapManageSchedule), func(c *gin.Context) {
				orgID := middleware.MustMembership(c).OrganizationID
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
					WHERE ds.organization_id = ? AND ds.date >= ? AND ds.date < ? AND st.task_type != 'custom'
				)`, orgID, today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete meal_recipes: %v", err)
				}

				// 2. Delete task_zones for non-custom tasks
				if err := db.Exec(`DELETE FROM task_zones WHERE schedule_task_id IN (
					SELECT st.id FROM schedule_tasks st
					JOIN daily_schedules ds ON ds.id = st.schedule_id
					WHERE ds.organization_id = ? AND ds.date >= ? AND ds.date < ? AND st.task_type != 'custom'
				)`, orgID, today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete task_zones: %v", err)
				}

				// 3. Delete non-custom schedule_tasks
				if err := db.Exec(`DELETE FROM schedule_tasks
					WHERE task_type != 'custom'
					AND schedule_id IN (SELECT id FROM daily_schedules WHERE organization_id = ? AND date >= ? AND date < ?)`,
					orgID, today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete schedule tasks: %v", err)
				}

				// 4. Delete daily_schedules that have no tasks left
				if err := db.Exec(`DELETE FROM daily_schedules
					WHERE organization_id = ? AND date >= ? AND date < ?
					AND id NOT IN (SELECT DISTINCT schedule_id FROM schedule_tasks)`,
					orgID, today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete empty daily schedules: %v", err)
				}

				// 5. Reset generated=false on schedules that still exist (have custom tasks only)
				if err := db.Exec(`UPDATE daily_schedules SET generated = false
					WHERE organization_id = ? AND date >= ? AND date < ?`, orgID, today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to reset generated flag: %v", err)
				}

				log.Println("Old schedules cleared, generating new schedules...")

				// Generate new schedules
				if err := sched.GenerateScheduleForOrgNextDays(orgID, days); err != nil {
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
			// Старые /helper/api/shopping* (без списков и без проверки прав) удалены: клиент давно на /shopping-lists.

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
			api.POST("/childcare/today", middleware.Require(middleware.CapManageSchedule), helperHandler.SaveTodayChildcare)
			api.DELETE("/childcare/today", middleware.Require(middleware.CapManageSchedule), helperHandler.DeleteTodayChildcare)
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

	return router
}

// securityHeaders — защита от встраивания в чужие сайты (кликджекинг), подмены типа и http.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
