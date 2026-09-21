package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (e *env) postJSON(path string, v any) (int, string) {
	e.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := e.c.Post(e.url+path, "application/json", strings.NewReader(string(b)))
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func (e *env) header(path, name string) string {
	e.t.Helper()
	resp, err := e.c.Get(e.url + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Header.Get(name)
}

type m = map[string]any

func validDiagram(name string) m {
	return m{
		"name": name,
		"nodes": []m{
			{"id": "n1", "kind": "router", "label": "Edge Router", "x": 40, "y": 60},
			{"id": "n2", "kind": "server", "label": "optiplex", "x": 300, "y": 60,
				"ref": m{"type": "devices", "id": 1}},
		},
		"links": []m{{"id": "l1", "from": "n1", "to": "n2", "label": "VLAN 10"}},
	}
}

func newTopo(e *env, name string) {
	e.t.Helper()
	e.created("/topologies", url.Values{"name": {name}})
}

func TestTopologyLifecycleAndRevisions(t *testing.T) {
	e := newEnv(t)

	resp, err := e.c.PostForm(e.url+"/topologies", url.Values{"name": {"Home Lab"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/topologies/1" {
		t.Fatalf("create = %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	if code, body := e.get("/topologies/1"); code != 200 || !strings.Contains(body, `id="canvas"`) || !strings.Contains(body, `"router"`) {
		t.Fatalf("editor page = %d, missing canvas or node kinds", code)
	}
	if code, body := e.get("/topologies"); code != 200 || !strings.Contains(body, "Home Lab") {
		t.Fatalf("list = %d, missing diagram", code)
	}

	if code, body := e.postJSON("/topologies/1/save", validDiagram("Home Lab")); code != 200 {
		t.Fatalf("save = %d %s", code, body)
	}
	// An identical save must not add a revision: created + first save only.
	if code, _ := e.postJSON("/topologies/1/save", validDiagram("Home Lab")); code != 200 {
		t.Fatal("repeat save failed")
	}
	if n := e.count("SELECT count(*) FROM revisions WHERE entity_type = 'topologies'"); n != 2 {
		t.Fatalf("revisions = %d, want 2", n)
	}

	code, svg := e.get("/topologies/1/image.svg")
	if code != 200 || !strings.HasPrefix(svg, "<?xml") {
		t.Fatalf("image = %d", code)
	}
	for _, want := range []string{"<svg", "Edge Router", "optiplex", "VLAN 10", "ROUTER", "<line"} {
		if !strings.Contains(svg, want) {
			t.Errorf("svg missing %q", want)
		}
	}
	if got := e.header("/topologies/1/image.svg?download=1", "Content-Disposition"); got != `attachment; filename="home-lab.svg"` {
		t.Errorf("Content-Disposition = %q", got)
	}

	// Reload keeps positions and links.
	_, page := e.get("/topologies/1")
	if !strings.Contains(page, `"label":"VLAN 10"`) {
		t.Error("saved link not present when reopening the editor")
	}

	if code, _ := e.post("/topologies/1/delete", nil); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	if n := e.count("SELECT count(*) FROM revisions WHERE entity_type = 'topologies' AND note = 'deleted'"); n != 1 {
		t.Fatalf("deleted revision count = %d, want 1", n)
	}
	if code, _ := e.get("/topologies/1/image.svg"); code != 404 {
		t.Fatalf("image after delete = %d, want 404", code)
	}
}

func TestTopologyRejectsInvalidSaves(t *testing.T) {
	e := newEnv(t)
	newTopo(e, "Lab")

	with := func(mut func(d m)) m {
		d := validDiagram("Lab")
		mut(d)
		return d
	}
	node := func(id, kind, label string, x, y float64) m {
		return m{"id": id, "kind": kind, "label": label, "x": x, "y": y}
	}
	cases := map[string]m{
		"empty name":   with(func(d m) { d["name"] = "  " }),
		"unknown kind": with(func(d m) { d["nodes"] = []m{node("n1", "toaster", "x", 0, 0)}; d["links"] = []m{} }),
		"empty label":  with(func(d m) { d["nodes"] = []m{node("n1", "router", " ", 0, 0)}; d["links"] = []m{} }),
		"duplicate id": with(func(d m) {
			d["nodes"] = []m{node("n1", "router", "a", 0, 0), node("n1", "server", "b", 9, 9)}
			d["links"] = []m{}
		}),
		"bad id chars":  with(func(d m) { d["nodes"] = []m{node("a b", "router", "a", 0, 0)}; d["links"] = []m{} }),
		"out of range":  with(func(d m) { d["nodes"] = []m{node("n1", "router", "a", 99999, 0)}; d["links"] = []m{} }),
		"dangling link": with(func(d m) { d["links"] = []m{{"id": "l1", "from": "n1", "to": "ghost"}} }),
		"self link":     with(func(d m) { d["links"] = []m{{"id": "l1", "from": "n1", "to": "n1"}} }),
		"long label":    with(func(d m) { d["nodes"] = []m{node("n1", "router", strings.Repeat("a", 81), 0, 0)}; d["links"] = []m{} }),
		"bad inventory": with(func(d m) {
			d["nodes"] = []m{{"id": "n1", "kind": "server", "label": "x", "x": 0, "y": 0, "ref": m{"type": "users", "id": 1}}}
			d["links"] = []m{}
		}),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if code, msg := e.postJSON("/topologies/1/save", body); code != 400 {
				t.Fatalf("status %d (%s), want 400", code, msg)
			}
		})
	}
	if code, _ := e.post("/topologies/1/save", url.Values{"name": {"x"}}); code != 400 {
		t.Error("non-JSON body should be rejected")
	}
	if n := e.count("SELECT count(*) FROM revisions WHERE entity_type = 'topologies'"); n != 1 {
		t.Fatalf("rejected saves must not write revisions; got %d", n)
	}
	if code, _ := e.postJSON("/topologies/99/save", validDiagram("x")); code != 404 {
		t.Error("saving a missing topology should 404")
	}
}

func TestTopologySVGEscapesLabels(t *testing.T) {
	e := newEnv(t)
	newTopo(e, "Lab")
	evil := `<script>alert(1)</script>"&`
	d := m{
		"name":  `<img src=x onerror=alert(1)>`,
		"nodes": []m{{"id": "n1", "kind": "server", "label": evil, "x": 0, "y": 0}, {"id": "n2", "kind": "server", "label": "b", "x": 300, "y": 0}},
		"links": []m{{"id": "l1", "from": "n1", "to": "n2", "label": "<b>"}},
	}
	if code, msg := e.postJSON("/topologies/1/save", d); code != 200 {
		t.Fatalf("save = %d %s", code, msg)
	}
	_, svg := e.get("/topologies/1/image.svg")
	for _, bad := range []string{"<script", "<img", "<b>"} {
		if strings.Contains(svg, bad) {
			t.Errorf("svg contains unescaped %q", bad)
		}
	}
	if !strings.Contains(svg, "&lt;script&gt;") {
		t.Error("expected escaped script text in svg")
	}
}

func TestTopologySharing(t *testing.T) {
	e := newEnv(t)
	newTopo(e, "Lab")
	if code, msg := e.postJSON("/topologies/1/save", validDiagram("Lab")); code != 200 {
		t.Fatalf("save = %d %s", code, msg)
	}

	// Nothing is public until shared, and guessed tokens fail.
	if code, _ := e.get("/share/anything"); code != 404 {
		t.Fatalf("unshared page = %d, want 404", code)
	}

	code, body := e.post("/topologies/1/share", nil)
	if code != 200 {
		t.Fatalf("share = %d", code)
	}
	var out struct{ Token string }
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Token) != 22 {
		t.Fatalf("token = %q (err %v), want 22 chars", out.Token, err)
	}
	// Re-sharing keeps the same link.
	_, body2 := e.post("/topologies/1/share", nil)
	if !strings.Contains(body2, out.Token) {
		t.Fatal("re-sharing must not change the token")
	}

	code, page := e.get("/share/" + out.Token)
	if code != 200 || !strings.Contains(page, "Lab") || !strings.Contains(page, "/image.svg") {
		t.Fatalf("share page = %d", code)
	}
	// The public page must not leak app navigation or inventory links.
	for _, leak := range []string{`href="/vlans"`, `href="/devices"`, `href="/topologies"`, "hx-boost"} {
		if strings.Contains(page, leak) {
			t.Errorf("share page leaks app UI: %q", leak)
		}
	}
	if got := e.header("/share/"+out.Token, "X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q", got)
	}
	if got := e.header("/share/"+out.Token+"/image.svg", "X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff missing on shared image: %q", got)
	}
	if code, svg := e.get("/share/" + out.Token + "/image.svg"); code != 200 || !strings.Contains(svg, "Edge Router") {
		t.Fatalf("shared image = %d", code)
	}

	// Revoking makes the old link dead immediately.
	if code, _ := e.post("/topologies/1/unshare", nil); code != 200 {
		t.Fatalf("unshare = %d", code)
	}
	for _, p := range []string{"/share/" + out.Token, "/share/" + out.Token + "/image.svg"} {
		if code, _ := e.get(p); code != 404 {
			t.Errorf("GET %s after unshare = %d, want 404", p, code)
		}
	}
	// A fresh share issues a different token.
	_, body3 := e.post("/topologies/1/share", nil)
	if strings.Contains(body3, out.Token) {
		t.Error("new share reused the revoked token")
	}
	if code, _ := e.post("/topologies/99/share", nil); code != http.StatusNotFound {
		t.Errorf("sharing a missing topology = %d, want 404", code)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Home Lab":   "home-lab",
		"  --A/B-- ": "a-b",
		"日本語":        "topology",
		"":           "topology",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
