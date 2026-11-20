package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// PUB-02: главная — лендинг (RU на /, EN на /en/), картинки на /site/; старые /admin2/ и /helper2/ ведут в приложение.
func TestLandingPages(t *testing.T) {
	t.Chdir("../..")
	r := NewRouter(Deps{})
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	for _, c := range []struct{ path, lang string }{{"/", `lang="ru"`}, {"/en/", `lang="en"`}} {
		w := get(c.path)
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: %d %s", c.path, w.Code, w.Header().Get("Content-Type"))
		}
		if !strings.Contains(body, c.lang) || !strings.Contains(body, `href="/app/"`) || !strings.Contains(body, "/app/site-icon.png?v=2") {
			t.Errorf("%s: нет языка, ссылки на приложение или иконки сайта", c.path)
		}
		for _, link := range []string{"/app/privacy", "/app/terms", "/app/support"} {
			if !strings.Contains(body, `href="`+link+`"`) {
				t.Errorf("%s: нет ссылки %s", c.path, link)
			}
		}
	}

	if w := get("/en"); w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/en/" {
		t.Errorf("/en: %d %s", w.Code, w.Header().Get("Location"))
	}
	for _, p := range []string{"/site/mascot.png", "/site/illustration.jpg", "/site/og.jpg"} {
		if w := get(p); w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
			t.Errorf("%s: %d %s", p, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if w := get("/site/../index.html"); w.Code == http.StatusOK && strings.Contains(w.Body.String(), "<html") {
		t.Errorf("/site/ отдаёт файлы вне папки картинок")
	}
	if w := get("/site/nope.png"); w.Code != http.StatusNotFound {
		t.Errorf("/site/nope.png: %d", w.Code)
	}
	for _, p := range []string{"/admin2/", "/helper2/"} {
		if w := get(p); w.Code != http.StatusFound || !strings.HasSuffix(w.Header().Get("Location"), "/app/") {
			t.Errorf("%s: %d %s", p, w.Code, w.Header().Get("Location"))
		}
	}
}
