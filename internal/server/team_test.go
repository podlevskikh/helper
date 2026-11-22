package server

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/models"
)

// Команда дома и роли: ROLE-11…ROLE-16 (docs/qa/test-cases.md во фронте).

func memberPath(o acct, userID uint) string {
	return fmt.Sprintf("/orgs/%d/members/%d", o.Org, userID)
}

// ROLE-16: имя из приглашения становится именем человека в команде; роль owner не пригласить.
func TestInviteNameBecomesTeamName(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("tname")
	w := e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org,
		body: map[string]string{"role": "helper", "name": "  Бабушка  "}})
	e.expect("приглашение с именем", w, 201)
	token, _ := e.json(w)["token"].(string)
	g := e.do(req{method: "GET", path: "/invites/" + token})
	if e.json(g)["name"] != "Бабушка" {
		t.Errorf("GET /invites: name = %v", e.json(g)["name"])
	}
	e.expect("принять", e.do(req{method: "POST", path: "/invites/" + token + "/accept", body: map[string]string{
		"name": "Галина", "password": "member-pass-1", "email": "gran-" + uniq() + "@test.local"}}), 200)
	list := e.jsonList(e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/members", o.Org), token: o.Token, org: o.Org}))
	found := false
	for _, m := range list {
		if m["display_name"] == "Бабушка" {
			found = true
			if m["name"] != "Бабушка" || m["account_name"] != "Галина" {
				t.Errorf("имя в команде: %v / %v", m["name"], m["account_name"])
			}
		}
	}
	if !found {
		t.Error("в команде нет «Бабушки»")
	}
	for _, role := range []string{"owner", "boss", ""} {
		e.expect("приглашение с ролью "+role, e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org,
			body: map[string]string{"role": role}}), 400)
	}
}

// ROLE-11: смена роли и имени; основного админа не понизить, свою роль не поменять, owner не выдать.
func TestUpdateMemberRole(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("trole")
	a := e.member(o, "admin", "trole")
	h := e.member(o, "helper", "trole")
	mgr := e.member(o, "manager", "trole")

	w := e.do(req{method: "PATCH", path: memberPath(o, h.UserID), token: a.Token, org: o.Org, body: map[string]string{"role": "manager", "name": "Мария"}})
	e.expect("админ делает помощника модератором", w, 200)
	if m := e.json(w); m["role"] != "manager" || m["name"] != "Мария" {
		t.Errorf("ответ: %v", m)
	}
	e.expect("пустое тело", e.do(req{method: "PATCH", path: memberPath(o, h.UserID), token: o.Token, org: o.Org, body: map[string]string{}}), 400)
	e.expect("роль owner не выдаётся", e.do(req{method: "PATCH", path: memberPath(o, h.UserID), token: o.Token, org: o.Org, body: map[string]string{"role": "owner"}}), 400)
	e.expect("основного админа не понизить", e.do(req{method: "PATCH", path: memberPath(o, o.UserID), token: a.Token, org: o.Org, body: map[string]string{"role": "helper"}}), 403)
	e.expect("имя основного админа можно", e.do(req{method: "PATCH", path: memberPath(o, o.UserID), token: a.Token, org: o.Org, body: map[string]string{"name": "Анна"}}), 200)
	e.expect("свою роль не поменять", e.do(req{method: "PATCH", path: memberPath(o, a.UserID), token: a.Token, org: o.Org, body: map[string]string{"role": "helper"}}), 400)
	e.expect("модератор не меняет роли", e.do(req{method: "PATCH", path: memberPath(o, h.UserID), token: mgr.Token, org: o.Org, body: map[string]string{"role": "helper"}}), 403)
	e.expect("невидимые символы в имени", e.do(req{method: "PATCH", path: memberPath(o, h.UserID), token: o.Token, org: o.Org, body: map[string]string{"name": "Ма\u0000рия"}}), 400)
	e.expect("смена направления текста в имени", e.do(req{method: "PATCH", path: memberPath(o, h.UserID), token: o.Token, org: o.Org, body: map[string]string{"name": "Анна\u202e"}}), 400)
	e.expect("не участник — 404", e.do(req{method: "PATCH", path: memberPath(o, 999999999), token: o.Token, org: o.Org, body: map[string]string{"role": "helper"}}), 404)

	// Права меняются сразу: бывший помощник-модератор ставит задачи, бывший админ — нет.
	e.expect("понизить админа", e.do(req{method: "PATCH", path: memberPath(o, a.UserID), token: o.Token, org: o.Org, body: map[string]string{"role": "helper"}}), 200)
	e.expect("бывший админ без команды", e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/members", o.Org), token: a.Token, org: o.Org}), 403)
}

// ROLE-13: убрать из команды и покинуть команду; основной админ не уходит и не убирается.
func TestRemoveAndLeave(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("tleave")
	a := e.member(o, "admin", "tleave")
	h := e.member(o, "helper", "tleave")
	mgr := e.member(o, "manager", "tleave")

	rt := models.RecurringTask{OrganizationID: o.Org, Title: "Пыль", Weekdays: "1", AssignedToUserID: &h.UserID}
	if err := e.db.Create(&rt).Error; err != nil {
		t.Fatal(err)
	}

	e.expect("модератор не убирает", e.do(req{method: "DELETE", path: memberPath(o, h.UserID), token: mgr.Token, org: o.Org}), 403)
	e.expect("помощник не убирает", e.do(req{method: "DELETE", path: memberPath(o, mgr.UserID), token: h.Token, org: o.Org}), 403)
	e.expect("основного админа не убрать", e.do(req{method: "DELETE", path: memberPath(o, o.UserID), token: a.Token, org: o.Org}), 403)
	e.expect("себя — через «покинуть»", e.do(req{method: "DELETE", path: memberPath(o, a.UserID), token: a.Token, org: o.Org}), 400)
	e.expect("основной админ не покидает", e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/leave", o.Org), token: o.Token, org: o.Org}), 409)

	e.expect("админ убирает помощника", e.do(req{method: "DELETE", path: memberPath(o, h.UserID), token: a.Token, org: o.Org}), 204)
	e.expect("убранный не видит дом", e.do(req{method: "GET", path: "/helper/api/schedule/today", token: h.Token, org: o.Org}), 403)
	var after models.RecurringTask
	e.db.First(&after, rt.ID)
	if after.AssignedToUserID != nil {
		t.Error("регулярная задача осталась на убранном человеке")
	}
	var u models.User
	if e.db.First(&u, h.UserID).Error != nil {
		t.Error("аккаунт убранного удалён")
	}

	// Ссылка, созданная до того, как человека убрали, его не возвращает; его собственные ссылки отозваны.
	old := e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: a.Token, org: o.Org, body: map[string]string{"role": "admin"}})
	oldTok, _ := e.json(old)["token"].(string)
	e.expect("основной админ убирает админа", e.do(req{method: "DELETE", path: memberPath(o, a.UserID), token: o.Token, org: o.Org}), 204)
	e.expect("своя старая ссылка не возвращает", e.do(req{method: "POST", path: "/invites/" + oldTok + "/accept", body: map[string]string{"name": "x", "password": a.Password, "email": a.Email}}), 410)
	e.expect("после неё дома нет", e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/members", o.Org), token: a.Token, org: o.Org}), 403)

	e.expect("модератор покидает команду", e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/leave", o.Org), token: mgr.Token, org: o.Org}), 204)
	e.expect("после выхода дома нет", e.do(req{method: "GET", path: "/helper/api/schedule/today", token: mgr.Token, org: o.Org}), 403)

	// Вернуть можно новой ссылкой — с новой ролью и именем.
	w := e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/invites", o.Org), token: o.Token, org: o.Org, body: map[string]string{"role": "admin", "name": "Ира", "email": mgr.Email}})
	tok, _ := e.json(w)["token"].(string)
	e.expect("вернулся по ссылке", e.do(req{method: "POST", path: "/invites/" + tok + "/accept", body: map[string]string{"name": "x", "password": mgr.Password}}), 200)
	var m models.Membership
	e.db.Where("user_id = ? AND organization_id = ?", mgr.UserID, o.Org).First(&m)
	if m.Status != models.MembershipActive || m.Role != models.RoleAdmin || m.DisplayName != "Ира" {
		t.Errorf("после возвращения: %s %s %q", m.Status, m.Role, m.DisplayName)
	}
}

// ROLE-14: передача роли основного админа — только админу и с паролем.
func TestTransferOwnership(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("ttrans")
	a := e.member(o, "admin", "ttrans")
	h := e.member(o, "helper", "ttrans")
	path := fmt.Sprintf("/orgs/%d/transfer-ownership", o.Org)

	e.expect("админ не передаёт", e.do(req{method: "POST", path: path, token: a.Token, org: o.Org, body: map[string]any{"user_id": a.UserID, "password": a.Password}}), 403)
	e.expect("неверный пароль", e.do(req{method: "POST", path: path, token: o.Token, org: o.Org, body: map[string]any{"user_id": a.UserID, "password": "nope-nope"}}), 403)
	e.expect("помощнику нельзя", e.do(req{method: "POST", path: path, token: o.Token, org: o.Org, body: map[string]any{"user_id": h.UserID, "password": o.Password}}), 400)
	e.expect("себе нельзя", e.do(req{method: "POST", path: path, token: o.Token, org: o.Org, body: map[string]any{"user_id": o.UserID, "password": o.Password}}), 400)
	e.expect("передать админу", e.do(req{method: "POST", path: path, token: o.Token, org: o.Org, body: map[string]any{"user_id": a.UserID, "password": o.Password}}), 204)

	role := func(uid uint) models.Role {
		var m models.Membership
		e.db.Where("user_id = ? AND organization_id = ?", uid, o.Org).First(&m)
		return m.Role
	}
	if role(a.UserID) != models.RoleOwner || role(o.UserID) != models.RoleAdmin {
		t.Errorf("после передачи: новый %s, прежний %s", role(a.UserID), role(o.UserID))
	}
	e.expect("прежний теперь может покинуть команду", e.do(req{method: "POST", path: fmt.Sprintf("/orgs/%d/leave", o.Org), token: o.Token, org: o.Org}), 204)
}

// ROLE-12: удаление аккаунта основным админом — решение про дом.
func TestDeleteAccountHomeDecision(t *testing.T) {
	e := newEnv(t, 1000)

	t.Run("есть админ — передать", func(t *testing.T) {
		o := e.owner("tdel1")
		a := e.member(o, "admin", "tdel1")
		e.member(o, "helper", "tdel1")
		w := e.do(req{method: "DELETE", path: "/auth/me", token: o.Token, body: map[string]any{"password": o.Password}})
		e.expect("без решения", w, 409)
		homes, _ := e.json(w)["homes"].([]any)
		if len(homes) != 1 {
			t.Fatalf("homes: %v", e.json(w))
		}
		admins, _ := homes[0].(map[string]any)["admins"].([]any)
		if len(admins) != 1 || uint(admins[0].(map[string]any)["user_id"].(float64)) != a.UserID {
			t.Errorf("admins: %v", admins)
		}
		e.expect("преемник не админ", e.do(req{method: "DELETE", path: "/auth/me", token: o.Token, body: map[string]any{"password": o.Password, "successor_id": 999999999}}), 409)
		e.expect("передать и удалить", e.do(req{method: "DELETE", path: "/auth/me", token: o.Token, body: map[string]any{"password": o.Password, "successor_id": a.UserID}}), 204)
		var org models.Organization
		if e.db.First(&org, o.Org).Error != nil {
			t.Fatal("дом удалён, хотя его передали")
		}
		var m models.Membership
		e.db.Where("user_id = ? AND organization_id = ?", a.UserID, o.Org).First(&m)
		if m.Role != models.RoleOwner {
			t.Errorf("новый основной админ: %s", m.Role)
		}
	})

	t.Run("админов нет — удалить дом", func(t *testing.T) {
		o := e.owner("tdel2")
		h := e.member(o, "helper", "tdel2")
		w := e.do(req{method: "DELETE", path: "/auth/me", token: o.Token, body: map[string]any{"password": o.Password}})
		e.expect("без решения", w, 409)
		e.expect("удалить дом", e.do(req{method: "DELETE", path: "/auth/me", token: o.Token, body: map[string]any{"password": o.Password, "delete_home": true}}), 204)
		var org models.Organization
		if e.db.First(&org, o.Org).Error == nil {
			t.Error("дом остался")
		}
		var u models.User
		if e.db.First(&u, h.UserID).Error != nil {
			t.Error("аккаунт помощника удалён вместе с домом")
		}
	})

	t.Run("один в доме", func(t *testing.T) {
		o := e.owner("tdel3")
		e.expect("удалить", e.do(req{method: "DELETE", path: "/auth/me", token: o.Token, body: map[string]any{"password": o.Password}}), 204)
		var org models.Organization
		if e.db.First(&org, o.Org).Error == nil {
			t.Error("дом остался")
		}
	})
}

// ROLE-15: рабочие дни дома — по помощникам и модераторам, админы не в счёт; ROLE-10: настройки дома — всем админам.
func TestWorkdaysAndAdminSettings(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("twork")
	a := e.member(o, "admin", "twork")
	h := e.member(o, "helper", "twork")
	e.db.Model(&models.Membership{}).Where("user_id = ? AND organization_id = ?", h.UserID, o.Org).Update("days_off", "6,7")
	e.db.Model(&models.Membership{}).Where("user_id = ? AND organization_id = ?", a.UserID, o.Org).Update("days_off", "")
	if got := calendar.OrgWorkdays(e.db, o.Org); !slices.Equal(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("рабочие дни %v, ждали Пн–Пт (админ без выходных не в счёт)", got)
	}
	e.expect("админ меняет настройки дома", e.do(req{method: "PUT", path: fmt.Sprintf("/orgs/%d/settings", o.Org), token: a.Token, org: o.Org, body: map[string]any{}}), 200)
}

// ROLE-17: модератор видит команду — имена и роли, без почты (ставить задачи людям, «Передать»).
func TestModeratorSeesTeamNamesOnly(t *testing.T) {
	e := newEnv(t, 1000)
	o := e.owner("modteam")
	h := e.member(o, "helper", "modteam")
	mgr := e.member(o, "manager", "modteam")
	w := e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/members", o.Org), token: mgr.Token, org: o.Org})
	e.expect("модератор: список команды", w, 200)
	var list []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("ответ: %v", err)
	}
	seen := false
	for _, m := range list {
		if m["email"] != "" {
			t.Errorf("модератор видит почту: %v", m)
		}
		if m["user_id"] == float64(h.UserID) && m["role"] == "helper" && m["name"] != "" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("модератор не видит помощника с именем и ролью: %v", list)
	}
	// Владелец по-прежнему видит почту.
	var full []map[string]any
	_ = json.Unmarshal(e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/members", o.Org), token: o.Token, org: o.Org}).Body.Bytes(), &full)
	if len(full) == 0 || full[0]["email"] == "" {
		t.Errorf("владелец не видит почту: %v", full)
	}
	// Менять команду модератор по-прежнему не может.
	e.expect("модератор: смена роли", e.do(req{method: "PATCH", path: fmt.Sprintf("/orgs/%d/members/%d", o.Org, h.UserID),
		token: mgr.Token, org: o.Org, body: map[string]string{"role": "manager"}}), 403)
}
