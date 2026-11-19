package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// Веб-версия приложения (Expo, `npx expo export -p web` с baseUrl /app) раздаётся самим бэкендом:
// один деплой — и API, и интерфейс. Сборку кладёт в webapp/ скрипт фронта scripts/build-for-backend.sh.

// WebAppPrefix — адрес, по которому открывается веб-версия.
const WebAppPrefix = "/app"

// Динамические маршруты Expo Router: /task/123 → task/[id].html.
var webAppDynamic = []struct {
	re   *regexp.Regexp
	file string
}{
	{regexp.MustCompile(`^/task/[^/]+/?$`), "task/[id].html"},
	{regexp.MustCompile(`^/recipe/[^/]+/?$`), "recipe/[id].html"},
	{regexp.MustCompile(`^/shopping/[^/]+/?$`), "shopping/[id].html"},
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// resolveWebAppFile: файл как есть → .html («чистые» адреса) → index.html папки → динамический маршрут.
func resolveWebAppFile(root, urlPath string) string {
	clean := filepath.Clean("/" + urlPath) // убирает ../ — выйти за root нельзя
	direct := filepath.Join(root, clean)
	for _, p := range []string{direct, direct + ".html", filepath.Join(direct, "index.html")} {
		if isRegularFile(p) {
			return p
		}
	}
	for _, d := range webAppDynamic {
		if d.re.MatchString(clean) {
			return filepath.Join(root, d.file)
		}
	}
	return ""
}

// RegisterWebApp подключает раздачу веб-версии из dir по адресу /app. Если сборки нет — ничего не делает.
func RegisterWebApp(router *gin.Engine, dir string) bool {
	root, err := filepath.Abs(dir)
	if err != nil || !isRegularFile(filepath.Join(root, "index.html")) {
		return false
	}
	serve := func(c *gin.Context) {
		target := resolveWebAppFile(root, c.Param("path"))
		if target == "" {
			c.Header("Cache-Control", "no-cache")
			page, err := os.ReadFile(filepath.Join(root, "+not-found.html"))
			if err != nil {
				c.Status(http.StatusNotFound)
				return
			}
			c.Data(http.StatusNotFound, "text/html; charset=utf-8", page)
			return
		}
		// Файлы с хешем в имени (_expo/static) — надолго, HTML — всегда свежий.
		if strings.HasPrefix(target, filepath.Join(root, "_expo")+string(filepath.Separator)) {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Header("Cache-Control", "no-cache")
		}
		c.File(target)
	}
	router.GET(WebAppPrefix, func(c *gin.Context) { c.Redirect(http.StatusFound, WebAppPrefix+"/") })
	router.GET(WebAppPrefix+"/*path", serve)
	router.HEAD(WebAppPrefix+"/*path", serve)
	return true
}
