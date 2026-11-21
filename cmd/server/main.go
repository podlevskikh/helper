package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/handlers"
	"podlevskikh/awesomeProject/internal/leader"
	"podlevskikh/awesomeProject/internal/mail"
	"podlevskikh/awesomeProject/internal/notify"
	"podlevskikh/awesomeProject/internal/observability"
	"podlevskikh/awesomeProject/internal/scheduler"
	"podlevskikh/awesomeProject/internal/server"
)

func main() {
	// Get database URL from environment
	databaseURL := os.Getenv("DATABASE_URL")

	// Debug logging for Railway
	if databaseURL == "" {
		log.Println("WARNING: DATABASE_URL environment variable is not set")
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
	database.LoadSealedSecrets(database.SealedFile) // пароль почты, SENTRY_DSN — см. scripts/seal-*.sh
	defer observability.Init(server.CommitSHA())()

	// SIGTERM (выкатка, рестарт) → мягкая остановка: новые запросы не принимаем, начатые дожидаемся.
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stopSignals()

	// Initialize scheduler
	sched := scheduler.NewScheduler(db)

	// Праздники стран: загружаем при старте (если ещё не загружены) и обновляем раз в сутки
	calendar.StartRefresher(db)

	// Письмо не ушло после всех попыток — в Sentry (без адреса получателя).
	mail.OnFailure = func(subject string, err error) {
		observability.CaptureMessage(fmt.Sprintf("mail: письмо «%s» не отправлено (SMTP %d): %s", subject, mail.SMTPCode(err), mail.SafeError(err)))
	}

	// Пуш-уведомления: очередь → Expo (PUSH_DRY_RUN=1 — только в лог)
	var pushSender notify.Sender = notify.NewExpoClientFromEnv()
	if os.Getenv("PUSH_DRY_RUN") == "1" {
		pushSender = notify.LogSender{Printf: log.Printf}
	}
	pushWorker := &notify.Worker{DB: db, Sender: pushSender, EnsureDay: handlers.EnsureDay(db)}

	router := server.NewRouter(server.Deps{DB: db, Sched: sched})

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	log.Printf("Starting server on :%s", port)

	// Фоновые задачи — только на ведущем сервере (во время выкатки работают две версии сразу).
	// Начальная сборка дней — уже после открытия порта: старт и healthcheck не ждут её.
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	jobsDone := make(chan struct{})
	go func() {
		defer close(jobsDone)
		leader.Run(ctx, sqlDB, leader.Name, func(jobCtx context.Context) {
			pushDone := make(chan struct{})
			go func() { defer close(pushDone); pushWorker.Run(jobCtx) }()
			defer func() { <-pushDone }() // ведущим перестаём, только когда очередь пушей остановилась
			log.Println("Generating initial schedules...")
			if err := sched.GenerateScheduleForNextDays(7); err != nil {
				log.Printf("Warning: Failed to generate initial schedules: %v", err)
			}
			// Каждый час: у домов разные часовые пояса, новый день дома собирается вскоре после его полуночи.
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-jobCtx.Done():
					return
				case <-ticker.C:
					// Копии задач, которые могла поставить версия без блокировки дня (во время выкатки).
					if _, err := database.DedupeRoutineTasks(db, time.Now().AddDate(0, 0, -2)); err != nil {
						log.Printf("dedupe routine tasks: %v", err)
					}
					log.Println("Running daily schedule generation...")
					if err := sched.GenerateScheduleForNextDays(7); err != nil {
						log.Printf("Error in daily schedule generation: %v", err)
					}
				}
			}
		})
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", err)
		}
	case <-ctx.Done():
	}
	log.Println("Остановка: дожидаемся начатых запросов и писем…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	select {
	case <-jobsDone:
	case <-time.After(5 * time.Second):
		log.Println("shutdown: фоновые задачи не успели остановиться")
	}
	if !mail.Wait(10 * time.Second) {
		log.Println("shutdown: не все письма успели уйти")
	}
	log.Println("Сервер остановлен")
}
