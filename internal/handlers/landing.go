package handlers

import (
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// Главная страница сайта (лендинг) — статические файлы из landing/: русская версия на /,
// английская на /en/, картинки на /site/*. Исходник — макет на холсте Claude Design,
// тексты по docs/brand/voice.md фронта.

// RegisterLanding подключает лендинг из dir. Если папки нет — ничего не делает и возвращает false.
func RegisterLanding(router *gin.Engine, dir string) bool {
	root, err := filepath.Abs(dir)
	if err != nil || !isRegularFile(filepath.Join(root, "index.html")) {
		return false
	}
	page := func(file string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-cache")
			c.File(filepath.Join(root, file))
		}
	}
	router.GET("/", page("index.html"))
	router.HEAD("/", page("index.html"))
	router.GET("/en", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/en/") })
	router.GET("/en/", page(filepath.Join("en", "index.html")))

	for _, f := range []string{"robots.txt", "sitemap.xml"} {
		router.GET("/"+f, page(f))
	}
	// Иконка сайта по старому адресу браузеров и поисковиков — та же, что у страниц сайта.
	router.GET("/favicon.ico", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, WebAppPrefix+"/site-icon.png?v=2")
	})

	assets := filepath.Join(root, "site")
	serveAsset := func(c *gin.Context) {
		p := filepath.Join(assets, filepath.Clean("/"+c.Param("path"))) // ../ не выходит за папку
		if !isRegularFile(p) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "public, max-age=86400")
		c.File(p)
	}
	router.GET("/site/*path", serveAsset)
	router.HEAD("/site/*path", serveAsset)
	return true
}
