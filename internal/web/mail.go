package web

import (
	"errors"
	"net"
	"net/smtp"
)

// MailConfig holds outgoing SMTP settings, wired from flags/env vars in
// cmd/labdoc (see SetMail). LabDoc never requires email to run: leaving Host
// blank simply means email verification and "forgot password" report a
// friendly "not configured" error instead of silently doing nothing.
//
// This talks SMTP directly via the standard library (no third-party mail
// dependency), with opportunistic STARTTLS on the usual submission port
// (587) — the same thing net/smtp.SendMail does. Implicit TLS on port 465 is
// not supported; point Port at a 587/STARTTLS submission endpoint.
type MailConfig struct {
	Host, Port, Username, Password, From string
}

func (c MailConfig) configured() bool { return c.Host != "" && c.From != "" }

// send sends a single plain-text email. Kept deliberately minimal (no HTML,
// no attachments, no connection reuse) since this only ever fires for a
// low-volume, one-off notification (a verification or reset link).
func (c MailConfig) send(to, subject, body string) error {
	if !c.configured() {
		return errors.New("outgoing email is not configured on this server")
	}
	var auth smtp.Auth
	if c.Username != "" {
		auth = smtp.PlainAuth("", c.Username, c.Password, c.Host)
	}
	msg := "From: " + c.From + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"utf-8\"\r\n\r\n" +
		body + "\r\n"
	return smtp.SendMail(net.JoinHostPort(c.Host, c.Port), auth, c.From, []string{to}, []byte(msg))
}
