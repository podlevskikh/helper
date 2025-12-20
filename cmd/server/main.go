package main

import (
	"log"
	"os"
	"time"

	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/handlers"
	"podlevskikh/awesomeProject/internal/scheduler"

	"github.com/gin-gonic/gin"
)

func main() {
	// Get database path from environment or use default
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./helper_app.db"
	}

	// Initialize database
	if err := database.Initialize(dbPath); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	db := database.GetDB()

	// Initialize scheduler
	sched := scheduler.NewScheduler(db)

	// Generate initial schedules for the next 7 days
	log.Println("Generating initial schedules...")
	if err := sched.GenerateScheduleForNextDays(7); err != nil {
		log.Printf("Warning: Failed to generate initial schedules: %v", err)
	}

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

	// Load HTML templates
	router.LoadHTMLGlob("web/templates/*")

	// Serve static files
	router.Static("/static", "./web/static")

	// Initialize handlers
	adminHandler := handlers.NewAdminHandler(db)
	helperHandler := handlers.NewHelperHandler(db)

	// Admin routes
	admin := router.Group("/admin")
	{
		// Web pages
		admin.GET("/", func(c *gin.Context) {
			c.HTML(200, "admin.html", nil)
		})

		// API routes
		api := admin.Group("/api")
		{
			// Recipes
			api.GET("/recipes", adminHandler.GetRecipes)
			api.GET("/recipes/:id", adminHandler.GetRecipe)
			api.POST("/recipes", adminHandler.CreateRecipe)
			api.PUT("/recipes/:id", adminHandler.UpdateRecipe)
			api.DELETE("/recipes/:id", adminHandler.DeleteRecipe)
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
			api.POST("/zones", adminHandler.CreateCleaningZone)
			api.PUT("/zones/:id", adminHandler.UpdateCleaningZone)
			api.DELETE("/zones/:id", adminHandler.DeleteCleaningZone)

			// Childcare schedules
			api.GET("/childcare", adminHandler.GetChildcareSchedules)
			api.POST("/childcare", adminHandler.CreateChildcareSchedule)
			api.PUT("/childcare/:id", adminHandler.UpdateChildcareSchedule)
			api.DELETE("/childcare/:id", adminHandler.DeleteChildcareSchedule)

			// Task management
			api.GET("/tasks/:id", adminHandler.GetTask)
			api.POST("/tasks/:id/recipes", adminHandler.AddRecipeToTask)
			api.DELETE("/tasks/:id/recipes/:recipe_id", adminHandler.RemoveRecipeFromTask)
			api.POST("/tasks/:id/zones", adminHandler.AddZoneToTask)
			api.DELETE("/tasks/:id/zones/:zone_id", adminHandler.RemoveZoneFromTask)

			// Schedule management
			api.POST("/regenerate-schedule", func(c *gin.Context) {
				days := 7

				// Delete existing schedules for the next N days
				today := time.Now()
				endDate := today.AddDate(0, 0, days)

				log.Println("Clearing schedules from", today.Format("2006-01-02"), "to", endDate.Format("2006-01-02"))

				// Delete tasks first (foreign key constraint)
				if err := db.Exec("DELETE FROM schedule_tasks WHERE schedule_id IN (SELECT id FROM daily_schedules WHERE date >= ? AND date < ?)", today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete schedule tasks: %v", err)
				}

				// Delete schedules
				if err := db.Exec("DELETE FROM daily_schedules WHERE date >= ? AND date < ?", today, endDate).Error; err != nil {
					log.Printf("Warning: Failed to delete daily schedules: %v", err)
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

		// API routes
		api := helper.Group("/api")
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

			// Recipe details
			api.GET("/recipes/:id", helperHandler.GetRecipeDetails)
		}
	}

	// Root redirect
	router.GET("/", func(c *gin.Context) {
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
