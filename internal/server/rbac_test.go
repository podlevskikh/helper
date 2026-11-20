package server

import (
	"fmt"
	"strings"
	"testing"
)

// ROLE-02: матрица «маршрут × роль» на настоящем роутере. Падает, если у нового маршрута нет строки в матрице.
//
// Политики:
//   public  — без входа;
//   auth    — любой вошедший, без дома;
//   member  — любой участник дома (owner, admin, manager, helper);
//   иначе — список ролей, которым маршрут разрешён.
// Разрешено = ответ не 401/403. Запрещено участнику = ровно 403. Без входа = ровно 401.
// Чужой дом (X-Org-Id дома, где человек не участник) = ровно 403.

var (
	all      = []string{"owner", "admin", "manager", "helper"}
	schedule = []string{"owner", "admin", "manager"}
	team     = []string{"owner", "admin"}
	recipes  = []string{"owner", "admin"}
	settings = []string{"owner"}
)

type policy struct {
	kind  string // public | auth | org
	roles []string
}

func pub() policy           { return policy{kind: "public"} }
func authOnly() policy      { return policy{kind: "auth"} }
func org(r []string) policy { return policy{kind: "org", roles: r} }

var routePolicy = map[string]policy{
	// без входа
	"POST /auth/register": pub(), "POST /auth/login": pub(), "POST /auth/refresh": pub(), "POST /auth/logout": pub(),
	"POST /auth/password/forgot": pub(), "POST /auth/password/reset": pub(),
	"GET /system/sealing-key": pub(), "GET /invites/:token": pub(), "POST /invites/:token/accept": pub(),
	"GET /healthz": pub(), "GET /version": pub(),
	"GET /app": pub(), "GET /app/*path": pub(), "HEAD /app/*path": pub(),
	"GET /": pub(), "GET /admin2/": pub(), "GET /helper2/": pub(), "GET /admin/": pub(), "GET /helper/": pub(),
	"GET /static/*filepath": pub(), "HEAD /static/*filepath": pub(), "GET /static2/*filepath": pub(), "HEAD /static2/*filepath": pub(),
	// вошедший, без дома
	"GET /auth/me": authOnly(), "PATCH /auth/me": authOnly(), "DELETE /auth/me": authOnly(), "POST /auth/password": authOnly(), "POST /auth/email": authOnly(), "GET /auth/email": authOnly(), "DELETE /auth/email": authOnly(),
	"POST /auth/email/resend": authOnly(), "POST /auth/email/check": pub(), "POST /auth/email/confirm": pub(), "POST /auth/email/cancel": pub(), "POST /auth/email/revert": pub(),
	"POST /me/devices": authOnly(), "DELETE /me/devices/:token": authOnly(),
	// дом
	"POST /orgs/:orgId/invites": org(team), "GET /orgs/:orgId/members": org(team),
	"GET /orgs/:orgId/invites": org(team), "DELETE /orgs/:orgId/invites/:id": org(team),
	"POST /auth/password/reset/check":                    pub(),
	"GET /orgs/:orgId/calendar":                          org(all),
	"GET /orgs/:orgId/members/:userId/schedule-settings": org(team), "PATCH /orgs/:orgId/members/:userId/schedule-settings": org(team),
	"GET /orgs/:orgId/members/:userId/day-overrides": org(team), "POST /orgs/:orgId/members/:userId/day-overrides": org(team),
	"DELETE /orgs/:orgId/members/:userId/day-overrides/:id": org(team),
	"GET /orgs/:orgId/task-categories":                      org(all), "POST /orgs/:orgId/task-categories": org(settings),
	"PUT /orgs/:orgId/task-categories/:id": org(settings), "DELETE /orgs/:orgId/task-categories/:id": org(settings),
	"GET /orgs/:orgId/settings": org(all), "PUT /orgs/:orgId/settings": org(settings),
	"GET /orgs/:orgId/notification-prefs": org(all), "PUT /orgs/:orgId/notification-prefs": org(all),
	"POST /orgs/:orgId/notification-prefs/test": org(all),

	"GET /admin/api/recipes": org(all), "GET /admin/api/recipes/:id": org(all),
	"POST /admin/api/recipes": org(recipes), "PUT /admin/api/recipes/:id": org(all), "DELETE /admin/api/recipes/:id": org(recipes),
	"GET /admin/api/recipes/:id/comments": org(all), "POST /admin/api/recipes/:id/comments": org(all),
	"DELETE /admin/api/comments/:id": org(recipes),
	"GET /admin/api/mealtimes":       org(all), "GET /admin/api/mealtimes/:id": org(all),
	"POST /admin/api/mealtimes": org(schedule), "PUT /admin/api/mealtimes/:id": org(schedule), "DELETE /admin/api/mealtimes/:id": org(schedule),
	"GET /admin/api/zones": org(all), "GET /admin/api/zones/:id": org(all),
	"POST /admin/api/zones": org(schedule), "PUT /admin/api/zones/:id": org(schedule), "DELETE /admin/api/zones/:id": org(schedule),
	"GET /admin/api/childcare": org(all), "GET /admin/api/childcare/:id": org(all),
	"POST /admin/api/childcare": org(schedule), "PUT /admin/api/childcare/:id": org(schedule), "DELETE /admin/api/childcare/:id": org(schedule),
	"GET /admin/api/tasks/:id": org(all), "PUT /admin/api/tasks/:id": org(schedule),
	"POST /admin/api/tasks/:id/recipes": org(schedule), "DELETE /admin/api/tasks/:id/recipes/:recipe_id": org(schedule),
	"POST /admin/api/tasks/:id/zones": org(schedule), "DELETE /admin/api/tasks/:id/zones/:zone_id": org(schedule),
	"POST /admin/api/custom-tasks": org(schedule), "DELETE /admin/api/custom-tasks/:id": org(schedule),
	"GET /admin/api/recurring-tasks": org(all), "POST /admin/api/recurring-tasks": org(schedule),
	"DELETE /admin/api/recurring-tasks/:id": org(schedule), "POST /admin/api/regenerate-schedule": org(schedule),

	"GET /helper/api/schedule/today": org(all), "GET /helper/api/schedule/date/:date": org(all),
	"GET /helper/api/schedule/upcoming":   org(all),
	"POST /helper/api/tasks/:id/complete": org(all), "POST /helper/api/tasks/:id/uncomplete": org(all),
	"GET /helper/api/shopping-lists": org(all), "POST /helper/api/shopping-lists": org(all),
	"GET /helper/api/shopping-lists/suggestions": org(all), "GET /helper/api/shopping-lists/:id": org(all),
	"GET /helper/api/shopping-lists/menu": org(all), "POST /helper/api/shopping-lists/from-menu": org(all),
	"DELETE /helper/api/shopping-lists/:id": org(all), "POST /helper/api/shopping-lists/:id/send": org(all),
	"POST /helper/api/shopping-lists/:id/viewed": org(all), "POST /helper/api/shopping-lists/:id/close": org(all),
	"POST /helper/api/shopping-lists/:id/items": org(all), "PATCH /helper/api/shopping-lists/:id/items/:itemId": org(all),
	"DELETE /helper/api/shopping-lists/:id/items/:itemId": org(all),
	"GET /helper/api/recipes/:id":                         org(all),
	"GET /helper/api/childcare/today":                     org(all), "POST /helper/api/childcare/today": org(schedule),
	"DELETE /helper/api/childcare/today": org(schedule),
}

func concrete(path string, orgID uint, userID uint) string {
	r := strings.NewReplacer(
		":orgId", fmt.Sprint(orgID), ":userId", fmt.Sprint(userID), ":date", "2026-11-20",
		":token", "x", ":recipe_id", "999999999", ":zone_id", "999999999", ":itemId", "999999999", ":id", "999999999",
		"*path", "login", "*filepath", "x.css")
	return r.Replace(path)
}

func TestRBACMatrix(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("rbac")
	who := map[string]acct{"owner": o}
	for _, r := range []string{"admin", "manager", "helper"} {
		who[r] = e.member(o, r, "rbac")
	}
	outsider := e.owner("rbac-out")
	if n := len(e.r.Routes()); n < 90 {
		t.Fatalf("маршрутов %d — роутер собран не полностью", n)
	}

	for _, rt := range e.r.Routes() {
		key := rt.Method + " " + rt.Path
		p, ok := routePolicy[key]
		if !ok {
			t.Errorf("маршрут без строки в матрице прав: %s", key)
			continue
		}
		path := concrete(rt.Path, o.Org, who["helper"].UserID)
		anon := e.do(req{method: rt.Method, path: path, org: o.Org, body: map[string]any{}})
		switch p.kind {
		case "public":
			if anon.Code == 401 || anon.Code == 403 {
				t.Errorf("%s без входа: %d, ждали доступ", key, anon.Code)
			}
			continue
		default:
			if anon.Code != 401 {
				t.Errorf("%s без входа: %d, ждали 401", key, anon.Code)
			}
		}
		if p.kind == "auth" || strings.HasPrefix(key, "DELETE /auth/me") {
			continue // проверено в TestTokenRevocation и smoke (удалять аккаунты здесь нельзя)
		}
		for _, role := range all {
			a := who[role]
			w := e.do(req{method: rt.Method, path: path, token: a.Token, org: o.Org, body: map[string]any{}})
			allowed := false
			for _, r := range p.roles {
				allowed = allowed || r == role
			}
			if allowed && (w.Code == 401 || w.Code == 403) {
				t.Errorf("%s для %s: %d, ждали доступ", key, role, w.Code)
			}
			if !allowed && w.Code != 403 {
				t.Errorf("%s для %s: %d, ждали 403", key, role, w.Code)
			}
		}
		w := e.do(req{method: rt.Method, path: path, token: outsider.Token, org: o.Org, body: map[string]any{}})
		if w.Code != 403 {
			t.Errorf("%s из чужого дома: %d, ждали 403", key, w.Code)
		}
	}
}

// SEC-06: дом в адресе /orgs/:orgId должен совпадать с X-Org-Id.
func TestOrgPathMustMatchHeader(t *testing.T) {
	e := newEnv(t, 1000)
	a := e.owner("pathA")
	b := e.owner("pathB")
	e.expect("свой дом", e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/settings", a.Org), token: a.Token, org: a.Org}), 200)
	e.expect("чужой дом в адресе", e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/settings", b.Org), token: a.Token, org: a.Org}), 403)
}
