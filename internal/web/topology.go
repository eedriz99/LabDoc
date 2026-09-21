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
	"sort"
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
	Tier  int    `json:"tier"` // top-to-bottom row for Auto layout: 0 internet ... 4 services
	// Legacy kinds still render but are not offered for new nodes (VLANs are
	// tags on devices now, not boxes).
	Legacy bool `json:"legacy,omitempty"`
	// Logical kinds are not equipment: they are drawn with a dashed border and
	// only ever connect with dashed logical links (e.g. a service runs on a device).
	Logical bool `json:"logical,omitempty"`
}

// topoKinds is the single source of truth for node types; the editor gets it
// as JSON and the SVG renderer uses it directly. Labels are plain words so a
// reader who does not know the gear can still tell what each box is.
var topoKinds = []topoKind{
	{"internet", "Internet / WAN", "#0284c7", 0, false, false},
	{"firewall", "Firewall", "#dc2626", 1, false, false},
	{"router", "Router", "#ea580c", 1, false, false},
	{"switch", "Switch", "#7c3aed", 2, false, false},
	{"ap", "Access point", "#0d9488", 3, false, false},
	{"hypervisor", "Hypervisor node", "#4338ca", 3, false, false},
	{"server", "Server", "#2563eb", 3, false, false},
	{"storage", "Storage / NAS", "#0e7490", 3, false, false},
	{"client", "Client device", "#475569", 3, false, false},
	{"service", "Service (app)", "#16a34a", 4, false, true},
	{"vlan", "VLAN (logical)", "#a16207", 5, true, true},
	{"other", "Other", "#334155", 3, false, false},
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
	// Detail is a short second line under the name (model or role, e.g.
	// "Cisco 3750 switch"). Vlans are the VLAN numbers the node belongs to,
	// drawn as colored tags; each must be listed in the diagram's VLANs.
	Detail string `json:"detail,omitempty"`
	Vlans  []int  `json:"vlans,omitempty"`
}

type topoLink struct {
	ID    string `json:"id"`
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
	Style string `json:"style,omitempty"` // "", "access", "trunk" or "logical"
}

// topoVLAN is a VLAN shown in the diagram's legend and available as a tag.
type topoVLAN struct {
	Num  int    `json:"num"`
	Name string `json:"name"`
}

type topoLayout struct {
	Nodes []topoNode `json:"nodes"`
	Links []topoLink `json:"links"`
	VLANs []topoVLAN `json:"vlans,omitempty"`
	// HideLabels turns off every link label. By default every link is labeled
	// (its own label, else its type) so no link looks undocumented.
	HideLabels bool `json:"hideLabels,omitempty"`
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
	topoNodeH     = 56 // base node height; grows for a detail line and VLAN tags
	topoDetailH   = 16
	topoChipRowH  = 20
	maxTopoVLANs  = 64
	maxNodeVLANs  = 6
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

	if len(p.VLANs) > maxTopoVLANs {
		return fmt.Errorf("too many VLANs (max %d)", maxTopoVLANs)
	}
	vlanSet := make(map[int]bool, len(p.VLANs))
	for i := range p.VLANs {
		v := &p.VLANs[i]
		if v.Num < 1 || v.Num > 4094 {
			return errors.New("VLAN number must be between 1 and 4094")
		}
		if vlanSet[v.Num] {
			return fmt.Errorf("duplicate VLAN %d", v.Num)
		}
		vlanSet[v.Num] = true
		v.Name = strings.TrimSpace(v.Name)
		if utf8.RuneCountInString(v.Name) > maxTopoLabel {
			return fmt.Errorf("VLAN name too long (max %d characters)", maxTopoLabel)
		}
	}
	sort.Slice(p.VLANs, func(i, j int) bool { return p.VLANs[i].Num < p.VLANs[j].Num })

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
		n.Detail = strings.TrimSpace(n.Detail)
		if utf8.RuneCountInString(n.Detail) > maxTopoLabel {
			return fmt.Errorf("node detail too long (max %d characters)", maxTopoLabel)
		}
		if len(n.Vlans) > maxNodeVLANs {
			return fmt.Errorf("a node can carry at most %d VLAN tags", maxNodeVLANs)
		}
		sort.Ints(n.Vlans)
		for j, v := range n.Vlans {
			if !vlanSet[v] {
				return fmt.Errorf("node %q uses VLAN %d, which is not in the diagram's VLAN list", n.ID, v)
			}
			if j > 0 && n.Vlans[j-1] == v {
				return fmt.Errorf("node %q lists VLAN %d twice", n.ID, v)
			}
		}
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
		if l.Style != "" && l.Style != "access" && l.Style != "trunk" && l.Style != "logical" {
			return fmt.Errorf("unknown link type %q", l.Style)
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

// topoVlanPalette colors VLAN tags. A VLAN's color is its position in the
// diagram's number-sorted VLAN list, so the editor (topology.js) and this
// renderer agree without storing colors.
var topoVlanPalette = []string{"#2563eb", "#be185d", "#15803d", "#b45309", "#6d28d9", "#0e7490", "#b91c1c", "#4d7c0f"}

func topoVlanColors(vlans []topoVLAN) map[int]string {
	m := make(map[int]string, len(vlans))
	for i, v := range vlans {
		m[v.Num] = topoVlanPalette[i%len(topoVlanPalette)]
	}
	return m
}

func topoChipText(n int) string { return "VLAN " + strconv.Itoa(n) }
func topoChipW(n int) float64   { return math.Ceil(float64(len(topoChipText(n)))*6.4) + 14 }

func topoChipsW(nums []int) float64 {
	w := 0.0
	for i, n := range nums {
		if i > 0 {
			w += 4
		}
		w += topoChipW(n)
	}
	return w
}

func topoNodeWidth(n topoNode) float64 {
	w := math.Max(140, float64(utf8.RuneCountInString(n.Label))*8+32)
	if n.Detail != "" {
		w = math.Max(w, float64(utf8.RuneCountInString(n.Detail))*6.4+32)
	}
	if len(n.Vlans) > 0 {
		w = math.Max(w, topoChipsW(n.Vlans)+24)
	}
	return w
}

func topoNodeHeight(n topoNode) float64 {
	h := float64(topoNodeH)
	if n.Detail != "" {
		h += topoDetailH
	}
	if len(n.Vlans) > 0 {
		h += topoChipRowH
	}
	return h
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// topoLinkType classifies a link for drawing, labels and the legend. A link
// touching a logical node (a service or legacy VLAN box) is logical, never a cable.
func topoLinkType(k topoLink, from, to topoNode) string {
	if k.Style == "logical" || topoKindByKey[from.Kind].Logical || topoKindByKey[to.Kind].Logical {
		return "logical"
	}
	if k.Style == "trunk" || k.Style == "access" {
		return k.Style
	}
	return "link"
}

// topoLinkTypes lists link types in legend order. Word is the fallback label.
var topoLinkTypes = []struct{ Key, Word, Legend string }{
	{"trunk", "Trunk", "Trunk cable: carries several VLANs"},
	{"access", "Access", "Access cable: carries one VLAN"},
	{"link", "Link", "Cable, VLANs not specified"},
	{"logical", "Logical", "Dashed: a service running on a device, not a cable"},
}

// topoLinkStyle returns stroke color, width and dash pattern for a link type.
// The editor (topology.js) uses the same values.
func topoLinkStyle(t string) (string, int, string) {
	switch t {
	case "trunk":
		return "#7c3aed", 4, ""
	case "access":
		return "#15803d", 2, ""
	case "logical":
		return "#94a3b8", 2, "6 5"
	}
	return "#64748b", 2, ""
}

// topoLinkLabel is the text drawn on a link: its own label, else its type, or
// nothing when labels are hidden. Every cable gets a label so no cable looks
// undocumented; dashed logical links stay unlabeled unless given a label (the
// legend explains them, and a device with many services would otherwise carry
// a pile of identical "runs on" tags).
func topoLinkLabel(l topoLayout, k topoLink, typ string, from, to topoNode) string {
	if l.HideLabels {
		return ""
	}
	if k.Label != "" {
		return k.Label
	}
	if typ == "logical" {
		return ""
	}
	for _, t := range topoLinkTypes {
		if t.Key == typ {
			return t.Word
		}
	}
	return ""
}

// topoLegend is the key drawn under exported diagrams. It only lists what the
// diagram actually uses.
type topoLegend struct {
	links  []int // indexes into topoLinkTypes
	vlans  []topoVLAN
	kinds  []topoKind
	colors map[int]string
}

func newTopoLegend(l topoLayout, byID map[string]topoNode, colors map[int]string) topoLegend {
	lg := topoLegend{vlans: l.VLANs, colors: colors}
	usedType := map[string]bool{}
	for _, k := range l.Links {
		from, ok1 := byID[k.From]
		to, ok2 := byID[k.To]
		if ok1 && ok2 {
			usedType[topoLinkType(k, from, to)] = true
		}
	}
	for i, t := range topoLinkTypes {
		if usedType[t.Key] {
			lg.links = append(lg.links, i)
		}
	}
	usedKind := map[string]bool{}
	for _, n := range l.Nodes {
		usedKind[n.Kind] = true
	}
	for _, k := range topoKinds {
		if usedKind[k.Key] {
			lg.kinds = append(lg.kinds, k)
		}
	}
	return lg
}

const (
	legendRowH  = 24.0
	legendGap   = 36.0
	legendPad   = 20.0
	legendHeadH = 36.0
)

func (lg topoLegend) columns() (widths []float64, rows int) {
	textW := func(s string) float64 { return float64(utf8.RuneCountInString(s)) * 6.6 }
	if len(lg.links) > 0 {
		w := 0.0
		for _, i := range lg.links {
			w = math.Max(w, textW(topoLinkTypes[i].Legend))
		}
		widths = append(widths, 46+w)
		rows = max(rows, len(lg.links))
	}
	if len(lg.vlans) > 0 {
		chip, name := 0.0, 0.0
		for _, v := range lg.vlans {
			chip = math.Max(chip, topoChipW(v.Num))
			name = math.Max(name, textW(v.Name))
		}
		widths = append(widths, chip+10+name)
		rows = max(rows, len(lg.vlans))
	}
	if len(lg.kinds) > 0 {
		w := 0.0
		for _, k := range lg.kinds {
			w = math.Max(w, textW(k.Label))
		}
		widths = append(widths, 24+w)
		rows = max(rows, len(lg.kinds))
	}
	return widths, rows
}

func (lg topoLegend) size() (w, h float64) {
	widths, rows := lg.columns()
	if len(widths) == 0 {
		return 0, 0
	}
	w = 2 * legendPad
	for i, cw := range widths {
		if i > 0 {
			w += legendGap
		}
		w += cw
	}
	return w, legendHeadH + float64(rows)*legendRowH + 14
}

func (lg topoLegend) write(b *strings.Builder, x, y float64) {
	esc := html.EscapeString
	w, h := lg.size()
	if w == 0 {
		return
	}
	widths, _ := lg.columns()
	fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" rx="8" fill="#f8fafc" stroke="#e2e8f0"/>`+"\n", num(x), num(y), num(w), num(h))
	cx, col := x+legendPad, 0
	head := func(t string) {
		fmt.Fprintf(b, `<text x="%s" y="%s" font-size="11" font-weight="700" letter-spacing="0.6" fill="#64748b">%s</text>`+"\n", num(cx), num(y+24), esc(t))
	}
	rowY := func(i int) float64 { return y + legendHeadH + float64(i)*legendRowH + 12 }
	next := func() { cx += widths[col] + legendGap; col++ }
	if len(lg.links) > 0 {
		head("LINES")
		for i, ti := range lg.links {
			stroke, width, dash := topoLinkStyle(topoLinkTypes[ti].Key)
			d := ""
			if dash != "" {
				d = fmt.Sprintf(` stroke-dasharray="%s"`, dash)
			}
			fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%d"%s/>`+"\n",
				num(cx), num(rowY(i)-4), num(cx+34), num(rowY(i)-4), stroke, width, d)
			fmt.Fprintf(b, `<text x="%s" y="%s" font-size="12" fill="#334155">%s</text>`+"\n", num(cx+46), num(rowY(i)), esc(topoLinkTypes[ti].Legend))
		}
		next()
	}
	if len(lg.vlans) > 0 {
		head("VLAN TAGS")
		chip := 0.0
		for _, v := range lg.vlans {
			chip = math.Max(chip, topoChipW(v.Num))
		}
		for i, v := range lg.vlans {
			cw := topoChipW(v.Num)
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="16" rx="8" fill="%s"/>`+"\n", num(cx), num(rowY(i)-12), num(cw), lg.colors[v.Num])
			fmt.Fprintf(b, `<text x="%s" y="%s" font-size="10" font-weight="700" text-anchor="middle" fill="#ffffff">%s</text>`+"\n", num(cx+cw/2), num(rowY(i)), esc(topoChipText(v.Num)))
			fmt.Fprintf(b, `<text x="%s" y="%s" font-size="12" fill="#334155">%s</text>`+"\n", num(cx+chip+10), num(rowY(i)), esc(v.Name))
		}
		next()
	}
	if len(lg.kinds) > 0 {
		head("DEVICE TYPES")
		for i, k := range lg.kinds {
			dash := ""
			if k.Logical {
				dash = ` stroke-dasharray="3 2"`
			}
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="14" height="14" rx="3" fill="#ffffff" stroke="%s" stroke-width="2"%s/>`+"\n", num(cx), num(rowY(i)-12), k.Color, dash)
			fmt.Fprintf(b, `<text x="%s" y="%s" font-size="12" fill="#334155">%s</text>`+"\n", num(cx+24), num(rowY(i)), esc(k.Label))
		}
	}
}

// renderTopologySVG draws a diagram as a standalone SVG on a white background.
// The editor draws the same shapes and sizes client-side (static/topology.js);
// keep the two in step.
func renderTopologySVG(name string, l topoLayout) string {
	esc := html.EscapeString
	const pad, titleH = 40.0, 44.0

	byID := make(map[string]topoNode, len(l.Nodes))
	minX, minY, maxX, maxY := 0.0, 0.0, 0.0, 0.0
	if len(l.Nodes) > 0 {
		minX, minY = math.Inf(1), math.Inf(1)
		maxX, maxY = math.Inf(-1), math.Inf(-1)
		for _, n := range l.Nodes {
			byID[n.ID] = n
			minX, minY = math.Min(minX, n.X), math.Min(minY, n.Y)
			maxX = math.Max(maxX, n.X+topoNodeWidth(n))
			maxY = math.Max(maxY, n.Y+topoNodeHeight(n))
		}
	}
	colors := topoVlanColors(l.VLANs)
	legend := newTopoLegend(l, byID, colors)
	lgW, lgH := legend.size()

	vx, vy := minX-pad, minY-pad-titleH
	vw, vh := math.Max(math.Max(maxX-minX+2*pad, 360), lgW+2*pad), maxY-minY+2*pad+titleH
	if len(l.Nodes) == 0 {
		vh += 40
	}
	legendY := maxY + 30
	if lgH > 0 {
		vh += lgH + 30
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

	center := func(n topoNode) (float64, float64) {
		return n.X + topoNodeWidth(n)/2, n.Y + topoNodeHeight(n)/2
	}

	// Links first so nodes paint over the line ends.
	for _, k := range l.Links {
		from, ok1 := byID[k.From]
		to, ok2 := byID[k.To]
		if !ok1 || !ok2 {
			continue
		}
		x1, y1 := center(from)
		x2, y2 := center(to)
		stroke, width, dash := topoLinkStyle(topoLinkType(k, from, to))
		d := ""
		if dash != "" {
			d = fmt.Sprintf(` stroke-dasharray="%s"`, dash)
		}
		fmt.Fprintf(&b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%d"%s/>`+"\n",
			num(x1), num(y1), num(x2), num(y2), stroke, width, d)
	}
	for _, n := range l.Nodes {
		kind := topoKindByKey[n.Kind]
		w, h := topoNodeWidth(n), topoNodeHeight(n)
		dash := ""
		if kind.Logical {
			dash = ` stroke-dasharray="6 4"`
		}
		fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s" rx="8" fill="#ffffff" stroke="%s" stroke-width="2"%s/>`+"\n",
			num(n.X), num(n.Y), num(w), num(h), kind.Color, dash)
		fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="10" font-weight="600" text-anchor="middle" fill="%s">%s</text>`+"\n",
			num(n.X+w/2), num(n.Y+20), kind.Color, esc(strings.ToUpper(kind.Label)))
		fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="14" font-weight="700" text-anchor="middle" fill="#0f172a">%s</text>`+"\n",
			num(n.X+w/2), num(n.Y+40), esc(n.Label))
		base := float64(topoNodeH)
		if n.Detail != "" {
			fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="11" text-anchor="middle" fill="#64748b">%s</text>`+"\n",
				num(n.X+w/2), num(n.Y+56), esc(n.Detail))
			base += topoDetailH
		}
		if len(n.Vlans) > 0 {
			cx := n.X + (w-topoChipsW(n.Vlans))/2
			for _, v := range n.Vlans {
				cw := topoChipW(v)
				fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="16" rx="8" fill="%s"/>`+"\n", num(cx), num(n.Y+base-2), num(cw), colors[v])
				fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="10" font-weight="700" text-anchor="middle" fill="#ffffff">%s</text>`+"\n",
					num(cx+cw/2), num(n.Y+base+9), esc(topoChipText(v)))
				cx += cw + 4
			}
		}
	}
	// Link labels last so nodes never cover them.
	for _, k := range l.Links {
		from, ok1 := byID[k.From]
		to, ok2 := byID[k.To]
		if !ok1 || !ok2 {
			continue
		}
		text := topoLinkLabel(l, k, topoLinkType(k, from, to), from, to)
		if text == "" {
			continue
		}
		x1, y1 := center(from)
		x2, y2 := center(to)
		mx, my := (x1+x2)/2, (y1+y2)/2
		w := float64(utf8.RuneCountInString(text))*7 + 12
		fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="20" rx="4" fill="#ffffff" stroke="#cbd5e1"/>`+"\n",
			num(mx-w/2), num(my-10), num(w))
		fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="12" text-anchor="middle" fill="#334155">%s</text>`+"\n",
			num(mx), num(my+4), esc(text))
	}
	if lgH > 0 {
		legend.write(&b, vx+20, legendY)
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
	Num   int64  `json:"num,omitempty"`  // VLAN number
	Name  string `json:"name,omitempty"` // VLAN name
}

type invDevice struct {
	ID     int64   `json:"id"`
	Label  string  `json:"label"`
	Kind   string  `json:"kind"`   // topology node type, derived from the device type
	Detail string  `json:"detail"` // the device's Role, shown under its name
	VLANs  []int64 `json:"vlans"`
}

// deviceTopoKind maps a device type to the topology node type used when the
// device is imported into a diagram. Unknown types fall back to "server".
var deviceTopoKind = map[string]string{
	"Server":           "server",
	"NAS":              "storage",
	"Hypervisor node":  "hypervisor",
	"PC / Workstation": "client",
	"Laptop":           "client",
	"Switch":           "switch",
	"Router":           "router",
	"Firewall":         "firewall",
	"Access point":     "ap",
	"Other":            "other",
}

// invConn is a device-to-switch connection, used to draw and label diagram links.
type invConn struct {
	Device   int64   `json:"device"`
	Switch   int64   `json:"switch"`
	Mode     string  `json:"mode"` // "Access" or "Trunk"
	Untagged int64   `json:"untagged"`
	Tagged   []int64 `json:"tagged"`
}

type invService struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Host  int64  `json:"host"`
	VLAN  int64  `json:"vlan"`
}

type topoInventory struct {
	VLANs       []invItem    `json:"vlans"`
	Devices     []invDevice  `json:"devices"`
	Services    []invService `json:"services"`
	Connections []invConn    `json:"connections"`
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
	inv := topoInventory{VLANs: []invItem{}, Devices: []invDevice{}, Services: []invService{}, Connections: []invConn{}}

	rows, err := queryAll(s.db, "SELECT id, 'VLAN ' || number || ' - ' || name, number, name FROM vlans ORDER BY number")
	if err != nil {
		return inv, err
	}
	for _, r := range rows {
		inv.VLANs = append(inv.VLANs, invItem{ID: toInt(r[0]), Label: str(r[1]), Num: toInt(r[2]), Name: str(r[3])})
	}

	rows, err = queryAll(s.db, "SELECT id, name, type, COALESCE(role, '') FROM devices ORDER BY name")
	if err != nil {
		return inv, err
	}
	devIdx := map[int64]int{}
	for _, r := range rows {
		kind, ok := deviceTopoKind[str(r[2])]
		if !ok {
			kind = "server"
		}
		devIdx[toInt(r[0])] = len(inv.Devices)
		inv.Devices = append(inv.Devices, invDevice{ID: toInt(r[0]), Label: str(r[1]), Kind: kind, Detail: str(r[3]), VLANs: []int64{}})
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

	rows, err = queryAll(s.db, "SELECT id, device_id, switch_id, mode, COALESCE(untagged_vlan_id, 0) FROM connections ORDER BY id")
	if err != nil {
		return inv, err
	}
	connIdx := map[int64]int{}
	for _, r := range rows {
		connIdx[toInt(r[0])] = len(inv.Connections)
		inv.Connections = append(inv.Connections, invConn{
			Device: toInt(r[1]), Switch: toInt(r[2]), Mode: str(r[3]), Untagged: toInt(r[4]), Tagged: []int64{},
		})
	}
	rows, err = queryAll(s.db, "SELECT connection_id, vlan_id FROM connection_vlans ORDER BY connection_id, vlan_id")
	if err != nil {
		return inv, err
	}
	for _, r := range rows {
		if i, ok := connIdx[toInt(r[0])]; ok {
			inv.Connections[i].Tagged = append(inv.Connections[i].Tagged, toInt(r[1]))
		}
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
	// Lets the open editor pick up inventory changes (e.g. a connection just
	// recorded in another tab) without a reload.
	r.Get("/inventory.json", func(w http.ResponseWriter, _ *http.Request) {
		inv, err := s.inventory()
		if err != nil {
			s.fail(w, "inventory", err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, inv)
	})
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
