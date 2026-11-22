package mail

import (
	"fmt"
	"html"
	"strings"
)

// Тексты писем на языке пользователя (ru / en).

func isRu(locale string) bool { return strings.HasPrefix(locale, "ru") }

// layout — простое письмо: заголовок, абзацы, кнопка и мелкая приписка.
func layout(title string, paragraphs []string, button, link, footnote string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;padding:24px;background:#F5EFE6;font-family:-apple-system,Segoe UI,Roboto,Arial,sans-serif;color:#2B2321">`)
	b.WriteString(`<div style="max-width:480px;margin:0 auto;background:#FFFFFF;border-radius:18px;padding:28px">`)
	fmt.Fprintf(&b, `<h1 style="margin:0 0 16px;font-size:22px;color:#65132A">%s</h1>`, html.EscapeString(title))
	for _, p := range paragraphs {
		fmt.Fprintf(&b, `<p style="margin:0 0 12px;font-size:16px;line-height:1.5">%s</p>`, html.EscapeString(p))
	}
	if button != "" && link != "" {
		fmt.Fprintf(&b, `<p style="margin:20px 0"><a href="%s" style="display:inline-block;background:#65132A;color:#FAECD2;text-decoration:none;font-weight:700;padding:14px 22px;border-radius:14px">%s</a></p>`,
			html.EscapeString(link), html.EscapeString(button))
		fmt.Fprintf(&b, `<p style="margin:0 0 12px;font-size:13px;color:#7A6E68;word-break:break-all">%s</p>`, html.EscapeString(link))
	}
	if footnote != "" {
		fmt.Fprintf(&b, `<p style="margin:16px 0 0;font-size:13px;color:#7A6E68">%s</p>`, html.EscapeString(footnote))
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

func plain(title string, paragraphs []string, link, footnote string) string {
	parts := append([]string{title, ""}, paragraphs...)
	if link != "" {
		parts = append(parts, "", link)
	}
	if footnote != "" {
		parts = append(parts, "", footnote)
	}
	return strings.Join(parts, "\n")
}

// Welcome — после регистрации дома или входа помощника по приглашению.
func Welcome(to, locale, name, orgName, appURL string, helper bool) Message {
	var title, button, foot string
	var ps []string
	if isRu(locale) {
		title = "Добро пожаловать!"
		if helper {
			ps = []string{fmt.Sprintf("Здравствуйте, %s! Вы в команде дома «%s».", name, orgName),
				"Здесь видно, что сделать сегодня: готовка, уборка, ребёнок и другие задачи. Отмечайте сделанное — тот, кто ведёт дом, это увидит."}
		} else {
			ps = []string{fmt.Sprintf("Аккаунт для %s создан, дом «%s» готов.", to, orgName),
				"Добавьте помощника из «Настроек», и расписание дня будет у вас обоих."}
		}
		button = "Открыть приложение"
		foot = "Если вы не регистрировались — просто проигнорируйте это письмо."
	} else {
		title = "Welcome!"
		if helper {
			ps = []string{fmt.Sprintf("Hi %s! You're now on the home team of “%s”.", name, orgName),
				"Here you'll see today's tasks: cooking, cleaning, childcare and more. Tick off what's done — whoever runs the home will see it."}
		} else {
			ps = []string{fmt.Sprintf("Your account for %s is ready, and so is “%s”.", to, orgName),
				"Invite your helper from Settings and you'll both share the day's plan."}
		}
		button = "Open the app"
		foot = "If you didn't sign up, just ignore this email."
	}
	return Message{To: to, Subject: title, Text: plain(title, ps, appURL, foot), HTML: layout(title, ps, button, appURL, foot)}
}

// PasswordReset — ссылка на новый пароль.
func PasswordReset(to, locale, link string) Message {
	if isRu(locale) {
		title := "Восстановление пароля"
		ps := []string{"Кто-то (надеемся, вы) попросил сбросить пароль. Нажмите на кнопку и придумайте новый.",
			"Ссылка действует 1 час и срабатывает один раз."}
		foot := "Если вы не просили — ничего не делайте, пароль останется прежним."
		return Message{To: to, Subject: title, Text: plain(title, ps, link, foot), HTML: layout(title, ps, "Задать новый пароль", link, foot)}
	}
	title := "Reset your password"
	ps := []string{"Someone (hopefully you) asked to reset your password. Tap the button and choose a new one.",
		"The link works once and expires in 1 hour."}
	foot := "If it wasn't you, do nothing — your password stays the same."
	return Message{To: to, Subject: title, Text: plain(title, ps, link, foot), HTML: layout(title, ps, "Set a new password", link, foot)}
}

// PasswordChanged — уведомление после смены пароля.
func PasswordChanged(to, locale, appURL string) Message {
	if isRu(locale) {
		title := "Пароль изменён"
		ps := []string{"Пароль от вашего аккаунта только что изменили. На других устройствах нужно будет войти заново."}
		foot := "Если это были не вы — восстановите пароль на странице входа («Забыли пароль?»)."
		return Message{To: to, Subject: title, Text: plain(title, ps, appURL, foot), HTML: layout(title, ps, "Открыть приложение", appURL, foot)}
	}
	title := "Your password was changed"
	ps := []string{"The password for your account was just changed. Other devices will need to sign in again."}
	foot := "If it wasn't you, reset your password on the sign-in page (“Forgot password?”)."
	return Message{To: to, Subject: title, Text: plain(title, ps, appURL, foot), HTML: layout(title, ps, "Open the app", appURL, foot)}
}

// EmailChangeConfirm — на НОВЫЙ адрес: подтвердить, что почта ваша.
func EmailChangeConfirm(to, locale, link string) Message {
	if isRu(locale) {
		title := "Подтвердите новую почту"
		ps := []string{fmt.Sprintf("В Helper's Day попросили сделать %s почтой для входа. Нажмите на кнопку, чтобы подтвердить.", to),
			"Ссылка действует 24 часа и срабатывает один раз. До подтверждения входить нужно со старой почтой."}
		foot := "Если вы ничего не просили — просто проигнорируйте письмо, ничего не изменится."
		return Message{To: to, Subject: title, Text: plain(title, ps, link, foot), HTML: layout(title, ps, "Подтвердить почту", link, foot)}
	}
	title := "Confirm your new email"
	ps := []string{fmt.Sprintf("Someone asked to make %s the sign-in email for a Helper's Day account. Tap the button to confirm.", to),
		"The link works once and expires in 24 hours. Until then, keep signing in with the old email."}
	foot := "If you didn't ask for this, just ignore this email — nothing will change."
	return Message{To: to, Subject: title, Text: plain(title, ps, link, foot), HTML: layout(title, ps, "Confirm email", link, foot)}
}

// EmailChangeTaken — на новый адрес, если он уже занят другим аккаунтом. Ответ API при этом такой же,
// как обычно, — по нему нельзя узнать, зарегистрирован ли адрес.
func EmailChangeTaken(to, locale, appURL string) Message {
	if isRu(locale) {
		title := "Эта почта уже используется"
		ps := []string{fmt.Sprintf("В Helper's Day попросили сделать %s почтой для входа в другой аккаунт, но с этой почтой уже есть аккаунт.", to),
			"Если это были вы — войдите с этой почтой или укажите в профиле другой адрес."}
		foot := "Если вы ничего не просили — просто проигнорируйте письмо."
		return Message{To: to, Subject: title, Text: plain(title, ps, appURL, foot), HTML: layout(title, ps, "Открыть приложение", appURL, foot)}
	}
	title := "This email is already in use"
	ps := []string{fmt.Sprintf("Someone asked to use %s as the sign-in email for a Helper's Day account, but there's already an account with this email.", to),
		"If it was you, sign in with this email or choose another address in your profile."}
	foot := "If you didn't ask for this, just ignore this email."
	return Message{To: to, Subject: title, Text: plain(title, ps, appURL, foot), HTML: layout(title, ps, "Open the app", appURL, foot)}
}

// EmailChangeRequested — на СТАРЫЙ адрес: запрошена смена, ссылка «это не я».
func EmailChangeRequested(to, locale, maskedNew, cancelLink string) Message {
	if isRu(locale) {
		title := "Запрошена смена почты"
		ps := []string{fmt.Sprintf("В вашем аккаунте Helper's Day запросили смену почты для входа на %s.", maskedNew),
			"Почта изменится, только когда по ссылке из письма на новый адрес перейдут в течение 24 часов."}
		foot := "Если это были вы — ничего делать не нужно."
		return Message{To: to, Subject: title, Text: plain(title, append(ps, "Это не вы? Отмените смену и смените пароль:"), cancelLink, foot),
			HTML: layout(title, append(ps, "Это не вы? Отмените смену — все входы в аккаунт будут завершены, затем смените пароль."), "Это не я — отменить", cancelLink, foot)}
	}
	title := "Email change requested"
	ps := []string{fmt.Sprintf("Someone asked to change the sign-in email of your Helper's Day account to %s.", maskedNew),
		"It will only change once the link sent to the new address is opened within 24 hours."}
	foot := "If it was you, there's nothing to do."
	return Message{To: to, Subject: title, Text: plain(title, append(ps, "Not you? Cancel the change and change your password:"), cancelLink, foot),
		HTML: layout(title, append(ps, "Not you? Cancel the change — every session will be signed out — then change your password."), "Not me — cancel", cancelLink, foot)}
}

// EmailChanged — на СТАРЫЙ адрес после подтверждения; revertLink — «это не я — вернуть почту» (14 дней).
func EmailChanged(to, locale, maskedNew, revertLink string) Message {
	if isRu(locale) {
		title := "Почта для входа изменена"
		ps := []string{fmt.Sprintf("Почта для входа в ваш аккаунт Helper's Day изменена на %s. На всех устройствах нужно войти заново.", maskedNew),
			"Это не вы? Верните почту по кнопке ниже в течение 14 дней — все входы будут завершены, а новый пароль вы зададите через «Забыли пароль?»."}
		foot := "Если это были вы — ничего делать не нужно."
		return Message{To: to, Subject: title, Text: plain(title, ps, revertLink, foot), HTML: layout(title, ps, "Это не я — вернуть почту", revertLink, foot)}
	}
	title := "Your sign-in email was changed"
	ps := []string{fmt.Sprintf("The sign-in email of your Helper's Day account was changed to %s. Every device will need to sign in again.", maskedNew),
		"Not you? Use the button below within 14 days to get your email back — every session will be signed out, and you'll set a new password via “Forgot password?”."}
	foot := "If it was you, there's nothing to do."
	return Message{To: to, Subject: title, Text: plain(title, ps, revertLink, foot), HTML: layout(title, ps, "Not me — restore my email", revertLink, foot)}
}

// MaskEmail: nikita@example.com → n***@example.com (в письмах на другой адрес полный адрес не показываем).
func MaskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 1 {
		return "***"
	}
	r := []rune(email[:at])
	return string(r[0]) + "***" + email[at:]
}
