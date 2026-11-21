package calendar

// Умолчания по часовому поясу дома: с какого дня начинается неделя, какой выходной у нового помощника
// и праздники какой страны ему ставить. Часовой пояс приходит с устройства при регистрации.
// Залив и Иордания: неделя с воскресенья, выходной помощника — пятница. ОАЭ: неделя с понедельника,
// выходные суббота–воскресенье. Остальные — неделя с понедельника, выходной в воскресенье.

// ISO-дни недели: 1 = Пн … 7 = Вс.
const (
	Monday   = 1
	Friday   = 5
	Saturday = 6
	Sunday   = 7
)

// regions — часовой пояс → страна праздников, первый день недели, выходной помощника по умолчанию.
var regions = map[string]struct {
	country   string
	weekStart int
	dayOff    int
}{
	"Asia/Riyadh":    {"SA", Sunday, Friday},
	"Asia/Kuwait":    {"KW", Sunday, Friday},
	"Asia/Qatar":     {"QA", Sunday, Friday},
	"Asia/Bahrain":   {"BH", Sunday, Friday},
	"Asia/Muscat":    {"OM", Sunday, Friday},
	"Asia/Amman":     {"JO", Sunday, Friday},
	"Asia/Dubai":     {"AE", Monday, Sunday},
	"Asia/Beirut":    {"LB", Monday, Sunday},
	"Asia/Nicosia":   {"CY", Monday, Sunday},
	"Europe/Nicosia": {"CY", Monday, Sunday},
	"Asia/Famagusta": {"CY", Monday, Sunday},
}

// WeekStartFor — первый день недели по часовому поясу (ISO), по умолчанию понедельник.
func WeekStartFor(tz string) int {
	if r, ok := regions[tz]; ok {
		return r.weekStart
	}
	return Monday
}

// DefaultDaysOff — выходной нового помощника по часовому поясу дома ("5" — пятница, "7" — воскресенье).
func DefaultDaysOff(tz string) string {
	if r, ok := regions[tz]; ok && r.dayOff == Friday {
		return "5"
	}
	return "7"
}

// DefaultCountryFor — страна праздников нового помощника по часовому поясу дома (иначе DefaultCountry).
func DefaultCountryFor(tz string) string {
	if r, ok := regions[tz]; ok && Supported(r.country) {
		return r.country
	}
	return DefaultCountry
}
