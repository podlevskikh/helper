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

// Welcome — после регистрации нанимателя или входа помощника по приглашению.
func Welcome(to, locale, name, orgName, appURL string, helper bool) Message {
	var title, button, foot string
	var ps []string
	if isRu(locale) {
		title = "Добро пожаловать!"
		if helper {
			ps = []string{fmt.Sprintf("Здравствуйте, %s! Вы присоединились к дому «%s».", name, orgName),
				"Здесь видно, что сделать сегодня: готовка, уборка, ребёнок и поручения. Отмечайте сделанное — наниматель это увидит."}
		} else {
			ps = []string{fmt.Sprintf("Аккаунт для %s создан, дом «%s» готов.", to, orgName),
				"Добавьте помощника из «Настроек», и расписание дня будет у вас обоих."}
		}
		button = "Открыть приложение"
		foot = "Если вы не регистрировались — просто проигнорируйте это письмо."
	} else {
		title = "Welcome!"
		if helper {
			ps = []string{fmt.Sprintf("Hi %s! You've joined “%s”.", name, orgName),
				"Here you'll see what to do today: cooking, cleaning, childcare and errands. Tick off what's done — your employer will see it."}
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
