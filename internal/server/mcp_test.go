package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"podlevskikh/awesomeProject/internal/models"
)

const mcpURL = "https://www.helperday.app/mcp"

func (e *env) form(path string, v url.Values) (int, map[string]any) {
	hr := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
	hr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, hr)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

// mcpCall — JSON-RPC на /mcp; ответ может прийти как SSE («data: {...}»).
func (e *env) mcpCall(token, method string, params any) (int, map[string]any, string) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	hr := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		hr.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, hr)
	raw := w.Body.String()
	for _, l := range strings.Split(raw, "\n") {
		if strings.HasPrefix(l, "data: ") {
			raw = strings.TrimPrefix(l, "data: ")
			break
		}
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	return w.Code, m, w.Header().Get("WWW-Authenticate")
}

// tool — вызвать инструмент; текст результата и признак ошибки.
func (e *env) tool(token, name string, args map[string]any) (string, bool) {
	e.t.Helper()
	code, m, _ := e.mcpCall(token, "tools/call", map[string]any{"name": name, "arguments": args})
	if code != 200 {
		return fmt.Sprintf("HTTP %d", code), true
	}
	res, _ := m["result"].(map[string]any)
	if res == nil {
		return fmt.Sprintf("%v", m["error"]), true
	}
	text := ""
	if cs, _ := res["content"].([]any); len(cs) > 0 {
		text, _ = cs[0].(map[string]any)["text"].(string)
	}
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

type oauthClient struct {
	id, verifier string
}

func (e *env) registerClient() oauthClient {
	e.t.Helper()
	w := e.do(req{method: "POST", path: "/oauth/register", body: map[string]any{
		"redirect_uris": []string{"https://claude.ai/api/mcp/auth_callback"}, "client_name": "Claude", "token_endpoint_auth_method": "none"}})
	e.expect("регистрация клиента", w, 201)
	return oauthClient{id: e.json(w)["client_id"].(string), verifier: strings.Repeat("v", 20) + uniq() + strings.Repeat("x", 30)}
}

func challenge(v string) string {
	s := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

// authorize — /oauth/authorize → id запроса на экране согласия.
func (e *env) authorize(c oauthClient) string {
	e.t.Helper()
	q := url.Values{"response_type": {"code"}, "client_id": {c.id}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge": {challenge(c.verifier)}, "code_challenge_method": {"S256"}, "state": {"st-1"},
		"scope": {"recipes offline_access"}, "resource": {mcpURL}}
	w := e.do(req{method: "GET", path: "/oauth/authorize?" + q.Encode()})
	e.expect("authorize", w, 302)
	loc, _ := url.Parse(w.Header().Get("Location"))
	if !strings.HasSuffix(loc.Path, "/app/oauth-consent") {
		e.t.Fatalf("authorize ведёт не на экран согласия: %s", loc)
	}
	return loc.Query().Get("req")
}

// connect — полный вход: согласие человека a в доме org → токены.
func (e *env) connect(a acct, org uint) (access, refresh string) {
	e.t.Helper()
	c := e.registerClient()
	id := e.authorize(c)
	e.expect("запрос согласия", e.do(req{method: "GET", path: "/oauth/requests/" + id, token: a.Token}), 200)
	w := e.do(req{method: "POST", path: "/oauth/requests/" + id + "/decision", token: a.Token, body: map[string]any{"approve": true, "org_id": org}})
	e.expect("Разрешить", w, 200)
	red, _ := url.Parse(e.json(w)["redirect"].(string))
	if red.Query().Get("state") != "st-1" || red.Query().Get("code") == "" {
		e.t.Fatalf("возврат в Claude без кода или state: %s", red)
	}
	code, m := e.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {red.Query().Get("code")},
		"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "client_id": {c.id}, "code_verifier": {c.verifier}, "resource": {mcpURL}})
	if code != 200 {
		e.t.Fatalf("обмен кода: %d %v", code, m)
	}
	return m["access_token"].(string), m["refresh_token"].(string)
}

// MCP-01…MCP-06: коннектор для Claude — OAuth (DCR, согласие, PKCE, обновление с ротацией, отзыв) и инструменты рецептов.
func TestMCPConnector(t *testing.T) {
	e := newEnv(t, 1400)
	o := e.owner("mcp")
	h := e.member(o, "helper", "mcp")
	adm := e.member(o, "admin", "mcp")
	other := e.owner("mcpOther")
	lunch := e.dishType(o, "Обед")
	e.recipe(o, "Борщ", lunch)
	foreign := e.recipe(other, "Чужой суп", e.dishType(other, "Суп"))

	// MCP-01: discovery и 401 без токена.
	w := e.do(req{method: "GET", path: "/.well-known/oauth-protected-resource/mcp"})
	e.expect("PRM", w, 200)
	if e.json(w)["resource"] != mcpURL {
		t.Fatalf("resource: %v", e.json(w))
	}
	w = e.do(req{method: "GET", path: "/.well-known/oauth-authorization-server"})
	if m := e.json(w); m["registration_endpoint"] != "https://www.helperday.app/oauth/register" ||
		fmt.Sprint(m["code_challenge_methods_supported"]) != "[S256]" {
		t.Fatalf("AS metadata: %v", m)
	}
	code, _, www := e.mcpCall("", "tools/list", map[string]any{})
	if code != 401 || !strings.Contains(www, `resource_metadata="https://www.helperday.app/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("без токена: %d %q", code, www)
	}
	if code, _, _ := e.mcpCall(o.Token, "tools/list", map[string]any{}); code != 401 {
		t.Fatalf("токен приложения на /mcp не годится: %d", code)
	}

	// MCP-02: регистрация только с адресом Claude или localhost.
	e.expect("чужой redirect", e.do(req{method: "POST", path: "/oauth/register", body: map[string]any{
		"redirect_uris": []string{"https://evil.example/cb"}}}), 400)
	e.expect("localhost redirect", e.do(req{method: "POST", path: "/oauth/register", body: map[string]any{
		"redirect_uris": []string{"http://localhost:6274/oauth/callback"}}}), 201)
	c := e.registerClient()
	bad := url.Values{"response_type": {"code"}, "client_id": {c.id}, "redirect_uri": {"https://evil.example/cb"},
		"code_challenge": {challenge(c.verifier)}, "code_challenge_method": {"S256"}}
	e.expect("authorize с чужим redirect", e.do(req{method: "GET", path: "/oauth/authorize?" + bad.Encode()}), 400)
	noPKCE := url.Values{"response_type": {"code"}, "client_id": {c.id}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "state": {"s"}}
	w = e.do(req{method: "GET", path: "/oauth/authorize?" + noPKCE.Encode()})
	if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "error=invalid_request") {
		t.Fatalf("без PKCE: %d %s", w.Code, w.Header().Get("Location"))
	}

	// MCP-03: согласие — только тот, кто ведёт дом; запрос привязан к открывшему; неверный verifier.
	id := e.authorize(c)
	w = e.do(req{method: "GET", path: "/oauth/requests/" + id, token: h.Token})
	e.expect("помощник открыл запрос", w, 200)
	if hs, _ := e.json(w)["homes"].([]any); len(hs) != 0 {
		t.Fatalf("помощнику нечего подключать: %v", hs)
	}
	e.expect("помощник разрешает", e.do(req{method: "POST", path: "/oauth/requests/" + id + "/decision", token: h.Token,
		body: map[string]any{"approve": true, "org_id": o.Org}}), 403)
	e.expect("чужой человек — уже не его запрос", e.do(req{method: "GET", path: "/oauth/requests/" + id, token: o.Token}), 404)
	id = e.authorize(c)
	e.expect("чужой дом", e.do(req{method: "POST", path: "/oauth/requests/" + id + "/decision", token: o.Token,
		body: map[string]any{"approve": true, "org_id": other.Org}}), 403)
	w = e.do(req{method: "POST", path: "/oauth/requests/" + id + "/decision", token: o.Token, body: map[string]any{"approve": true, "org_id": o.Org}})
	red, _ := url.Parse(e.json(w)["redirect"].(string))
	if code, m := e.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {red.Query().Get("code")},
		"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "client_id": {c.id}, "code_verifier": {strings.Repeat("z", 50)}}); code != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("неверный PKCE: %d %v", code, m)
	}
	id = e.authorize(c)
	w = e.do(req{method: "POST", path: "/oauth/requests/" + id + "/decision", token: o.Token, body: map[string]any{"approve": false}})
	if !strings.Contains(e.json(w)["redirect"].(string), "error=access_denied") {
		t.Fatalf("Отмена: %v", e.json(w))
	}

	// MCP-04: инструменты.
	access, refresh := e.connect(o, o.Org)
	_, m, _ := e.mcpCall(access, "tools/list", map[string]any{})
	tools := map[string]map[string]any{}
	for _, x := range m["result"].(map[string]any)["tools"].([]any) {
		tl := x.(map[string]any)
		ann, _ := tl["annotations"].(map[string]any)
		tools[tl["name"].(string)] = ann
	}
	for _, n := range []string{"list_dish_types", "find_recipes", "get_recipe", "get_recipe_template", "preview_recipe_import",
		"import_recipes", "undo_recipe_import", "update_recipe"} {
		if tools[n] == nil {
			t.Fatalf("нет инструмента %s: %v", n, tools)
		}
	}
	if tools["find_recipes"]["readOnlyHint"] != true || tools["update_recipe"]["destructiveHint"] != true || tools["import_recipes"]["readOnlyHint"] == true {
		t.Fatalf("пометки инструментов: %v", tools)
	}
	if txt, isErr := e.tool(access, "list_dish_types", nil); isErr || !strings.Contains(txt, "Обед") {
		t.Fatalf("типы: %s", txt)
	}
	if txt, _ := e.tool(access, "find_recipes", map[string]any{"query": "суп"}); strings.Contains(txt, "Чужой") {
		t.Fatalf("видно чужой дом: %s", txt)
	}
	text := "## Борщ\nИнгредиенты:\nСвёкла — 80 г\n\n## Плов\nType: обед\nLink: https://example.com/plov\nIngredients:\nRice — 70 g\nChicken — 150 g\nHow to cook:\nFry."
	txt, isErr := e.tool(access, "preview_recipe_import", map[string]any{"text": text})
	if isErr || !strings.Contains(txt, `"duplicate"`) || !strings.Contains(txt, `"new"`) {
		t.Fatalf("предпросмотр: %s", txt)
	}
	txt, isErr = e.tool(access, "import_recipes", map[string]any{"text": text})
	var res map[string]any
	_ = json.Unmarshal([]byte(txt), &res)
	if isErr || res["created"] != 1.0 || res["skipped"] != 1.0 || res["source"] != "mcp" {
		t.Fatalf("сохранение: %s", txt)
	}
	var plov models.Recipe
	e.db.Where("organization_id = ? AND name = ?", o.Org, "Плов").First(&plov)
	if !strings.HasSuffix(plov.UpdatedByName, "· Claude") || !plov.IsActive || plov.DishTypeID == nil || *plov.DishTypeID != uint(lunch) {
		t.Fatalf("плов из Claude: %+v", plov)
	}
	txt, isErr = e.tool(access, "update_recipe", map[string]any{"id": plov.ID, "ingredients": []string{"Rice — 80 g", "- Chicken: 150 g"}, "cook_time": 40})
	e.db.First(&plov, plov.ID)
	if isErr || plov.Ingredients != "Rice — 80 g\nChicken — 150 g" || plov.CookTime != 40 {
		t.Fatalf("правка: %s / %+v", txt, plov)
	}
	if _, isErr := e.tool(access, "update_recipe", map[string]any{"id": uint(foreign), "name": "взлом"}); !isErr {
		t.Fatalf("правка рецепта чужого дома прошла")
	}
	if _, isErr := e.tool(access, "get_recipe", map[string]any{"id": uint(foreign)}); !isErr {
		t.Fatalf("чтение рецепта чужого дома прошло")
	}
	w = e.do(req{method: "GET", path: "/admin/api/recipe-imports", token: o.Token, org: o.Org})
	if l := e.jsonList(w); len(l) != 1 || l[0]["source"] != "mcp" {
		t.Fatalf("недавние загрузки: %v", l)
	}
	txt, isErr = e.tool(access, "undo_recipe_import", map[string]any{"import_id": res["import_id"]})
	if isErr || !strings.Contains(txt, `"deleted": 1`) {
		t.Fatalf("отмена: %s", txt)
	}

	// MCP-05: обновление токена с ротацией; повтор старого — отзыв подключения.
	code, m2 := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "resource": {mcpURL}})
	if code != 200 || m2["refresh_token"] == refresh {
		t.Fatalf("обновление: %d %v", code, m2)
	}
	access2 := m2["access_token"].(string)
	if _, isErr := e.tool(access2, "list_dish_types", nil); isErr {
		t.Fatalf("новый токен не работает")
	}
	// Повтор сразу после обмена (ответ потерялся) — ещё одна пара, один раз.
	if code, m3 := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}); code != 200 {
		t.Fatalf("повтор refresh в первые секунды: %d %v", code, m3)
	}
	if code, m3 := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}); code != 400 || m3["error"] != "invalid_grant" {
		t.Fatalf("второй повтор старого refresh: %d %v", code, m3)
	}
	if code, _, _ := e.mcpCall(access2, "tools/list", map[string]any{}); code != 401 {
		t.Fatalf("после повтора refresh подключение должно быть отозвано: %d", code)
	}

	// MCP-06: «Подключения» и «Отключить»; потеря прав закрывает доступ.
	access3, _ := e.connect(o, o.Org)
	w = e.do(req{method: "GET", path: "/me/connections", token: o.Token})
	list := e.jsonList(w)
	if len(list) != 1 || list[0]["client_name"] != "Claude" || list[0]["org_id"] != float64(o.Org) {
		t.Fatalf("подключения: %v", list)
	}
	e.expect("отключить чужое", e.do(req{method: "DELETE", path: fmt.Sprintf("/me/connections/%.0f", list[0]["id"]), token: h.Token}), 404)
	e.expect("отключить", e.do(req{method: "DELETE", path: fmt.Sprintf("/me/connections/%.0f", list[0]["id"]), token: o.Token}), 200)
	if code, _, _ := e.mcpCall(access3, "tools/list", map[string]any{}); code != 401 {
		t.Fatalf("после «Отключить» токен работает: %d", code)
	}
	accessAdm, _ := e.connect(adm, o.Org)
	if _, isErr := e.tool(accessAdm, "list_dish_types", nil); isErr {
		t.Fatalf("админ подключил Claude")
	}
	e.expect("админ → помощник", e.do(req{method: "PATCH", path: fmt.Sprintf("/orgs/%d/members/%d", o.Org, adm.UserID), token: o.Token, org: o.Org,
		body: map[string]any{"role": "helper"}}), 200)
	if code, _, _ := e.mcpCall(accessAdm, "tools/list", map[string]any{}); code != 401 {
		t.Fatalf("без manage_recipes токен работает: %d", code)
	}
	// Права вернули — старое подключение не оживает, нужно новое согласие.
	e.expect("помощник → админ", e.do(req{method: "PATCH", path: fmt.Sprintf("/orgs/%d/members/%d", o.Org, adm.UserID), token: o.Token, org: o.Org,
		body: map[string]any{"role": "admin"}}), 200)
	if code, _, _ := e.mcpCall(accessAdm, "tools/list", map[string]any{}); code != 401 {
		t.Fatalf("после возврата прав старый токен снова работает: %d", code)
	}
	// Убрали из команды — подключение отозвано.
	accessAdm2, _ := e.connect(adm, o.Org)
	e.expect("убрать из команды", e.do(req{method: "DELETE", path: fmt.Sprintf("/orgs/%d/members/%d", o.Org, adm.UserID), token: o.Token, org: o.Org}), 204)
	if code, _, _ := e.mcpCall(accessAdm2, "tools/list", map[string]any{}); code != 401 {
		t.Fatalf("после «убрать из команды» токен работает: %d", code)
	}
	var live int64
	e.db.Model(&models.OAuthGrant{}).Where("user_id = ? AND org_id = ? AND revoked_at IS NULL", adm.UserID, o.Org).Count(&live)
	if live != 0 {
		t.Fatalf("подключения убранного не отозваны: %d", live)
	}
	// Поиск: «%» — просто символ.
	access4, _ := e.connect(o, o.Org)
	if txt, _ := e.tool(access4, "find_recipes", map[string]any{"query": "%"}); strings.Contains(txt, "Борщ") {
		t.Fatalf("«%%» в поиске нашёл всё: %s", txt)
	}
}
