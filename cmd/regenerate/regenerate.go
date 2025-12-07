package main

import (
	"log"
	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/scheduler"
)

func main() {
	// Initialize database
	if err := database.Initialize("./helper_app.db"); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	db := database.GetDB()

	log.Println("Clearing old schedules...")

	// Delete all existing schedules and tasks
	if err := db.Exec("DELETE FROM schedule_tasks").Error; err != nil {
		log.Printf("Warning: Failed to delete schedule tasks: %v", err)
	}

	if err := db.Exec("DELETE FROM daily_schedules").Error; err != nil {
		log.Printf("Warning: Failed to delete daily schedules: %v", err)
	}

	log.Println("Old schedules cleared successfully!")

	// Initialize scheduler
	sched := scheduler.NewScheduler(db)

	// Generate schedules for the next 7 days
	log.Println("Generating new schedules for the next 7 days...")
	if err := sched.GenerateScheduleForNextDays(7); err != nil {
		log.Fatalf("Failed to generate schedules: %v", err)
	}

	log.Println("Schedules regenerated successfully!")

	// Display summary
	var scheduleCount int64
	db.Model(&models.DailySchedule{}).Count(&scheduleCount)
	log.Printf("Total schedules created: %d", scheduleCount)

	var taskCount int64
	db.Model(&models.ScheduleTask{}).Count(&taskCount)
	log.Printf("Total tasks created: %d", taskCount)
}

