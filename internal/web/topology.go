package web

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// topoEntity lets topology writes reuse addRevision, which only needs a Key.
var topoEntity = &Entity{Key: "topologies"}

type topoKind struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Color string `json:"color"`
}

// topoKinds is the single source of truth for node types; the editor gets it
// as JSON and the SVG renderer uses it directly.
var topoKinds = []topoKind{
	{"internet", "Internet", "#0284c7"},
	{"router", "Router", "#ea580c"},
	{"firewall", "Firewall", "#dc2626"},
	{"switch", "Switch", "#7c3aed"},
	{"ap", "Access point", "#0d9488"},
	{"server", "Server", "#2563eb"},
	{"service", "Service", "#16a34a"},
	{"client", "Client", "#475569"},
	{"vlan", "VLAN", "#a16207"},
	{"other", "Other", "#334155"},
}

var topoKindByKey = func() map[string]topoKind {
	m := make(map[string]topoKind, len(topoKinds))
	for _, k := range topoKinds {
		m[k.Key] = k
	}
	return m
}()

type topoRef struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

type topoNode struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"`
	Label string   `json:"label"`
	X     float64  `json:"x"`
	Y     float64  `json:"y"`
	Ref   *topoRef `json:"ref,omitempty"`
}

type topoLink struct {
	ID    string `json:"id"`
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

type topoLayout struct {
	Nodes []topoNode `json:"nodes"`
	Links []topoLink `json:"links"`
}

type topoPayload struct {
	Name string `json:"name"`
	topoLayout
}

const (
	maxTopoNodes  = 500
	maxTopoLinks  = 1000
	maxTopoLabel  = 80
	maxTopoCoord  = 5000
	topoNodeH     = 56
	topoMaxBody   = 1 << 20
	topoImageFont = "system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif"
)

var (
	topoIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	nonSlugRe  = regexp.MustCompile(`[^a-z0-9]+`)
	topoRefSet = map[string]bool{"vlans": true, "devices": true, "services": true}
)

// validate checks and normalizes a save payload. Everything stored (and later
// rendered, including into public share pages) has passed through here.
func (p *topoPayload) validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return errors.New("name is required")
	}
	if utf8.RuneCountInString(p.Name) > maxTopoLabel {
		return fmt.Errorf("name is too long (max %d characters)", maxTopoLabel)
	}
	if len(p.Nodes) > maxTopoNodes {
		return fmt.Errorf("too many nodes (max %d)", maxTopoNodes)
	}
	if len(p.Links) > maxTopoLinks {
		return fmt.Errorf("too many links (max %d)", maxTopoLinks)
	}
	if p.Nodes == nil {
		p.Nodes = []topoNode{}
	}
	if p.Links == nil {
		p.Links = []topoLink{}
	}

	nodeIDs := make(map[string]bool, len(p.Nodes))
	for i := range p.Nodes {
		n := &p.Nodes[i]
		if !topoIDRe.MatchString(n.ID) {
			return errors.New("invalid node id")
		}
		if nodeIDs[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		nodeIDs[n.ID] = true
		if _, ok := topoKindByKey[n.Kind]; !ok {
			return fmt.Errorf("unknown node type %q", n.Kind)
		}
		n.Label = strings.TrimSpace(n.Label)
		if n.Label == "" {
			return errors.New("every node needs a label")
		}
		if utf8.RuneCountInString(n.Label) > maxTopoLabel {
			return fmt.Errorf("node label too long (max %d characters)", maxTopoLabel)
		}
		if n.X < 0 || n.X > maxTopoCoord || n.Y < 0 || n.Y > maxTopoCoord {
			return errors.New("node position out of range")
		}
		n.X, n.Y = math.Round(n.X), math.Round(n.Y)
		if n.Ref != nil && (!topoRefSet[n.Ref.Type] || n.Ref.ID <= 0) {
			return errors.New("invalid inventory reference")
		}
	}

	linkIDs := make(map[string]bool, len(p.Links))
	for i := range p.Links {
		l := &p.Links[i]
		if !topoIDRe.MatchString(l.ID) {
			return errors.New("invalid link id")
		}
		if linkIDs[l.ID] {
			return fmt.Errorf("duplicate link id %q", l.ID)
		}
		linkIDs[l.ID] = true
		if !nodeIDs[l.From] || !nodeIDs[l.To] {
			return errors.New("link refers to a missing node")
		}
		if l.From == l.To {
			return errors.New("a link cannot connect a node to itself")
		}
		l.Label = strings.TrimSpace(l.Label)
		if utf8.RuneCountInString(l.Label) > maxTopoLabel {
			return fmt.Errorf("link label too long (max %d characters)", maxTopoLabel)
		}
	}
	return nil
}

func (p *topoPayload) layoutJSON() (string, error) {
	b, err := json.Marshal(p.topoLayout)
	return string(b), err
}

func topoNodeWidth(label string) float64 {
	return math.Max(140, float64(utf8.RuneCountInString(label))*8+32)
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// renderTopologySVG draws a diagram as a standalone SVG on a white background.
// The editor draws the same shapes and sizes client-side (static/topology.js);
// keep the two in step.
func renderTopologySVG(name string, l topoLayout) string {
	esc := html.EscapeString
	const pad, titleH = 40.0, 44.0

	minX, minY, maxX, maxY := 0.0, 0.0, 0.0, 0.0
	if len(l.Nodes) > 0 {
		minX, minY = math.Inf(1), math.Inf(1)
		maxX, maxY = math.Inf(-1), math.Inf(-1)
		for _, n := range l.Nodes {
			minX, minY = math.Min(minX, n.X), math.Min(minY, n.Y)
			maxX = math.Max(maxX, n.X+topoNodeWidth(n.Label))
			maxY = math.Max(maxY, n.Y+topoNodeH)
		}
	}
	vx, vy := minX-pad, minY-pad-titleH
	vw, vh := math.Max(maxX-minX+2*pad, 360), maxY-minY+2*pad+titleH
	if len(l.Nodes) == 0 {
		vh += 40
	}

	byID := make(map[string]topoNode, len(l.Nodes))
	for _, n := range l.Nodes {
		byID[n.ID] = n
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="%s %s %s %s" font-family="%s">`+"\n",
		num(vw), num(vh), num(vx), num(vy), num(vw), num(vh), topoImageFont)
	fmt.Fprintf(&b, `<title>%s</title>`+"\n", esc(name))
	fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" fill="#ffffff"/>`+"\n", num(vx), num(vy), num(vw), num(vh))
	fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="18" font-weight="700" fill="#0f172a">%s</text>`+"\n",
		num(vx+20), num(vy+30), esc(name))
	if len(l.Nodes) == 0 {
		fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="14" fill="#64748b">Empty diagram</text>`+"\n",
			num(vx+20), num(vy+titleH+40))
	}

	// Links first so nodes paint over the line ends.
	for _, k := range l.Links {
		from, ok1 := byID[k.From]
		to, ok2 := byID[k.To]
		if !ok1 || !ok2 {
			continue
		}
		x1, y1 := from.X+topoNodeWidth(from.Label)/2, from.Y+topoNodeH/2
		x2, y2 := to.X+topoNodeWidth(to.Label)/2, to.Y+topoNodeH/2
		fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#64748b" stroke-width="2"/>`+"\n",
			num(x1), num(y1), num(x2), num(y2))
		if k.Label != "" {
			mx, my := (x1+x2)/2, (y1+y2)/2
			w := float64(utf8.RuneCountInString(k.Label))*7 + 12
			fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="20" rx="4" fill="#ffffff" stroke="#cbd5e1"/>`+"\n",
				num(mx-w/2), num(my-10), num(w))
			fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="12" text-anchor="middle" fill="#334155">%s</text>`+"\n",
				num(mx), num(my+4), esc(k.Label))
		}
	}
	for _, n := range l.Nodes {
		kind := topoKindByKey[n.Kind]
		w := topoNodeWidth(n.Label)
		fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%d" rx="8" fill="#ffffff" stroke="%s" stroke-width="2"/>`+"\n",
			num(n.X), num(n.Y), num(w), topoNodeH, kind.Color)
		fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="10" font-weight="600" text-anchor="middle" fill="%s">%s</text>`+"\n",
			num(n.X+w/2), num(n.Y+20), kind.Color, esc(strings.ToUpper(kind.Label)))
		fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="14" font-weight="700" text-anchor="middle" fill="#0f172a">%s</text>`+"\n",
			num(n.X+w/2), num(n.Y+40), esc(n.Label))
	}
	b.WriteString("</svg>\n")
	return b.String()
}

func slugify(s string) string {
	s = strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		return "topology"
	}
	return s
}

func newShareToken() (string, error) {
	b := make([]byte, 16) // 128 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type topoRow struct {
	ID                           int64
	Name, Layout, Token, Updated string
}

// loadTopo fetches one row by a fixed WHERE clause ("id = ?" or "share_token = ?").
func (s *Server) loadTopo(where string, arg any) (topoRow, error) {
	var t topoRow
	var tok sql.NullString
	err := s.db.QueryRow("SELECT id, name, layout_json, share_token, updated_at FROM topologies WHERE "+where, arg).
		Scan(&t.ID, &t.Name, &t.Layout, &tok, &t.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errNotFound
	}
	t.Token = tok.String
	return t, err
}

func parseLayout(raw string) topoLayout {
	l := topoLayout{Nodes: []topoNode{}, Links: []topoLink{}}
	_ = json.Unmarshal([]byte(raw), &l) // stored data was validated on write
	return l
}

type topoItem struct {
	ID            int64
	Name, Updated string
	Shared        bool
}

type topoListPage struct {
	base
	Error string
	Items []topoItem
}

type invItem struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

type invDevice struct {
	ID    int64   `json:"id"`
	Label string  `json:"label"`
	VLANs []int64 `json:"vlans"`
}

type invService struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Host  int64  `json:"host"`
	VLAN  int64  `json:"vlan"`
}

type topoInventory struct {
	VLANs    []invItem    `json:"vlans"`
	Devices  []invDevice  `json:"devices"`
	Services []invService `json:"services"`
}

type topoEditPage struct {
	base
	ID        int64
	Name      string
	Token     string
	Layout    json.RawMessage
	Kinds     []topoKind
	Inventory topoInventory
}

type sharePage struct {
	Name, Token, Slug string
}

func toInt(v any) int64 {
	n, _ := v.(int64)
	return n
}

func (s *Server) inventory() (topoInventory, error) {
	inv := topoInventory{VLANs: []invItem{}, Devices: []invDevice{}, Services: []invService{}}

	rows, err := queryAll(s.db, "SELECT id, 'VLAN ' || number || ' - ' || name FROM vlans ORDER BY number")
	if err != nil {
		return inv, err
	}
	for _, r := range rows {
		inv.VLANs = append(inv.VLANs, invItem{ID: toInt(r[0]), Label: str(r[1])})
	}

	rows, err = queryAll(s.db, "SELECT id, name FROM devices ORDER BY name")
	if err != nil {
		return inv, err
	}
	devIdx := map[int64]int{}
	for _, r := range rows {
		devIdx[toInt(r[0])] = len(inv.Devices)
		inv.Devices = append(inv.Devices, invDevice{ID: toInt(r[0]), Label: str(r[1]), VLANs: []int64{}})
	}
	rows, err = queryAll(s.db, "SELECT device_id, vlan_id FROM device_vlans ORDER BY device_id, vlan_id")
	if err != nil {
		return inv, err
	}
	for _, r := range rows {
		if i, ok := devIdx[toInt(r[0])]; ok {
			inv.Devices[i].VLANs = append(inv.Devices[i].VLANs, toInt(r[1]))
		}
	}

	rows, err = queryAll(s.db, "SELECT id, name, COALESCE(host_device_id, 0), COALESCE(vlan_id, 0) FROM services ORDER BY name")
	if err != nil {
		return inv, err
	}
	for _, r := range rows {
		inv.Services = append(inv.Services, invService{ID: toInt(r[0]), Label: str(r[1]), Host: toInt(r[2]), VLAN: toInt(r[3])})
	}
	return inv, nil
}

func (s *Server) renderTopoList(w http.ResponseWriter, errMsg string) {
	rows, err := queryAll(s.db, "SELECT id, name, updated_at, share_token IS NOT NULL FROM topologies ORDER BY name, id")
	if err != nil {
		s.fail(w, "list topologies", err)
		return
	}
	page := topoListPage{base: s.newBase("Topology"), Error: errMsg}
	for _, r := range rows {
		page.Items = append(page.Items, topoItem{ID: toInt(r[0]), Name: str(r[1]), Updated: str(r[2]), Shared: toInt(r[3]) == 1})
	}
	s.render(w, "topologies.html", page)
}

func (s *Server) mountTopology(r chi.Router) {
	r.Get("/topologies", func(w http.ResponseWriter, _ *http.Request) { s.renderTopoList(w, "") })
	r.Post("/topologies", s.topoCreate)
	r.Get("/topologies/{id}", s.topoEdit)
	r.Post("/topologies/{id}/save", s.topoSave)
	r.Get("/topologies/{id}/image.svg", s.topoImage)
	r.Post("/topologies/{id}/share", s.topoShare)
	r.Post("/topologies/{id}/unshare", s.topoUnshare)
	r.Post("/topologies/{id}/delete", s.topoDelete)

	// Public, unauthenticated, read-only. Keep this surface minimal.
	r.Get("/share/{token}", s.sharePage)
	r.Get("/share/{token}/image.svg", s.shareImage)
}

func (s *Server) topoCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || utf8.RuneCountInString(name) > maxTopoLabel {
		s.renderTopoList(w, fmt.Sprintf("Name is required (max %d characters)", maxTopoLabel))
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		s.fail(w, "create topology", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec("INSERT INTO topologies (name) VALUES (?)", name)
	if err != nil {
		s.fail(w, "create topology", err)
		return
	}
	id, err := res.LastInsertId()
	if err == nil {
		snap := map[string]any{"id": id, "name": name, "layout": parseLayout("")}
		if err = addRevision(tx, topoEntity, id, snap, "created"); err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		s.fail(w, "create topology", err)
		return
	}
	http.Redirect(w, r, "/topologies/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) topoEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	t, err := s.loadTopo("id = ?", id)
	if errors.Is(err, errNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "load topology", err)
		return
	}
	inv, err := s.inventory()
	if err != nil {
		s.fail(w, "inventory", err)
		return
	}
	s.render(w, "topology_edit.html", topoEditPage{
		base: s.newBase(t.Name), ID: t.ID, Name: t.Name, Token: t.Token,
		Layout: json.RawMessage(t.Layout), Kinds: topoKinds, Inventory: inv,
	})
}

func (s *Server) topoSave(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, topoMaxBody)
	var p topoPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := p.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	layout, err := p.layoutJSON()
	if err == nil {
		err = s.writeTopo(id, p.Name, layout)
	}
	if errors.Is(err, errNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "save topology", err)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

// writeTopo stores a validated diagram and, when it changed, a revision.
func (s *Server) writeTopo(id int64, name, layout string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var oldName, oldLayout string
	err = tx.QueryRow("SELECT name, layout_json FROM topologies WHERE id = ?", id).Scan(&oldName, &oldLayout)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	} else if err != nil {
		return err
	}
	if oldName == name && oldLayout == layout {
		return nil
	}
	if _, err := tx.Exec("UPDATE topologies SET name = ?, layout_json = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = ?",
		name, layout, id); err != nil {
		return err
	}
	snap := map[string]any{"id": id, "name": name, "layout": json.RawMessage(layout)}
	if err := addRevision(tx, topoEntity, id, snap, "updated"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) topoDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.removeTopo(id)
	if errors.Is(err, errNotFound) {
		http.NotFound(w, r)
	} else if err != nil {
		s.fail(w, "delete topology", err)
	}
	// Empty 200: htmx swaps the table row out.
}

func (s *Server) removeTopo(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var name, layout string
	err = tx.QueryRow("SELECT name, layout_json FROM topologies WHERE id = ?", id).Scan(&name, &layout)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM topologies WHERE id = ?", id); err != nil {
		return err
	}
	snap := map[string]any{"id": id, "name": name, "layout": json.RawMessage(layout)}
	if err := addRevision(tx, topoEntity, id, snap, "deleted"); err != nil {
		return err
	}
	return tx.Commit()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) topoShare(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tok, err := newShareToken()
	if err != nil {
		s.fail(w, "share token", err)
		return
	}
	// COALESCE keeps an existing token, so re-sharing never changes the URL.
	res, err := s.db.Exec("UPDATE topologies SET share_token = COALESCE(share_token, ?) WHERE id = ?", tok, id)
	if err != nil {
		s.fail(w, "share topology", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.NotFound(w, r)
		return
	}
	t, err := s.loadTopo("id = ?", id)
	if err != nil {
		s.fail(w, "share topology", err)
		return
	}
	writeJSON(w, map[string]string{"token": t.Token})
}

func (s *Server) topoUnshare(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	res, err := s.db.Exec("UPDATE topologies SET share_token = NULL WHERE id = ?", id)
	if err != nil {
		s.fail(w, "unshare topology", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]string{})
}

func serveTopoSVG(w http.ResponseWriter, r *http.Request, t topoRow) {
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	h.Set("Cache-Control", "no-store")
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", `attachment; filename="`+slugify(t.Name)+`.svg"`)
	}
	_, _ = w.Write([]byte(renderTopologySVG(t.Name, parseLayout(t.Layout))))
}

func (s *Server) topoImage(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	t, err := s.loadTopo("id = ?", id)
	if errors.Is(err, errNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "topology image", err)
		return
	}
	serveTopoSVG(w, r, t)
}

func (s *Server) sharedTopo(w http.ResponseWriter, r *http.Request) (topoRow, bool) {
	tok := chi.URLParam(r, "token")
	if tok == "" || len(tok) > 64 {
		http.NotFound(w, r)
		return topoRow{}, false
	}
	t, err := s.loadTopo("share_token = ?", tok)
	if errors.Is(err, errNotFound) {
		http.NotFound(w, r)
		return t, false
	} else if err != nil {
		s.fail(w, "shared topology", err)
		return t, false
	}
	return t, true
}

func (s *Server) shareImage(w http.ResponseWriter, r *http.Request) {
	if t, ok := s.sharedTopo(w, r); ok {
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("Referrer-Policy", "no-referrer")
		serveTopoSVG(w, r, t)
	}
}

func (s *Server) sharePage(w http.ResponseWriter, r *http.Request) {
	t, ok := s.sharedTopo(w, r)
	if !ok {
		return
	}
	h := w.Header()
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	s.render(w, "share.html", sharePage{Name: t.Name, Token: t.Token, Slug: slugify(t.Name)})
}
