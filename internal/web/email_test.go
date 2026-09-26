package web

import (
	"database/sql"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"labdoc/internal/db"
)

// startFakeSMTP runs just enough of the SMTP protocol for net/smtp.SendMail
// to succeed (it never advertises STARTTLS, so SendMail proceeds in the
// clear), returning the "host:port" to point a MailConfig at. There's
// nothing here to actually read the mail's content — tests instead read the
// token straight out of the email_tokens table, the same way the person who
// receives the real email would only have the link.
func startFakeSMTP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed at test cleanup
			}
			go serveFakeSMTP(conn)
		}
	}()
	return ln.Addr().String()
}

func serveFakeSMTP(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("220 localhost fake smtp")
	inData := false
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		if inData {
			if line == "." {
				inData = false
				_ = tp.PrintfLine("250 OK")
			}
			continue
		}
		switch upper := strings.ToUpper(line); {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			_ = tp.PrintfLine("250 localhost")
		case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
			_ = tp.PrintfLine("250 OK")
		case upper == "DATA":
			_ = tp.PrintfLine("354 End data with <CR><LF>.<CR><LF>")
			inData = true
		case upper == "QUIT":
			_ = tp.PrintfLine("221 Bye")
			return
		default:
			_ = tp.PrintfLine("500 unrecognized command")
		}
	}
}

// newEnvWithMail is newEnv plus a working (fake) SMTP config, for the tests
// that need s.mail.configured() to be true.
func newEnvWithMail(t *testing.T) *env {
	t.Helper()
	addr := startFakeSMTP(t)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	s.SetMail(MailConfig{Host: host, Port: port, From: "labdoc@example.com"})
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, db: d, url: ts.URL,
		c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	e.created("/setup", url.Values{"username": {"tester"}, "password": {"testpassword"}, "confirm": {"testpassword"}})
	return e
}

func TestEmailNotConfiguredShowsFriendlyError(t *testing.T) {
	e := newEnv(t) // no SetMail call: mail.configured() is false
	if code, body := e.post("/account/email", url.Values{"email": {"me@example.com"}}); code != 200 || !strings.Contains(body, "Outgoing email isn't configured") {
		t.Fatalf("save email with no SMTP configured = %d %q", code, body)
	}
	if n := e.count("SELECT count(*) FROM email_tokens"); n != 0 {
		t.Fatalf("no token should be issued when mail isn't configured, got %d", n)
	}
}

func TestEmailVerifyThenPasswordResetFlow(t *testing.T) {
	e := newEnvWithMail(t)

	// Saving an email starts it unverified and queues a verify token.
	if code, body := e.post("/account/email", url.Values{"email": {"me@example.com"}}); code != 200 || !strings.Contains(body, "Verification email sent") {
		t.Fatalf("save email = %d %q", code, body)
	}
	assertVerified := func(want bool) {
		t.Helper()
		var v sql.NullString
		if err := e.db.QueryRow("SELECT email_verified_at FROM users WHERE username = 'tester'").Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v.Valid != want {
			t.Fatalf("email_verified_at valid = %v, want %v", v.Valid, want)
		}
	}
	assertVerified(false)

	// Resubmitting the same, still-unverified address resends rather than
	// erroring, and replaces the previous token (never more than one).
	if code, body := e.post("/account/email", url.Values{"email": {"me@example.com"}}); code != 200 || !strings.Contains(body, "Verification email sent") {
		t.Fatalf("resend verification = %d %q", code, body)
	}
	if n := e.count("SELECT count(*) FROM email_tokens WHERE purpose = 'verify'"); n != 1 {
		t.Fatalf("verify tokens = %d, want 1 (replaced, not accumulated)", n)
	}

	var token string
	if err := e.db.QueryRow("SELECT token FROM email_tokens WHERE purpose = 'verify'").Scan(&token); err != nil {
		t.Fatal(err)
	}

	// Still signed in on this browser: the link lands back on /account.
	if loc := e.location("/verify-email/" + token); loc != "/account?verified=1" {
		t.Fatalf("verify redirect = %q, want /account?verified=1", loc)
	}
	assertVerified(true)
	if n := e.count("SELECT count(*) FROM email_tokens"); n != 0 {
		t.Fatalf("verify token should be consumed, got %d left", n)
	}

	// The same (now-deleted) link doesn't work twice.
	if code, body := e.get("/verify-email/" + token); code != 200 || !strings.Contains(body, "Link expired") {
		t.Fatalf("reused verify link = %d, want the expired notice, got %q", code, body)
	}

	// Resubmitting the now-verified address again is a no-op, not a re-send.
	if code, body := e.post("/account/email", url.Values{"email": {"me@example.com"}}); code != 200 || !strings.Contains(body, "already your verified email") {
		t.Fatalf("resubmit verified email = %d %q", code, body)
	}

	// Forgot password, from a separate, logged-out client (as it would be in
	// practice) sharing the same server and database.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	out := &env{t: t, db: e.db, url: e.url,
		c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}

	if code, body := out.post("/forgot-password", url.Values{"email": {"me@example.com"}}); code != 200 || !strings.Contains(body, "reset link is on its way") {
		t.Fatalf("forgot-password = %d %q", code, body)
	}
	var resetToken string
	if err := e.db.QueryRow("SELECT token FROM email_tokens WHERE purpose = 'reset'").Scan(&resetToken); err != nil {
		t.Fatal(err)
	}

	if code, body := out.get("/reset-password/" + resetToken); code != 200 || !strings.Contains(body, "New password") {
		t.Fatalf("reset-password form = %d, missing the form", code)
	}
	// A mistyped confirmation doesn't burn the (still single-use) token.
	if code, body := out.post("/reset-password/"+resetToken, url.Values{"password": {"newlongpassword"}, "confirm": {"typo"}}); code != 200 || !strings.Contains(body, "do not match") {
		t.Fatalf("mismatched confirm = %d %q", code, body)
	}
	if n := e.count("SELECT count(*) FROM email_tokens WHERE purpose = 'reset'"); n != 1 {
		t.Fatalf("reset token should survive a failed attempt, got %d left", n)
	}

	if code, _ := out.post("/reset-password/"+resetToken, url.Values{"password": {"newlongpassword"}, "confirm": {"newlongpassword"}}); code != http.StatusSeeOther {
		t.Fatalf("successful reset = %d, want 303", code)
	}
	if n := e.count("SELECT count(*) FROM email_tokens"); n != 0 {
		t.Fatalf("reset token should now be consumed, got %d left", n)
	}

	// The account's original, still-logged-in session was killed by the reset.
	if code, _ := e.get("/vlans"); code != http.StatusSeeOther {
		t.Fatal("the session active before the reset should no longer work")
	}
	// The old password no longer works; the new one does.
	if code, body := out.post("/login", url.Values{"username": {"tester"}, "password": {"testpassword"}}); code != 200 || !strings.Contains(body, "Invalid username or password") {
		t.Fatalf("login with old password = %d %q, want rejected", code, body)
	}
	if code, _ := out.post("/login", url.Values{"username": {"tester"}, "password": {"newlongpassword"}}); code != http.StatusSeeOther {
		t.Fatalf("login with new password = %d, want 303", code)
	}
}

func TestForgotPasswordDoesNotRevealAccountExistence(t *testing.T) {
	e := newEnvWithMail(t)
	// No account has this (or any) email on file at all.
	code, body := e.post("/forgot-password", url.Values{"email": {"nobody@example.com"}})
	if code != 200 || !strings.Contains(body, "reset link is on its way") {
		t.Fatalf("unknown email = %d %q, want the same generic notice", code, body)
	}
	if n := e.count("SELECT count(*) FROM email_tokens"); n != 0 {
		t.Fatalf("no token should be issued for an address nobody owns, got %d", n)
	}
}

func TestEmailMustBeUniqueAcrossAccounts(t *testing.T) {
	e := newEnvWithMail(t)
	if code, body := e.post("/account/users", url.Values{"username": {"second"}, "password": {"anotherlongpw"}, "confirm": {"anotherlongpw"}}); code != 200 || !strings.Contains(body, "Account added.") {
		t.Fatalf("add second account = %d %q", code, body)
	}
	if code, body := e.post("/account/email", url.Values{"email": {"shared@example.com"}}); code != 200 || !strings.Contains(body, "Verification email sent") {
		t.Fatalf("first save = %d %q", code, body)
	}

	// Log in as the second account and try to claim the same address.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	second := &env{t: t, db: e.db, url: e.url,
		c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	second.created("/login", url.Values{"username": {"second"}, "password": {"anotherlongpw"}})
	if code, body := second.post("/account/email", url.Values{"email": {"shared@example.com"}}); code != 200 || !strings.Contains(body, "already in use by another account") {
		t.Fatalf("duplicate email = %d %q", code, body)
	}
}
