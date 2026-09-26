package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Operator accounts, AdGuard Home style: no public self-service signup, and
// every account has the same (only) privilege level. The first account is
// created by the one-time /setup page, reachable only while the users table
// is empty; further accounts, if wanted, are added from /account by someone
// already signed in. A session cookie (opaque token, looked up in the
// sessions table) gates every other route via sessionAuth, and a separate
// CSRF cookie (see csrfProtect) guards every state-changing request.

const (
	sessionCookie = "labdoc_session"
	sessionTTL    = 30 * 24 * time.Hour
	minPassword   = 10
	maxPassword   = 72 // bcrypt ignores/rejects input beyond this

	csrfCookie = "labdoc_csrf"
	csrfTTL    = sessionTTL
	csrfHeader = "X-CSRF-Token"
	csrfField  = "csrf_token"
)

type authUser struct {
	ID       int64
	Username string
}

type ctxKey int

const (
	ctxUserKey ctxKey = iota
	ctxCSRFKey
)

func userFromContext(ctx context.Context) *authUser {
	u, _ := ctx.Value(ctxUserKey).(*authUser)
	return u
}

// csrfFromContext returns the current request's CSRF token, as stashed by
// csrfProtect, for embedding into rendered pages.
func csrfFromContext(ctx context.Context) string {
	t, _ := ctx.Value(ctxCSRFKey).(string)
	return t
}

var errInvalidLogin = errors.New("invalid username or password")

// userCount reports how many operator accounts exist. Zero means the app is
// unconfigured and only /setup is reachable.
func (s *Server) userCount() (int, error) {
	var n int
	err := s.db.QueryRow("SELECT count(*) FROM users").Scan(&n)
	return n, err
}

// validCredentials checks the rules shared by account creation and password
// changes. Uniqueness is left to the UNIQUE index (see createUser).
func validCredentials(username, password, confirm string) error {
	switch {
	case strings.TrimSpace(username) == "":
		return errors.New("username is required")
	case len(password) < minPassword:
		return fmt.Errorf("password must be at least %d characters", minPassword)
	case len(password) > maxPassword:
		return fmt.Errorf("password must be at most %d characters", maxPassword)
	case password != confirm:
		return errors.New("passwords do not match")
	}
	return nil
}

// createUser hashes password and inserts a new operator account.
func (s *Server) createUser(username, password string) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec("INSERT INTO users (username, password_hash) VALUES (?, ?)",
		strings.TrimSpace(username), string(hash))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return 0, errors.New("that username already exists")
		}
		return 0, err
	}
	return res.LastInsertId()
}

// verifyLogin returns the user id when username/password match a stored
// account. It always runs a bcrypt comparison, even for an unknown username,
// so the response time does not reveal whether the account exists.
func (s *Server) verifyLogin(username, password string) (int64, error) {
	var id int64
	var hash string
	err := s.db.QueryRow("SELECT id, password_hash FROM users WHERE username = ?",
		strings.TrimSpace(username)).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		return 0, errInvalidLogin
	}
	if err != nil {
		return 0, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return 0, errInvalidLogin
	}
	return id, nil
}

// randomToken returns a 256-bit random value, hex-encoded, used for both
// session and CSRF cookies.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// requestIsHTTPS reports whether the original request arrived over HTTPS.
// The app itself only ever speaks plain HTTP; TLS is terminated by the
// reverse proxy in front of it, which is expected to set this header.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// startSession creates a session row and sets the cookie that names it.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(sessionTTL)
	if _, err := s.db.Exec("INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)",
		token, userID, expires.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
	return nil
}

// endSession deletes the session row (if any) and clears the cookie.
func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = s.db.Exec("DELETE FROM sessions WHERE token = ?", c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// authenticate resolves the session cookie to a user, if any, clearing the
// row when it has expired.
func (s *Server) authenticate(r *http.Request) *authUser {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	var u authUser
	var expires string
	err = s.db.QueryRow(
		`SELECT u.id, u.username, s.expires_at FROM sessions s
		 JOIN users u ON u.id = s.user_id WHERE s.token = ?`, c.Value).
		Scan(&u.ID, &u.Username, &expires)
	if err != nil {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, expires); err != nil || time.Now().After(t) {
		_, _ = s.db.Exec("DELETE FROM sessions WHERE token = ?", c.Value)
		return nil
	}
	return &u
}

// loginLimiter throttles repeated failed logins per client IP: after
// loginMaxAttempts failures in loginWindow, that IP is locked out for
// loginLockout. State is in memory only (fine for a single-operator,
// single-process app; a restart simply clears it).
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

type loginAttempt struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

const (
	loginMaxAttempts = 5
	loginWindow      = 15 * time.Minute
	loginLockout     = 15 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string]*loginAttempt{}}
}

func (l *loginLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.attempts[key]
	return !ok || time.Now().After(a.lockedUntil)
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a, ok := l.attempts[key]
	if !ok || now.Sub(a.windowStart) > loginWindow {
		a = &loginAttempt{windowStart: now}
		l.attempts[key] = a
	}
	a.count++
	if a.count >= loginMaxAttempts {
		a.lockedUntil = now.Add(loginLockout)
	}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// publicExact bypasses the login gate for exact paths: the auth pages
// themselves, the health check (used by infra, not a browser session), and
// the two static assets those pages render with. Nothing else under
// /static/* is reachable unauthenticated, and there is no unauthenticated
// directory listing of it either.
var publicExact = map[string]bool{
	"/login":                     true,
	"/setup":                     true,
	"/healthz":                   true,
	"/forgot-password":           true,
	"/static/pico.min.css":       true,
	"/static/app.css":            true,
	"/static/topology-export.js": true, // used by the public share page
}

// publicPrefixes bypasses the login gate for whole subtrees identified by an
// unguessable token in the path rather than a session: the pre-existing
// read-only share links, and the email-verify/password-reset links mailed to
// an address that may not be signed in on this browser/device at all.
var publicPrefixes = []string{"/share/", "/verify-email/", "/reset-password/"}

// isPublicPath reports whether path is reachable without a session.
func isPublicPath(path string) bool {
	if publicExact[path] {
		return true
	}
	for _, p := range publicPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// csrfProtect is a second, independent layer against cross-site requests,
// on top of (not instead of) the session cookie's SameSite=Lax: it still
// holds if a reverse proxy strips that attribute, an older browser ignores
// it, or a future change loosens it. It runs for every request, including
// /login and /setup:
//
//   - It ensures every response carries a labdoc_csrf cookie, generating one
//     on the visitor's first request if there isn't one already, and stashes
//     that value in the request context (see csrfFromContext) so the page
//     being rendered can embed the same token a submitted form must echo
//     back, as a hidden field or an X-CSRF-Token header.
//   - For state-changing methods, it then requires the submitted token to
//     match the cookie (constant-time compare), rejecting with 403 if it's
//     missing or wrong. A forged cross-site request can make the browser
//     attach the cookie, but — being a separate, HttpOnly cookie the
//     attacker's page can neither read nor guess — can't reproduce its value
//     in the body or header LabDoc actually checks.
func (s *Server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if c, err := r.Cookie(csrfCookie); err == nil {
			token = c.Value
		}
		if token == "" {
			var err error
			if token, err = randomToken(); err != nil {
				s.fail(w, "csrf token", err)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     csrfCookie,
				Value:    token,
				Path:     "/",
				Expires:  time.Now().Add(csrfTTL),
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   requestIsHTTPS(r),
			})
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			submitted := r.Header.Get(csrfHeader)
			if submitted == "" {
				if err := r.ParseForm(); err != nil {
					http.Error(w, "bad form", http.StatusBadRequest)
					return
				}
				submitted = r.PostFormValue(csrfField)
			}
			if submitted == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(token)) != 1 {
				http.Error(w, "Your session looks stale. Reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxCSRFKey, token)))
	})
}

// sessionAuth is the security layer in front of everything else the mux
// serves, including /static/*: no valid session and no matching public path
// means no response body, only a redirect to /login (or /setup, before any
// account exists).
func (s *Server) sessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := s.authenticate(r); u != nil {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey, u)))
			return
		}
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		target := "/login"
		if n, err := s.userCount(); err == nil && n == 0 {
			target = "/setup"
		}
		if r.Header.Get("HX-Request") == "true" {
			// hx-boost swaps the response body into the current document;
			// a plain redirect would land the login page's <html> inside
			// it, so tell htmx to navigate the whole page instead.
			w.Header().Set("HX-Redirect", target)
			return
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

// capitalize matches the display convention used for validation errors
// throughout the app (messages are written lowercase, Go style).
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

type authPage struct {
	Title, Error, Notice, Username, Token, CSRFToken string
}

// renderAuthPage fills in the CSRF token every setup/login render needs so
// individual handlers only supply what varies.
func (s *Server) renderAuthPage(w http.ResponseWriter, r *http.Request, template, title, errMsg, username string) {
	s.render(w, template, authPage{
		Title: title, Error: errMsg, Username: username,
		CSRFToken: csrfFromContext(r.Context()),
	})
}

func (s *Server) setupGet(w http.ResponseWriter, r *http.Request) {
	if n, err := s.userCount(); err == nil && n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderAuthPage(w, r, "setup.html", "Create the operator account", "", "")
}

func (s *Server) setupPost(w http.ResponseWriter, r *http.Request) {
	if n, err := s.userCount(); err == nil && n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("confirm")
	if err := validCredentials(username, password, confirm); err != nil {
		s.renderAuthPage(w, r, "setup.html", "Create the operator account", capitalize(err.Error()), username)
		return
	}
	id, err := s.createUser(username, password)
	if err != nil {
		s.renderAuthPage(w, r, "setup.html", "Create the operator account", capitalize(err.Error()), username)
		return
	}
	if err := s.startSession(w, r, id); err != nil {
		s.fail(w, "start session", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) loginGet(w http.ResponseWriter, r *http.Request) {
	if n, err := s.userCount(); err == nil && n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if userFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	notice := ""
	if r.URL.Query().Get("reset") == "1" {
		notice = "Password updated. Log in with your new password."
	}
	s.render(w, "login.html", authPage{Title: "Log in", Notice: notice, CSRFToken: csrfFromContext(r.Context())})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	if n, err := s.userCount(); err == nil && n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")
	key := clientIP(r)
	if !s.loginLimiter.allowed(key) {
		s.renderAuthPage(w, r, "login.html", "Log in", "Too many attempts. Try again in a few minutes.", username)
		return
	}
	id, err := s.verifyLogin(username, password)
	if err != nil {
		s.loginLimiter.fail(key)
		s.renderAuthPage(w, r, "login.html", "Log in", "Invalid username or password.", username)
		return
	}
	s.loginLimiter.reset(key)
	if err := s.startSession(w, r, id); err != nil {
		s.fail(w, "start session", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logoutPost(w http.ResponseWriter, r *http.Request) {
	s.endSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type accountUserRow struct {
	ID       int64
	Username string
	Created  string
	Self     bool
}

type accountPage struct {
	base
	Notice, Error  string
	Email          string
	EmailVerified  bool // Email is set and confirmed
	EmailPending   bool // Email is set but not yet confirmed
	MailConfigured bool
	Users          []accountUserRow
}

func (s *Server) accountGet(w http.ResponseWriter, r *http.Request, notice, errMsg string) {
	rows, err := queryAll(s.db, "SELECT id, username, created_at FROM users ORDER BY username")
	if err != nil {
		s.fail(w, "list users", err)
		return
	}
	me := userFromContext(r.Context())
	var email, verifiedAt sql.NullString
	if err := s.db.QueryRow("SELECT email, email_verified_at FROM users WHERE id = ?", me.ID).
		Scan(&email, &verifiedAt); err != nil {
		s.fail(w, "load email", err)
		return
	}
	page := accountPage{
		base: s.newBase(r, "Account"), Notice: notice, Error: errMsg,
		Email: email.String, EmailVerified: email.Valid && verifiedAt.Valid,
		EmailPending: email.Valid && !verifiedAt.Valid, MailConfigured: s.mail.configured(),
	}
	for _, row := range rows {
		id := row[0].(int64)
		page.Users = append(page.Users, accountUserRow{
			ID: id, Username: str(row[1]), Created: str(row[2]),
			Self: me != nil && me.ID == id,
		})
	}
	s.render(w, "account.html", page)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	me := userFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	current := r.PostFormValue("current_password")
	next := r.PostFormValue("new_password")
	confirm := r.PostFormValue("confirm")
	if _, err := s.verifyLogin(me.Username, current); err != nil {
		s.accountGet(w, r, "", "Current password is incorrect.")
		return
	}
	if err := validCredentials(me.Username, next, confirm); err != nil {
		s.accountGet(w, r, "", capitalize(err.Error()))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		s.fail(w, "hash password", err)
		return
	}
	if _, err := s.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(hash), me.ID); err != nil {
		s.fail(w, "update password", err)
		return
	}
	s.accountGet(w, r, "Password updated.", "")
}

func (s *Server) addUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("confirm")
	if err := validCredentials(username, password, confirm); err != nil {
		s.accountGet(w, r, "", capitalize(err.Error()))
		return
	}
	if _, err := s.createUser(username, password); err != nil {
		s.accountGet(w, r, "", capitalize(err.Error()))
		return
	}
	s.accountGet(w, r, "Account added.", "")
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if me := userFromContext(r.Context()); me != nil && me.ID == id {
		s.accountGet(w, r, "", "You cannot remove the account you are logged in as.")
		return
	}
	n, err := s.userCount()
	if err != nil {
		s.fail(w, "count users", err)
		return
	}
	if n <= 1 {
		s.accountGet(w, r, "", "At least one account must remain.")
		return
	}
	// Sessions belonging to the removed account cascade-delete with it.
	if _, err := s.db.Exec("DELETE FROM users WHERE id = ?", id); err != nil {
		s.fail(w, "delete user", err)
		return
	}
	s.accountGet(w, r, "Account removed.", "")
}
