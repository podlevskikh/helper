package clock

import (
	"testing"
	"time"
)

// DATE-01 / DATE-02: «сегодня» дома — по его поясу, в том числе около полуночи и при переходе на летнее/зимнее время.
func TestOrgTodayAcrossMidnightAndDST(t *testing.T) {
	defer func(old func() time.Time) { Now = old }(Now)
	nicosia, _ := time.LoadLocation(DefaultTZ)
	cases := []struct {
		utc  string
		want string
	}{
		{"2026-11-19T21:30:00Z", "2026-11-19"}, // 23:30 по Никосии (зима, UTC+2)
		{"2026-11-19T22:30:00Z", "2026-11-20"}, // 00:30 по Никосии — уже завтра, хотя в UTC ещё 19-е
		{"2027-03-27T22:30:00Z", "2027-03-28"}, // ночь перехода на летнее время (28.03.2027)
		{"2027-03-28T01:30:00Z", "2027-03-28"}, // 04:30 летнего времени
		{"2027-10-30T21:30:00Z", "2027-10-31"}, // 00:30 летнего, ночь перехода на зимнее (31.10.2027)
		{"2027-10-31T21:59:00Z", "2027-10-31"}, // 23:59 уже зимнего
	}
	for _, c := range cases {
		ts, _ := time.Parse(time.RFC3339, c.utc)
		Now = func() time.Time { return ts }
		got := Day(Now().In(nicosia)).Format("2006-01-02")
		if got != c.want {
			t.Errorf("%s: %s, ждали %s", c.utc, got, c.want)
		}
		if d := Day(Now().In(nicosia)); d.Location() != time.UTC || d.Hour() != 0 {
			t.Errorf("день не полночь UTC: %v", d)
		}
	}
}
