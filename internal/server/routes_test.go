package server

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// API-01: снимок маршрутов (internal/server/routes.txt). По нему фронт проверяет, что вызывает
// только существующие маршруты (scripts/check-contract.mjs во фронте). Обновить: UPDATE_ROUTES=1 go test ./internal/server -run RoutesSnapshot
func TestRoutesSnapshot(t *testing.T) {
	t.Chdir("../..")
	r := NewRouter(Deps{})
	var lines []string
	for _, rt := range r.Routes() {
		lines = append(lines, rt.Method+" "+rt.Path)
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"
	const file = "internal/server/routes.txt"
	if os.Getenv("UPDATE_ROUTES") == "1" {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, _ := os.ReadFile(file)
	if string(want) != got {
		t.Errorf("маршруты изменились — обновите снимок: UPDATE_ROUTES=1 go test ./internal/server -run RoutesSnapshot")
	}
}
