package handlers

import "testing"

// PUB-09: файлы сборки с хешем в имени кешируются навсегда, HTML и файлы без хеша — перепроверяются.
func TestWebAppCacheControl(t *testing.T) {
	const imm, fresh = "public, max-age=31536000, immutable", "no-cache"
	root := "/srv/webapp"
	for path, want := range map[string]string{
		"/srv/webapp/_expo/static/js/web/entry-a65dfbaff5d4dcf10a3c0b7af0c245c0.js":                                                      imm,
		"/srv/webapp/assets/assets/images/mascot-house.f72a3ca201e0df9fb63990ba44295c80.png":                                             imm,
		"/srv/webapp/assets/___node_modules/@expo-google-fonts/nunito/400Regular/Nunito_400Regular.f04f0e9ff969fd52a75deade3a9761cd.ttf": imm,
		"/srv/webapp/fonts/nunito-400regular.d254cccf4c09a5d13e57.woff2":                                                                 imm,
		"/srv/webapp/assets/images/logo.png":                                                                                             fresh,
		"/srv/webapp/fonts/readme.txt":                                                                                                   fresh,
		"/srv/webapp/index.html":                                                                                                         fresh,
		"/srv/webapp/favicon.ico":                                                                                                        fresh,
		"/srv/webapp/site-icon.png":                                                                                                      fresh,
	} {
		if got := webAppCacheControl(root, path); got != want {
			t.Errorf("%s: %q, ждали %q", path, got, want)
		}
	}
}
