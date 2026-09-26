package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"labdoc/internal/db"
)

// rawEnv is like newEnv but skips the /setup bootstrap, for tests that need
// to drive the unauthenticated state themselves.
func rawEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &env{
		t: t, db: d, url: ts.URL,
		c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// loggedOutClientFor returns a fresh, cookie-less client pointed at the same
// running server as e, so both share one database and set of accounts.
func loggedOutClientFor(t *testing.T, e *env) *env {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, db: e.db, url: e.url,
		c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func creds() url.Values {
	return url.Values{"username": {"admin"}, "password": {"correct-horse"}, "confirm": {"correct-horse"}}
}

// location performs a GET and returns the Location header (the test client
// does not follow redirects, by design).
func (e *env) location(path string) string {
	e.t.Helper()
	resp, err := e.c.Get(e.url + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Header.Get("Location")
}

func TestUnauthenticatedRedirectsToSetupThenLogin(t *testing.T) {
	e := rawEnv(t)

	// No account yet: every protected path, including static assets not on
	// the public allowlist, bounces to /setup.
	for _, path := range []string{"/", "/vlans", "/devices/new", "/topologies", "/static/htmx.min.js", "/account"} {
		if code, _ := e.get(path); code != http.StatusSeeOther {
			t.Errorf("GET %s (no account) = %d, want 303", path, code)
		}
	}
	if loc := e.location("/vlans"); loc != "/setup" {
		t.Errorf("redirect target = %q, want /setup", loc)
	}

	e.created("/setup", creds())
	if n := e.count("SELECT count(*) FROM users"); n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}

	// /setup is one-time: once an account exists, an unauthenticated visit
	// (even a fresh client) goes to /login instead of creating another one.
	out := loggedOutClientFor(t, e)
	if code, _ := out.get("/setup"); code != http.StatusSeeOther {
		t.Fatalf("GET /setup after an account exists = %d, want 303", code)
	}
	if loc := out.location("/setup"); loc != "/login" {
		t.Errorf("second /setup redirect = %q, want /login", loc)
	}
	if loc := out.location("/vlans"); loc != "/login" {
		t.Errorf("protected redirect target = %q, want /login", loc)
	}
}

func TestLoginLogoutAndWrongPassword(t *testing.T) {
	e := rawEnv(t)
	e.created("/setup", creds())
	// The setup flow itself logs the operator in; exercise /login from a
	// separate, logged-out client against the same server.
	out := loggedOutClientFor(t, e)

	if code, body := out.post("/login", url.Values{"username": {"admin"}, "password": {"wrong"}}); code != 200 || !strings.Contains(body, "Invalid username or password") {
		t.Fatalf("bad login = %d, want 200 with an error", code)
	}
	if code, _ := out.get("/vlans"); code != http.StatusSeeOther {
		t.Fatal("a rejected login must not grant access")
	}

	out.created("/login", url.Values{"username": {"admin"}, "password": {"correct-horse"}})
	if code, _ := out.get("/vlans"); code != 200 {
		t.Fatalf("GET /vlans after login = %d, want 200", code)
	}

	if code, _ := out.post("/logout", nil); code != http.StatusSeeOther {
		t.Fatalf("logout = %d, want 303", code)
	}
	if code, _ := out.get("/vlans"); code != http.StatusSeeOther {
		t.Fatal("GET /vlans after logout should redirect to /login")
	}
}

func TestLoginRateLimited(t *testing.T) {
	e := rawEnv(t)
	e.created("/setup", creds())
	out := loggedOutClientFor(t, e)

	var last string
	for range loginMaxAttempts {
		_, last = out.post("/login", url.Values{"username": {"admin"}, "password": {"wrong"}})
	}
	if !strings.Contains(last, "Invalid username or password") {
		t.Fatalf("attempt %d body = %q, want the plain invalid-credentials error", loginMaxAttempts, last)
	}
	_, body := out.post("/login", url.Values{"username": {"admin"}, "password": {"correct-horse"}})
	if !strings.Contains(body, "Too many attempts") {
		t.Fatalf("locked-out login body = %q, want the lockout message", body)
	}
}

func TestPublicPathsBypassAuth(t *testing.T) {
	e := rawEnv(t)
	e.created("/setup", creds())
	out := loggedOutClientFor(t, e)

	// /setup is deliberately excluded here: it's on the public allowlist too,
	// but once an account exists its handler itself redirects to /login
	// (covered by TestUnauthenticatedRedirectsToSetupThenLogin).
	for _, path := range []string{"/login", "/healthz", "/static/pico.min.css", "/static/app.css", "/static/topology-export.js"} {
		if code, _ := out.get(path); code == http.StatusSeeOther {
			t.Errorf("GET %s should be public, got a redirect", path)
		}
	}
	// Everything else under /static/ still requires a session.
	for _, path := range []string{"/static/htmx.min.js", "/static/topology.js"} {
		if code, _ := out.get(path); code != http.StatusSeeOther {
			t.Errorf("GET %s = %d, want 303 (gated)", path, code)
		}
	}
}

func TestCSRFRejectsMissingOrWrongToken(t *testing.T) {
	e := newEnv(t) // logged in, real session cookie in e.c's jar

	// A same-origin request that just omits the token entirely. Uses the raw
	// client, not e.post, since that helper fills in a valid token by design.
	resp0, err := e.c.PostForm(e.url+"/vlans", url.Values{"number": {"10"}, "name": {"Mgmt"}})
	if err != nil {
		t.Fatal(err)
	}
	body0, _ := io.ReadAll(resp0.Body)
	_ = resp0.Body.Close()
	if resp0.StatusCode != http.StatusForbidden || !strings.Contains(string(body0), "stale") {
		t.Fatalf("missing csrf_token = %d %q, want 403", resp0.StatusCode, body0)
	}
	// A well-formed but wrong token (what a cross-site attacker would have
	// to guess, having no way to read the real cookie).
	if code, body := e.post("/vlans", url.Values{"number": {"10"}, "name": {"Mgmt"}, "csrf_token": {strings.Repeat("a", 64)}}); code != http.StatusForbidden || !strings.Contains(body, "stale") {
		t.Fatalf("wrong csrf_token = %d %q, want 403", code, body)
	}
	if n := e.count("SELECT count(*) FROM vlans"); n != 0 {
		t.Fatalf("rejected requests must not write anything, got %d vlans", n)
	}

	// The X-CSRF-Token header works as an alternative to the form field
	// (what the JS-driven parts of the UI use).
	req, err := http.NewRequest(http.MethodPost, e.url+"/vlans", strings.NewReader(url.Values{"number": {"10"}, "name": {"Mgmt"}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(csrfHeader, e.csrfToken())
	resp, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("valid X-CSRF-Token header = %d, want 303", resp.StatusCode)
	}

	// A cross-site attacker's forged request: it has no cookie jar for
	// labdoc.example at all, so it can neither send the session cookie nor
	// know the CSRF token, however it tries to submit the form.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	attacker := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = attacker.PostForm(e.url+"/vlans", url.Values{"number": {"20"}, "name": {"Forged"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forged cross-origin request = %d, want 403", resp.StatusCode)
	}
	if n := e.count("SELECT count(*) FROM vlans"); n != 1 {
		t.Fatalf("only the one legitimate write should have landed, got %d vlans", n)
	}
}

func TestAccountPasswordChangeAndUserManagement(t *testing.T) {
	e := newEnv(t) // logged in as "tester" / "testpassword"
	s := &Server{db: e.db}

	// Wrong current password is rejected; the hash is left untouched.
	_, body := e.post("/account/password", url.Values{
		"current_password": {"nope"}, "new_password": {"newlongpassword"}, "confirm": {"newlongpassword"},
	})
	if !strings.Contains(body, "Current password is incorrect") {
		t.Fatalf("wrong current password body = %q", body)
	}

	// Correct current password updates the hash; the old password stops working.
	if code, _ := e.post("/account/password", url.Values{
		"current_password": {"testpassword"}, "new_password": {"newlongpassword"}, "confirm": {"newlongpassword"},
	}); code != 200 {
		t.Fatalf("change password = %d, want 200", code)
	}
	if _, err := s.verifyLogin("tester", "testpassword"); err == nil {
		t.Fatal("old password should no longer verify")
	}
	if _, err := s.verifyLogin("tester", "newlongpassword"); err != nil {
		t.Fatal("new password should verify")
	}

	// Add a second account; it appears in the list and can log in.
	if code, body := e.post("/account/users", url.Values{"username": {"second"}, "password": {"anotherlongpw"}, "confirm": {"anotherlongpw"}}); code != 200 || !strings.Contains(body, "Account added.") {
		t.Fatalf("add account = %d, want 200 with a confirmation", code)
	}
	if n := e.count("SELECT count(*) FROM users"); n != 2 {
		t.Fatalf("users = %d, want 2", n)
	}
	_, list := e.get("/account")
	if !strings.Contains(list, "second") {
		t.Fatal("account list should show the new account")
	}

	// The account you're logged in as can't be deleted; another one can.
	var secondID int64
	if err := e.db.QueryRow("SELECT id FROM users WHERE username = 'second'").Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if code, body := e.post("/account/users/1/delete", nil); code != 200 || !strings.Contains(body, "You cannot remove the account you are logged in as") {
		t.Fatalf("self-delete = %d %q", code, body)
	}
	if code, _ := e.post("/account/users/"+strconv.FormatInt(secondID, 10)+"/delete", nil); code != 200 {
		t.Fatalf("delete second account = %d, want 200", code)
	}
	if n := e.count("SELECT count(*) FROM users"); n != 1 {
		t.Fatalf("users after delete = %d, want 1", n)
	}
}
