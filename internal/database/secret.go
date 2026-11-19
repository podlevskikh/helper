package database

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// EnsureJWTSecret: если JWT_SECRET не задан в окружении, берём секрет из базы
// (создаём случайный при первом запуске). Без этого токены подписывались бы
// известной строкой из кода, и их мог бы подделать кто угодно.
func EnsureJWTSecret(db *gorm.DB) {
	if os.Getenv("JWT_SECRET") != "" {
		return
	}
	db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}) // секрет — не в лог
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS app_secrets (name TEXT PRIMARY KEY, value TEXT NOT NULL)`).Error; err != nil {
		log.Fatalf("JWT_SECRET не задан, а сохранить секрет в базе не удалось: %v", err)
	}
	var value string
	db.Raw(`SELECT value FROM app_secrets WHERE name = 'jwt_secret'`).Scan(&value)
	if value == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("JWT secret: %v", err)
		}
		db.Exec(`INSERT INTO app_secrets (name, value) VALUES ('jwt_secret', ?) ON CONFLICT (name) DO NOTHING`, hex.EncodeToString(b))
		db.Raw(`SELECT value FROM app_secrets WHERE name = 'jwt_secret'`).Scan(&value)
	}
	if value == "" {
		log.Fatal("JWT_SECRET не задан и не найден в базе")
	}
	os.Setenv("JWT_SECRET", value)
	log.Println("JWT_SECRET не задан в окружении — используется секрет, сохранённый в базе")
}
