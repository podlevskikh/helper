package main

import (
	"context"
	"log"
	"os"
	"time"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/handlers"
	"podlevskikh/awesomeProject/internal/notify"
	"podlevskikh/awesomeProject/internal/scheduler"
	"podlevskikh/awesomeProject/internal/server"

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

	router := server.NewRouter(server.Deps{DB: db, Sched: sched})

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
