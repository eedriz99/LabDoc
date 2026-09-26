package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// Email verification and password reset. An account's email is optional and
// unrelated to login (username/password still work with no email on file);
// it exists only so "forgot password" has somewhere to send a reset link,
// and only once that address is confirmed — an unverified email is never
// used to send one. Sending goes through s.mail (see mail.go); if that isn't
// configured, these features report a friendly error instead of doing
// nothing silently.

const (
	verifyTokenTTL = 24 * time.Hour
	resetTokenTTL  = 1 * time.Hour
)

// requestBaseURL builds the externally-reachable origin for a mailed link,
// from the request that triggered the send: the same X-Forwarded-Proto the
// session cookie's Secure flag already trusts, plus X-Forwarded-Host if the
// reverse proxy sets it, else the plain Host header.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if requestIsHTTPS(r) {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

type emailToken struct {
	UserID   int64
	Username string
	Email    string
	Expired  bool
}

// issueEmailToken replaces any existing token of that purpose for the user
// with a fresh one (so an old, unfinished flow can't also be completed) and
// returns it.
func (s *Server) issueEmailToken(userID int64, purpose, email string, ttl time.Duration) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("DELETE FROM email_tokens WHERE user_id = ? AND purpose = ?", userID, purpose); err != nil {
		return "", err
	}
	expires := time.Now().Add(ttl).UTC().Format(time.RFC3339)
	if _, err := tx.Exec("INSERT INTO email_tokens (token, user_id, purpose, email, expires_at) VALUES (?, ?, ?, ?, ?)",
		token, userID, purpose, email, expires); err != nil {
		return "", err
	}
	return token, tx.Commit()
}

// lookupEmailToken returns a token's details without consuming it (so a
// mistyped form on /reset-password can be retried against the same link,
// and loading the page doesn't burn a single-use token by itself), or nil if
// no such (token, purpose) pair exists.
func (s *Server) lookupEmailToken(token, purpose string) (*emailToken, error) {
	var t emailToken
	var expires string
	err := s.db.QueryRow(
		`SELECT et.user_id, u.username, et.email, et.expires_at FROM email_tokens et
		 JOIN users u ON u.id = et.user_id WHERE et.token = ? AND et.purpose = ?`,
		token, purpose).Scan(&t.UserID, &t.Username, &t.Email, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	exp, perr := time.Parse(time.RFC3339, expires)
	t.Expired = perr != nil || time.Now().After(exp)
	return &t, nil
}

func (s *Server) deleteEmailToken(token string) error {
	_, err := s.db.Exec("DELETE FROM email_tokens WHERE token = ?", token)
	return err
}

func (s *Server) sendVerifyEmail(r *http.Request, userID int64, email string) error {
	token, err := s.issueEmailToken(userID, "verify", email, verifyTokenTTL)
	if err != nil {
		return err
	}
	link := requestBaseURL(r) + "/verify-email/" + token
	body := fmt.Sprintf(
		"Confirm this address for your LabDoc account by opening this link:\n\n%s\n\n"+
			"This link expires in 24 hours. If you didn't request this, you can ignore it.", link)
	return s.mail.send(email, "Verify your LabDoc email", body)
}

// saveEmail sets or changes the signed-in account's email and, if it's new
// or still unverified, (re)sends a confirmation link. Resubmitting the
// current, already-verified address is a no-op rather than a needless
// re-verification loop.
func (s *Server) saveEmail(w http.ResponseWriter, r *http.Request) {
	me := userFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	if email == "" || !strings.Contains(email, "@") {
		s.accountGet(w, r, "", "Enter a valid email address.")
		return
	}
	var current sql.NullString
	var verifiedAt sql.NullString
	if err := s.db.QueryRow("SELECT email, email_verified_at FROM users WHERE id = ?", me.ID).
		Scan(&current, &verifiedAt); err != nil {
		s.fail(w, "load email", err)
		return
	}
	if current.Valid && current.String == email && verifiedAt.Valid {
		s.accountGet(w, r, "That's already your verified email.", "")
		return
	}
	if !s.mail.configured() {
		s.accountGet(w, r, "", "Outgoing email isn't configured on this server yet; ask the administrator to set the SMTP flags.")
		return
	}
	if _, err := s.db.Exec("UPDATE users SET email = ?, email_verified_at = NULL WHERE id = ?", email, me.ID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			s.accountGet(w, r, "", "That email is already in use by another account.")
			return
		}
		s.fail(w, "save email", err)
		return
	}
	if err := s.sendVerifyEmail(r, me.ID, email); err != nil {
		s.accountGet(w, r, "", "Email saved, but sending the verification link failed: "+err.Error())
		return
	}
	s.accountGet(w, r, "Verification email sent to "+email+". Click the link in it to confirm.", "")
}

// verifyEmail completes the link mailed by saveEmail. It works whether or
// not the visitor is signed in on this browser (they may be reading mail on
// a different device), hence the public route and the branching landing page.
func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	t, err := s.lookupEmailToken(token, "verify")
	if err != nil {
		s.fail(w, "lookup verify token", err)
		return
	}
	if t == nil || t.Expired {
		s.renderNotice(w, "Link expired",
			"This verification link is invalid or has expired. Save your email again from Account to get a new one.",
			"/login", "Go to login")
		return
	}
	if err := s.deleteEmailToken(token); err != nil {
		s.fail(w, "consume verify token", err)
		return
	}
	// The address may have changed again since this link was sent; only
	// mark verified if it's still the one on file.
	res, err := s.db.Exec(
		"UPDATE users SET email_verified_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ? AND email = ?",
		t.UserID, t.Email)
	if err != nil {
		s.fail(w, "verify email", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.renderNotice(w, "Link expired",
			"This email has changed since this link was sent. Save the current one again from Account to get a new link.",
			"/login", "Go to login")
		return
	}
	if me := userFromContext(r.Context()); me != nil && me.ID == t.UserID {
		http.Redirect(w, r, "/account?verified=1", http.StatusSeeOther)
		return
	}
	s.renderNotice(w, "Email verified", "Your email is confirmed and can now be used to reset your password.", "/login", "Go to login")
}

func (s *Server) forgotPasswordGet(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "forgot-password.html", authPage{Title: "Reset your password", CSRFToken: csrfFromContext(r.Context())})
}

// forgotPasswordPost never reveals whether the address matched an account,
// has no email at all, or isn't verified — the response is identical either
// way, and rate limiting is by IP rather than by address, so this can't be
// used to enumerate accounts by timing or by watching which addresses get
// locked out.
func (s *Server) forgotPasswordPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	key := clientIP(r)
	if !s.resetLimiter.allowed(key) {
		s.render(w, "forgot-password.html", authPage{
			Title: "Reset your password", Username: email, CSRFToken: csrfFromContext(r.Context()),
			Error: "Too many attempts. Try again in a few minutes.",
		})
		return
	}
	s.resetLimiter.fail(key) // every attempt counts, whether or not it matches an account

	if !s.mail.configured() {
		s.render(w, "forgot-password.html", authPage{
			Title: "Reset your password", Username: email, CSRFToken: csrfFromContext(r.Context()),
			Error: "Outgoing email isn't configured on this server.",
		})
		return
	}
	if email != "" {
		var userID int64
		err := s.db.QueryRow("SELECT id FROM users WHERE email = ? AND email_verified_at IS NOT NULL", email).Scan(&userID)
		if err == nil {
			if token, err := s.issueEmailToken(userID, "reset", email, resetTokenTTL); err == nil {
				link := requestBaseURL(r) + "/reset-password/" + token
				body := fmt.Sprintf(
					"Reset your LabDoc password by opening this link:\n\n%s\n\n"+
						"This link expires in 1 hour. If you didn't request this, you can ignore it — your password hasn't changed.", link)
				_ = s.mail.send(email, "Reset your LabDoc password", body) // best-effort; response is the same regardless
			}
		}
	}
	s.render(w, "forgot-password.html", authPage{
		Title: "Reset your password", CSRFToken: csrfFromContext(r.Context()),
		Notice: "If that address has a verified LabDoc account, a reset link is on its way.",
	})
}

func (s *Server) resetPasswordGet(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	t, err := s.lookupEmailToken(token, "reset")
	if err != nil {
		s.fail(w, "lookup reset token", err)
		return
	}
	if t == nil || t.Expired {
		s.renderNotice(w, "Link expired",
			"This password reset link is invalid or has expired. Request a new one from the login page.",
			"/forgot-password", "Request a new link")
		return
	}
	s.render(w, "reset-password.html", authPage{Title: "Choose a new password", Token: token, CSRFToken: csrfFromContext(r.Context())})
}

// resetPasswordPost only consumes the token once the new password is
// actually accepted, so a mistyped confirmation doesn't burn the link.
func (s *Server) resetPasswordPost(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	t, err := s.lookupEmailToken(token, "reset")
	if err != nil {
		s.fail(w, "lookup reset token", err)
		return
	}
	if t == nil || t.Expired {
		s.renderNotice(w, "Link expired",
			"This password reset link is invalid or has expired. Request a new one from the login page.",
			"/forgot-password", "Request a new link")
		return
	}
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("confirm")
	if err := validCredentials(t.Username, password, confirm); err != nil {
		s.render(w, "reset-password.html", authPage{
			Title: "Choose a new password", Token: token, CSRFToken: csrfFromContext(r.Context()),
			Error: capitalize(err.Error()),
		})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.fail(w, "hash password", err)
		return
	}
	if _, err := s.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(hash), t.UserID); err != nil {
		s.fail(w, "reset password", err)
		return
	}
	if err := s.deleteEmailToken(token); err != nil {
		s.fail(w, "consume reset token", err)
		return
	}
	// A password reset this way means the old one may be compromised: sign
	// every session for this account out, not just skip creating a new one.
	if _, err := s.db.Exec("DELETE FROM sessions WHERE user_id = ?", t.UserID); err != nil {
		s.fail(w, "invalidate sessions", err)
		return
	}
	http.Redirect(w, r, "/login?reset=1", http.StatusSeeOther)
}

// noticePage is a standalone (no app nav, matching login/setup) page for a
// one-off message to a visitor who may not have a session at all, such as
// the far end of an email link.
type noticePage struct {
	Title, Message, LinkHref, LinkText string
}

func (s *Server) renderNotice(w http.ResponseWriter, title, message, linkHref, linkText string) {
	s.render(w, "notice.html", noticePage{Title: title, Message: message, LinkHref: linkHref, LinkText: linkText})
}
