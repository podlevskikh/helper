package calendar

import (
	"strings"
	"testing"
)

const sampleICS = "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20261225\r\nDTEND;VALUE=DATE:20261226\r\nDESCRIPTION:Public holiday\r\nSUMMARY:Christmas Day\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20261224\r\nDTEND;VALUE=DATE:20261225\r\nDESCRIPTION:Observance\\nTo hide observances\\, go to Google Calendar Setting\r\n s > Holidays\r\nSUMMARY:Christmas Eve\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20270216\r\nDTEND;VALUE=DATE:20270219\r\nDESCRIPTION:Public holiday\r\nSUMMARY:Tet\\, holiday\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestParseICS(t *testing.T) {
	list, err := parseICS(strings.NewReader(sampleICS), "CY")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range list {
		got = append(got, h.Date.Format("2006-01-02")+" "+h.Name)
	}
	want := []string{"2026-12-25 Christmas Day", "2027-02-16 Tet, holiday", "2027-02-17 Tet, holiday", "2027-02-18 Tet, holiday"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDaysOffRoundTrip(t *testing.T) {
	if s := FormatDaysOff([]int{7, 6, 6}); s != "6,7" {
		t.Fatalf("FormatDaysOff = %q", s)
	}
	m := ParseDaysOff("6, 7,9")
	if !m[6] || !m[7] || m[9] || len(m) != 2 {
		t.Fatalf("ParseDaysOff = %v", m)
	}
}
