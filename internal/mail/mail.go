// Package mail — отправка писем через SMTP (например, Gmail с паролем приложения).
//
// Настройки — из окружения: SMTP_USER, SMTP_PASSWORD (обязательно), SMTP_HOST, SMTP_PORT,
// MAIL_FROM, MAIL_FROM_NAME (необязательно). Если окружение задать нельзя, те же значения
// можно положить в secrets.sealed.json (см. scripts/seal-mail.sh) — их расшифровывает только сервер.
// Пока почта не настроена, письма не уходят, а в лог пишется, что было бы отправлено.
package mail

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

var ErrNotConfigured = errors.New("mail: SMTP не настроен")

type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type config struct {
	host, port, user, pass, from, fromName string
}

func loadConfig() (config, bool) {
	c := config{
		host:     os.Getenv("SMTP_HOST"),
		port:     os.Getenv("SMTP_PORT"),
		user:     strings.TrimSpace(os.Getenv("SMTP_USER")),
		pass:     strings.ReplaceAll(os.Getenv("SMTP_PASSWORD"), " ", ""), // пароль приложения Gmail показывают с пробелами
		from:     os.Getenv("MAIL_FROM"),
		fromName: os.Getenv("MAIL_FROM_NAME"),
	}
	if c.user == "" || c.pass == "" {
		return c, false
	}
	if c.host == "" {
		c.host = "smtp.gmail.com"
	}
	if c.port == "" {
		c.port = "587"
	}
	if c.from == "" {
		c.from = c.user
	}
	if c.fromName == "" {
		c.fromName = "Helper"
	}
	return c, true
}

// Configured — настроена ли отправка.
func Configured() bool {
	_, ok := loadConfig()
	return ok
}

// SendAsync отправляет письмо в фоне: ответ API не ждёт почтовый сервер.
func SendAsync(m Message) {
	go func() {
		if err := Send(m); err != nil {
			if errors.Is(err, ErrNotConfigured) {
				log.Printf("mail: почта не настроена — письмо «%s» для %s не отправлено", m.Subject, m.To)
				if os.Getenv("MAIL_DEV_LOG") == "1" { // только для локальной разработки: ссылки из писем в лог
					log.Printf("mail (dev):\n%s", m.Text)
				}
				return
			}
			log.Printf("mail: не удалось отправить «%s» для %s: %v", m.Subject, m.To, err)
		}
	}()
}

func Send(m Message) error {
	c, ok := loadConfig()
	if !ok {
		return ErrNotConfigured
	}
	msg := build(c, m)
	addr := net.JoinHostPort(c.host, c.port)
	auth := smtp.PlainAuth("", c.user, c.pass, c.host)
	if c.port == "465" { // TLS сразу
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{ServerName: c.host})
		if err != nil {
			return err
		}
		cl, err := smtp.NewClient(conn, c.host)
		if err != nil {
			return err
		}
		defer cl.Close()
		if err := cl.Auth(auth); err != nil {
			return err
		}
		if err := cl.Mail(c.from); err != nil {
			return err
		}
		if err := cl.Rcpt(m.To); err != nil {
			return err
		}
		w, err := cl.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write(msg); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return cl.Quit()
	}
	return smtp.SendMail(addr, auth, c.from, []string{m.To}, msg) // 587: STARTTLS
}

func build(c config, m Message) []byte {
	var b bytes.Buffer
	boundary := randomHex(12)
	fmt.Fprintf(&b, "From: %s <%s>\r\n", mime.QEncoding.Encode("utf-8", c.fromName), c.from)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", randomHex(16), domainOf(c.from))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", boundary, crlf(m.Text))
	if m.HTML != "" {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", boundary, crlf(m.HTML))
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

func domainOf(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return email[i+1:]
	}
	return "localhost"
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
