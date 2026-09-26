package web

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"labdoc/internal/db"
)

type env struct {
	t   *testing.T
	db  *sql.DB
	url string
	c   *http.Client
}

func newEnv(t *testing.T) *env {
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
	e := &env{
		t: t, db: d, url: ts.URL,
		c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	// All routes require a session; bootstrap the one-time setup account so
	// the rest of the suite exercises CRUD behavior as a signed-in operator.
	e.created("/setup", url.Values{"username": {"tester"}, "password": {"testpassword"}, "confirm": {"testpassword"}})
	return e
}

func (e *env) get(path string) (int, string) {
	e.t.Helper()
	resp, err := e.c.Get(e.url + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// csrfToken returns the CSRF cookie this client already holds, fetching a
// page first to obtain one if it doesn't (every response sets it; see
// csrfProtect). Tests that specifically exercise CSRF rejection bypass this
// via a raw client instead of env's helpers.
func (e *env) csrfToken() string {
	e.t.Helper()
	u, err := url.Parse(e.url)
	if err != nil {
		e.t.Fatal(err)
	}
	find := func() string {
		for _, c := range e.c.Jar.Cookies(u) {
			if c.Name == csrfCookie {
				return c.Value
			}
		}
		return ""
	}
	if tok := find(); tok != "" {
		return tok
	}
	if _, _ = e.get("/"); find() == "" {
		e.t.Fatal("CSRF cookie was not set by the server")
	}
	return find()
}

// post submits form, transparently attaching the CSRF token this client's
// session already carries (or fetches one first). Tests target the app's
// actual behavior, not the CSRF layer itself, which auth_test.go covers
// separately.
func (e *env) post(path string, form url.Values) (int, string) {
	e.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if form.Get("csrf_token") == "" {
		form.Set("csrf_token", e.csrfToken())
	}
	resp, err := e.c.PostForm(e.url+path, form)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// created posts a form expecting a redirect (success).
func (e *env) created(path string, form url.Values) {
	e.t.Helper()
	if code, body := e.post(path, form); code != http.StatusSeeOther {
		e.t.Fatalf("POST %s = %d, want 303\n%s", path, code, body)
	}
}

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func vlan(num, name string) url.Values {
	return url.Values{"number": {num}, "name": {name}}
}

func TestCreateEditDeleteWritesRevisions(t *testing.T) {
	e := newEnv(t)

	e.created("/vlans", vlan("10", "Mgmt"))
	if n := e.count("SELECT count(*) FROM vlans WHERE number = 10"); n != 1 {
		t.Fatalf("vlan not created, count = %d", n)
	}
	if code, body := e.get("/vlans"); code != 200 || !strings.Contains(body, "Mgmt") {
		t.Fatalf("list page = %d, missing Mgmt", code)
	}

	e.created("/vlans/1", vlan("10", "Management"))
	if code, body := e.get("/vlans/1/edit"); code != 200 || !strings.Contains(body, `value="Management"`) {
		t.Fatalf("edit form not prefilled (status %d)", code)
	}

	if code, _ := e.post("/vlans/1/delete", nil); code != 200 {
		t.Fatalf("delete = %d, want 200", code)
	}
	if n := e.count("SELECT count(*) FROM vlans"); n != 0 {
		t.Fatalf("vlan not deleted, count = %d", n)
	}

	// created, updated, deleted.
	if n := e.count("SELECT count(*) FROM revisions WHERE entity_type = 'vlans' AND entity_id = 1"); n != 3 {
		t.Fatalf("revisions = %d, want 3", n)
	}
	_, hist := e.get("/vlans/1/history")
	for _, want := range []string{"created", "updated", "deleted", "name: Mgmt → Management"} {
		if !strings.Contains(hist, want) {
			t.Errorf("history missing %q", want)
		}
	}
}

func TestNoOpUpdateWritesNoRevision(t *testing.T) {
	e := newEnv(t)
	e.created("/vlans", vlan("10", "Mgmt"))
	e.created("/vlans/1", vlan("10", "Mgmt"))
	if n := e.count("SELECT count(*) FROM revisions"); n != 1 {
		t.Fatalf("revisions = %d, want 1 (create only)", n)
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	e.created("/vlans", vlan("10", "Mgmt"))

	cases := []struct {
		name string
		path string
		form url.Values
		want string
	}{
		{"required field", "/vlans", vlan("", "x"), "VLAN number is required"},
		{"non-numeric", "/vlans", vlan("abc", "x"), "must be a number"},
		{"duplicate", "/vlans", vlan("10", "Dup"), "must be unique"},
		{"bad select", "/services", url.Values{"name": {"x"}, "type": {"jail"}}, "invalid value"},
		{"ip needs owner", "/ips", url.Values{"ip": {"10.0.0.1"}}, "needs a device or a service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Errors re-render the form with 200 so htmx swaps them in.
			code, body := e.post(tc.path, tc.form)
			if code != 200 || !strings.Contains(body, tc.want) {
				t.Fatalf("status %d, want 200 containing %q", code, tc.want)
			}
		})
	}
	if n := e.count("SELECT count(*) FROM revisions"); n != 1 {
		t.Fatalf("failed writes must not create revisions, got %d", n)
	}
}

func TestDeviceVLANLinks(t *testing.T) {
	e := newEnv(t)
	e.created("/vlans", vlan("10", "Mgmt"))
	e.created("/vlans", vlan("20", "Servers"))
	e.created("/devices", url.Values{"name": {"optiplex"}, "type": {"Server"}, "vlans": {"1", "2"}})

	if n := e.count("SELECT count(*) FROM device_vlans WHERE device_id = 1"); n != 2 {
		t.Fatalf("links = %d, want 2", n)
	}
	_, list := e.get("/devices")
	if !strings.Contains(list, "VLAN 10 - Mgmt") || !strings.Contains(list, "VLAN 20 - Servers") {
		t.Fatal("device list should show linked VLAN labels")
	}

	e.created("/devices/1", url.Values{"name": {"optiplex"}, "type": {"Server"}, "vlans": {"1"}})
	if n := e.count("SELECT count(*) FROM device_vlans WHERE device_id = 1"); n != 1 {
		t.Fatalf("links after edit = %d, want 1", n)
	}
	_, hist := e.get("/devices/1/history")
	if !strings.Contains(hist, "vlans: [1 2] → [1]") {
		t.Fatal("history should record the VLAN link change")
	}
}

func TestNotFoundAndHealth(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/vlans/999/edit", "/vlans/abc/edit", "/nope"} {
		if code, _ := e.get(path); code != 404 {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
	if code, _ := e.post("/vlans/999/delete", nil); code != 404 {
		t.Errorf("delete missing = %d, want 404", code)
	}
	if code, body := e.get("/healthz"); code != 200 || body != "ok" {
		t.Errorf("healthz = %d %q", code, body)
	}
	code, body := e.get("/")
	if code != 200 || !strings.Contains(body, "<footer>") {
		t.Errorf("index = %d, expected footer with version", code)
	}
	if !strings.Contains(body, `id="theme-toggle"`) {
		t.Error("index should include the dark mode toggle")
	}
}
