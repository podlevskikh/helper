package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/models"
)

// Новый дом сразу получает системные категории задач (раньше — только после перезапуска сервера).
// Нужна отдельная база: TEST_DATABASE_URL=... go test ./internal/handlers -run Register
func TestRegisterCreatesDefaultCategories(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	t.Setenv("SMTP_HOST", "")
	if err := database.Initialize(url); err != nil {
		t.Fatal(err)
	}
	db := database.DB
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/auth/register", NewAuthHandler(db).Register)

	suffix := fmt.Sprint(time.Now().UnixNano())
	b, _ := json.Marshal(map[string]string{
		"email": "reg-" + suffix + "@example.test", "password": "owner-pass-1", "org_name": "Home " + suffix,
	})
	req := httptest.NewRequest("POST", "/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	var reg struct {
		User models.User `json:"user"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &reg)
	var orgID uint
	db.Model(&models.Membership{}).Where("user_id = ?", reg.User.ID).Pluck("organization_id", &orgID)

	var keys []string
	db.Model(&models.TaskCategory{}).Where("organization_id = ?", orgID).Order("sort_order").Pluck("system_key", &keys)
	want := []string{"meal", "cleaning", "childcare", "custom"}
	if fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Fatalf("категории нового дома: %v, ждали %v", keys, want)
	}
}
