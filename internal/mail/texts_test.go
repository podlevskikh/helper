package mail

import (
	"regexp"
	"testing"
)

// UI-08 (сервер): письма — по словарю бренда docs/brand/voice.md фронта.
func TestTextsFollowBrandVocabulary(t *testing.T) {
	bad := regexp.MustCompile(`(?i)нанимател|работодател|сотрудни|персонал|поручени|прислуг|хозяйк|\bemployers?\b|\bstaff\b|\bmaids?\b|\berrands?\b|Helper Day`)
	var msgs []Message
	for _, loc := range []string{"ru", "en"} {
		msgs = append(msgs,
			Welcome("a@b.c", loc, "Мария", "Дом", "https://x", true),
			Welcome("a@b.c", loc, "Анна", "Дом", "https://x", false),
			PasswordReset("a@b.c", loc, "https://x"),
			PasswordChanged("a@b.c", loc, "https://x"),
			EmailChangeConfirm("a@b.c", loc, "https://x"),
			EmailChangeTaken("a@b.c", loc, "https://x"),
		)
	}
	for _, m := range msgs {
		if w := bad.FindString(m.Subject + "\n" + m.Text); w != "" {
			t.Errorf("«%s» — не по словарю бренда: %s", w, m.Subject)
		}
	}
}
