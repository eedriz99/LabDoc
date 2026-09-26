package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// postJSON posts a JSON body, attaching the CSRF token as a header since a
// JSON body has nowhere to carry the usual hidden form field.
func (e *env) postJSON(path string, v any) (int, string) {
	e.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		e.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, e.url+path, strings.NewReader(string(b)))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, e.csrfToken())
	resp, err := e.c.Do(req)
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

	resp, err := e.c.PostForm(e.url+"/topologies", url.Values{"name": {"Home Lab"}, "csrf_token": {e.csrfToken()}})
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

// networkDiagram is a small tiered diagram using VLAN tags, a logical service
// link and an unlabeled trunk.
func networkDiagram() m {
	return m{
		"name": "Net",
		"vlans": []m{
			{"num": 30, "name": "Lab Services"},
			{"num": 10, "name": "Management"},
		},
		"nodes": []m{
			{"id": "n1", "kind": "internet", "label": "Internet", "x": 100, "y": 40},
			{"id": "n2", "kind": "router", "label": "RTR-2951", "x": 100, "y": 200, "detail": "Cisco 2951 router", "vlans": []int{30, 10}},
			{"id": "n3", "kind": "hypervisor", "label": "Master", "x": 100, "y": 380},
			{"id": "n4", "kind": "service", "label": "nginx", "x": 100, "y": 540, "vlans": []int{30}},
		},
		"links": []m{
			{"id": "l1", "from": "n1", "to": "n2"},
			{"id": "l2", "from": "n2", "to": "n3", "style": "trunk"},
			{"id": "l3", "from": "n3", "to": "n4"},
		},
	}
}

func TestTopologyVLANTagValidation(t *testing.T) {
	e := newEnv(t)
	newTopo(e, "Lab")
	if code, msg := e.postJSON("/topologies/1/save", networkDiagram()); code != 200 {
		t.Fatalf("valid tagged diagram = %d %s", code, msg)
	}

	bad := map[string]func(d m){
		"tag not in VLAN list": func(d m) { d["nodes"].([]m)[1]["vlans"] = []int{99} },
		"vlan zero":            func(d m) { d["vlans"] = []m{{"num": 0, "name": "x"}} },
		"vlan too big":         func(d m) { d["vlans"] = []m{{"num": 4095, "name": "x"}} },
		"duplicate vlan":       func(d m) { d["vlans"] = []m{{"num": 10, "name": "a"}, {"num": 10, "name": "b"}} },
		"duplicate tag":        func(d m) { d["nodes"].([]m)[1]["vlans"] = []int{10, 10} },
		"too many tags": func(d m) {
			var all []m
			var tags []int
			for i := 1; i <= 7; i++ {
				all = append(all, m{"num": i, "name": "v"})
				tags = append(tags, i)
			}
			d["vlans"] = all
			d["nodes"].([]m)[1]["vlans"] = tags
		},
		"detail too long": func(d m) { d["nodes"].([]m)[1]["detail"] = strings.Repeat("x", 81) },
	}
	for name, mut := range bad {
		d := networkDiagram()
		mut(d)
		if code, _ := e.postJSON("/topologies/1/save", d); code != 400 {
			t.Errorf("%s: save = %d, want 400", name, code)
		}
	}

	// The logical link type is accepted.
	d := networkDiagram()
	d["links"].([]m)[0]["style"] = "logical"
	if code, msg := e.postJSON("/topologies/1/save", d); code != 200 {
		t.Errorf("logical link style = %d %s", code, msg)
	}
}

func TestTopologySVGLegendTagsAndLabels(t *testing.T) {
	e := newEnv(t)
	newTopo(e, "Lab")
	if code, msg := e.postJSON("/topologies/1/save", networkDiagram()); code != 200 {
		t.Fatalf("save = %d %s", code, msg)
	}
	_, svg := e.get("/topologies/1/image.svg")

	for name, want := range map[string]string{
		"role line under the name": ">Cisco 2951 router<",
		"vlan tag":                 ">VLAN 30<",
		"legend lines heading":     ">LINES<",
		"legend vlan heading":      ">VLAN TAGS<",
		"legend device heading":    ">DEVICE TYPES<",
		"legend vlan name":         ">Lab Services<",
		"legend trunk entry":       "Trunk cable: carries several VLANs",
		"legend logical entry":     "Dashed: a service running on a device, not a cable",
		"plain link labeled":       ">Link<",
		"trunk fallback label":     ">Trunk<",
		"logical links are dashed": `stroke-dasharray="6 5"`,
		"plain hypervisor label":   "HYPERVISOR NODE",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("svg missing %s (%q)", name, want)
		}
	}
	if strings.Contains(svg, ">Access cable") {
		t.Error("legend lists a link type the diagram does not use")
	}

	// Hiding labels removes every link label but keeps the legend.
	d := networkDiagram()
	d["hideLabels"] = true
	if code, msg := e.postJSON("/topologies/1/save", d); code != 200 {
		t.Fatalf("save = %d %s", code, msg)
	}
	_, svg = e.get("/topologies/1/image.svg")
	for _, gone := range []string{">Runs on<", ">Trunk<", ">Link<"} {
		if strings.Contains(svg, gone) {
			t.Errorf("hideLabels still draws %q", gone)
		}
	}
	if !strings.Contains(svg, "Trunk cable: carries several VLANs") {
		t.Error("legend should stay when labels are hidden")
	}
}

func TestTopologyLinkTypeIsLogicalForServicesAndVLANNodes(t *testing.T) {
	sw := topoNode{Kind: "switch"}
	for _, c := range []struct {
		link     topoLink
		from, to topoNode
		want     string
	}{
		{topoLink{}, sw, topoNode{Kind: "server"}, "link"},
		{topoLink{Style: "trunk"}, sw, topoNode{Kind: "server"}, "trunk"},
		{topoLink{Style: "access"}, sw, topoNode{Kind: "server"}, "access"},
		{topoLink{Style: "trunk"}, sw, topoNode{Kind: "service"}, "logical"}, // a service is never a cable
		{topoLink{}, topoNode{Kind: "vlan"}, sw, "logical"},
		{topoLink{Style: "logical"}, sw, sw, "logical"},
	} {
		if got := topoLinkType(c.link, c.from, c.to); got != c.want {
			t.Errorf("topoLinkType(%+v, %s, %s) = %q, want %q", c.link, c.from.Kind, c.to.Kind, got, c.want)
		}
	}
}

func TestInventoryJSONCarriesRoleAndAccurateKinds(t *testing.T) {
	e := newEnv(t)
	e.created("/vlans", url.Values{"number": {"30"}, "name": {"Lab Services"}})
	d := device("Master", "Hypervisor node")
	d.Set("role", "Proxmox cluster node")
	e.created("/devices", d)
	e.created("/devices", device("Nas1", "NAS"))
	_, body := e.get("/inventory.json")
	for _, want := range []string{`"kind":"hypervisor"`, `"detail":"Proxmox cluster node"`, `"kind":"storage"`, `"name":"Lab Services"`} {
		if !strings.Contains(body, want) {
			t.Errorf("inventory.json missing %s in %s", want, body)
		}
	}
}

func TestTopologyServicesAreDashedLogicalNodes(t *testing.T) {
	e := newEnv(t)
	newTopo(e, "Lab")
	d := m{
		"name": "Svc",
		"nodes": []m{
			{"id": "n1", "kind": "hypervisor", "label": "Master", "x": 40, "y": 40},
			{"id": "n2", "kind": "service", "label": "nginx", "x": 40, "y": 240},
			{"id": "n3", "kind": "server", "label": "Plain", "x": 400, "y": 40},
		},
		"links": []m{{"id": "l1", "from": "n1", "to": "n2"}},
	}
	if code, msg := e.postJSON("/topologies/1/save", d); code != 200 {
		t.Fatalf("save = %d %s", code, msg)
	}
	_, svg := e.get("/topologies/1/image.svg")

	// The service box has a dashed border, the equipment boxes do not.
	if got := strings.Count(svg, `rx="8" fill="#ffffff" stroke="#16a34a" stroke-width="2" stroke-dasharray="6 4"`); got != 1 {
		t.Errorf("service border dashed %d times, want 1", got)
	}
	if strings.Contains(svg, `stroke="#4338ca" stroke-width="2" stroke-dasharray`) || strings.Contains(svg, `stroke="#2563eb" stroke-width="2" stroke-dasharray`) {
		t.Error("equipment nodes must keep a solid border")
	}
	// Its connector is dashed too, even though the link has no style set. It is
	// not labeled: the legend explains dashed lines, and 10+ services on one
	// device would otherwise carry 10+ identical tags.
	if !strings.Contains(svg, `stroke="#94a3b8" stroke-width="2" stroke-dasharray="6 5"`) {
		t.Error("link to a service should be a dashed logical connector")
	}
	if strings.Contains(svg, ">Runs on<") || strings.Contains(svg, ">Logical<") {
		t.Error("logical links should not get a default label")
	}
	// The legend explains both, and marks the service type with a dashed swatch.
	for _, want := range []string{"Dashed: a service running on a device, not a cable", ">Service (app)<", `stroke-dasharray="3 2"`} {
		if !strings.Contains(svg, want) {
			t.Errorf("legend missing %q", want)
		}
	}
	// A trunk style cannot turn a service link into a cable.
	d["links"] = []m{{"id": "l1", "from": "n1", "to": "n2", "style": "trunk"}}
	if code, msg := e.postJSON("/topologies/1/save", d); code != 200 {
		t.Fatalf("save = %d %s", code, msg)
	}
	_, svg = e.get("/topologies/1/image.svg")
	if strings.Contains(svg, "#7c3aed") {
		t.Error("a link to a service must never be drawn as a trunk cable")
	}
}

func TestTopologyHandlesManyServicesOnOneDevice(t *testing.T) {
	e := newEnv(t)
	newTopo(e, "Lab")
	nodes := []m{{"id": "h", "kind": "hypervisor", "label": "Master", "x": 400, "y": 40}}
	var links []m
	for i := 0; i < 14; i++ {
		id := "s" + strings.Repeat("x", i%3) + string(rune('a'+i))
		nodes = append(nodes, m{"id": id, "kind": "service", "label": "svc-" + string(rune('a'+i)), "x": 40 + (i%4)*180, "y": 240 + (i/4)*90})
		links = append(links, m{"id": "l" + id, "from": "h", "to": id})
	}
	d := m{"name": "Busy", "nodes": nodes, "links": links}
	if code, msg := e.postJSON("/topologies/1/save", d); code != 200 {
		t.Fatalf("14 services on one device = %d %s", code, msg)
	}
	_, svg := e.get("/topologies/1/image.svg")
	if got := strings.Count(svg, `stroke-dasharray="6 5"`); got < 14 {
		t.Errorf("want 14 dashed connectors, found %d", got)
	}
}

func TestTopologyKindsLogicalAndLegacyFlags(t *testing.T) {
	if !topoKindByKey["service"].Logical || topoKindByKey["service"].Legacy {
		t.Error("service should be a logical kind that is still offered")
	}
	if !topoKindByKey["vlan"].Logical || !topoKindByKey["vlan"].Legacy {
		t.Error("vlan should be a legacy logical kind")
	}
	for _, k := range []string{"router", "switch", "server", "hypervisor", "storage", "internet"} {
		if topoKindByKey[k].Logical {
			t.Errorf("%s is equipment, not logical", k)
		}
	}
}

func TestTopologyEditorOffersServicesButNotVLANBoxes(t *testing.T) {
	e := newEnv(t)
	newTopo(e, "Lab")
	_, page := e.get("/topologies/1")
	sel := page[strings.Index(page, `id="new-kind"`):]
	sel = sel[:strings.Index(sel, "</select>")]
	if strings.Contains(sel, `value="vlan"`) {
		t.Error("Add node should not offer VLAN boxes")
	}
	for _, want := range []string{`value="service"`, `value="router"`} {
		if !strings.Contains(sel, want) {
			t.Errorf("Add node should offer %s", want)
		}
	}
}

// The editor script looks elements up by id; a template that lost one (for
// example after a bad merge) leaves the editor throwing on load. Guard the
// contract between topology.js and topology_edit.html.
func TestTopologyEditorPageHasEveryElementTheScriptNeeds(t *testing.T) {
	js, err := staticFS.ReadFile("static/topology.js")
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	newTopo(e, "Lab")
	_, page := e.get("/topologies/1")
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`\$\("([A-Za-z0-9_-]+)"\)`).FindAllStringSubmatch(string(js), -1) {
		ids[m[1]] = true
	}
	if len(ids) < 40 {
		t.Fatalf("found only %d element ids in topology.js; the pattern is probably wrong", len(ids))
	}
	for id := range ids {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("topology.js uses #%s but the editor page has no such element", id)
		}
	}
}
