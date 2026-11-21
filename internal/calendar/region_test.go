package calendar

import "testing"

func TestRegion(t *testing.T) {
	cases := []struct {
		tz      string
		week    int
		off     string
		country string
	}{
		{"Asia/Riyadh", Sunday, "5", "SA"},
		{"Asia/Kuwait", Sunday, "5", "KW"},
		{"Asia/Amman", Sunday, "5", "JO"},
		{"Asia/Dubai", Monday, "7", "AE"},
		{"Asia/Beirut", Monday, "7", "LB"},
		{"Europe/Nicosia", Monday, "7", "CY"},
		{"Europe/Moscow", Monday, "7", "CY"},
		{"", Monday, "7", "CY"},
	}
	for _, c := range cases {
		if WeekStartFor(c.tz) != c.week || DefaultDaysOff(c.tz) != c.off || DefaultCountryFor(c.tz) != c.country {
			t.Errorf("%q: %d %q %q", c.tz, WeekStartFor(c.tz), DefaultDaysOff(c.tz), DefaultCountryFor(c.tz))
		}
	}
	for _, c := range []string{"SA", "AE", "KW", "QA", "OM", "BH", "JO", "LB"} {
		if !Supported(c) {
			t.Errorf("страна праздников %s не поддерживается", c)
		}
	}
}
