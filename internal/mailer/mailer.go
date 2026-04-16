package mailer

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"net/url"
)

// Send sends a plain-text email.
//
// smtpURL formats:
//
//	smtp://user:pass@host:587   — STARTTLS (recommended)
//	smtps://user:pass@host:465  — direct TLS
//	smtp://host:25              — unauthenticated relay
func Send(smtpURL, from, to, subject, body string) error {
	u, err := url.Parse(smtpURL)
	if err != nil {
		return fmt.Errorf("invalid SMTP URL: %w", err)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "smtps" {
			port = "465"
		} else {
			port = "587"
		}
	}
	addr := net.JoinHostPort(host, port)

	msg := buildMessage(from, to, subject, body)

	var auth smtp.Auth
	if u.User != nil {
		user := u.User.Username()
		pass, _ := u.User.Password()
		auth = smtp.PlainAuth("", user, pass, host)
	}

	if u.Scheme == "smtps" {
		return sendTLS(addr, host, auth, from, to, msg)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, msg)
}

func sendTLS(addr, host string, auth smtp.Auth, from, to string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("TLS dial %s: %w", addr, err)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP client: %w", err)
	}
	defer c.Close() //nolint:errcheck

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err = wc.Write(msg); err != nil {
		return fmt.Errorf("SMTP write: %w", err)
	}
	return wc.Close()
}

func buildMessage(from, to, subject, body string) []byte {
	return []byte(
		"From: " + from + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"\r\n" +
			body,
	)
}
