package server

import (
	"fmt"
	"testing"

	"podlevskikh/awesomeProject/internal/models"
)

// LANG-11: часовой пояс устройства при регистрации становится поясом дома; по нему — первый день недели
// и выходной со страной праздников у нового помощника (Залив: воскресенье, пятница, праздники страны).
func TestRegionDefaults(t *testing.T) {
	e := newEnv(t, 100)
	email := "own-gulf-" + uniq() + "@test.local"
	pw := "owner-pass-" + uniq()
	w := e.do(req{method: "POST", path: "/auth/register", body: map[string]string{
		"email": email, "password": pw, "org_name": "Gulf", "locale": "ar", "timezone": "Asia/Riyadh"}})
	e.expect("регистрация с поясом", w, 201)
	o := authAcct(e, e.json(w), email, pw)

	path := fmt.Sprintf("/orgs/%d/settings", o.Org)
	s := e.json(e.do(req{method: "GET", path: path, token: o.Token, org: o.Org}))
	if s["timezone"] != "Asia/Riyadh" || s["week_start"] != 7.0 || s["week_start_auto"] != true {
		t.Errorf("настройки дома в Эр-Рияде: %v", s)
	}
	h := e.member(o, "helper", "gulf")
	var m models.Membership
	e.db.Where("user_id = ? AND organization_id = ?", h.UserID, o.Org).First(&m)
	if m.DaysOff != "5" || m.HolidayCountry != "SA" {
		t.Errorf("помощник в Эр-Рияде: выходной %q, праздники %q", m.DaysOff, m.HolidayCountry)
	}

	e.expect("неделя с субботы", e.do(req{method: "PUT", path: path, token: o.Token, org: o.Org, body: map[string]any{"week_start": 6}}), 200)
	if s := e.json(e.do(req{method: "GET", path: path, token: o.Token, org: o.Org})); s["week_start"] != 6.0 || s["week_start_auto"] != false {
		t.Errorf("после выбора субботы: %v", s)
	}
	e.expect("среда — нельзя", e.do(req{method: "PUT", path: path, token: o.Token, org: o.Org, body: map[string]any{"week_start": 3}}), 400)

	// Без пояса и с незнакомым поясом — как раньше: Кипр, понедельник, воскресенье.
	c := e.owner("cy")
	cs := e.json(e.do(req{method: "GET", path: fmt.Sprintf("/orgs/%d/settings", c.Org), token: c.Token, org: c.Org}))
	if cs["timezone"] != "Europe/Nicosia" || cs["week_start"] != 1.0 {
		t.Errorf("дом без пояса: %v", cs)
	}
	ch := e.member(c, "helper", "cy")
	var cm models.Membership
	e.db.Where("user_id = ? AND organization_id = ?", ch.UserID, c.Org).First(&cm)
	if cm.DaysOff != "7" || cm.HolidayCountry != "CY" {
		t.Errorf("помощник на Кипре: %q %q", cm.DaysOff, cm.HolidayCountry)
	}
	w = e.do(req{method: "POST", path: "/auth/register", body: map[string]string{
		"email": "own-bad-" + uniq() + "@test.local", "password": pw, "org_name": "X", "timezone": "Mars/Base"}})
	e.expect("незнакомый пояс не мешает регистрации", w, 201)
}
