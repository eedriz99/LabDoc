package web

import (
	"net/http"
	"strings"
)

// Field kinds understood by the generic CRUD handlers and form template.
const (
	kText     = "text"
	kTextarea = "textarea"
	kNumber   = "number"
	kBool     = "bool"
	kSelect   = "select" // fixed Options
	kRef      = "ref"    // foreign key to another entity (Ref)
	kMulti    = "multi"  // many-to-many via Join
	kChecks   = "checks" // any subset of fixed Options, stored comma-separated in one column
)

// deviceTypes are the choices for a device's type. The topology designer maps
// each to a node type in deviceTopoKind (topology.go); keep them in step.
var deviceTypes = []string{
	"Server", "Hypervisor node", "PC / Workstation", "Laptop", "NAS",
	"Switch", "Router", "Firewall", "Access point", "Other",
}

// driveTypes may be combined on one device (e.g. an SSD boot drive plus HDD storage).
var driveTypes = []string{"SSD", "HDD", "NVMe"}

type Join struct{ Table, Owner, Other string }

type Field struct {
	Name     string // column name, or form key for kMulti
	Label    string
	Kind     string
	Required bool
	Options  []string // kSelect, kChecks
	Default  string   // pre-selected value on the "new" form (kSelect)
	Badge    bool     // show the value as a colored badge in lists (kSelect)
	Short    string   // shorter column header for lists; Label is used if empty
	Ref      string   // kRef / kMulti: key of the referenced entity
	Join     *Join    // kMulti
}

// Entity describes one editable table. Adding an entity here gives it list,
// create, edit, delete and revision history with no further code.
type Entity struct {
	Key      string // URL segment and revisions.entity_type
	Table    string
	Singular string
	Plural   string
	OrderBy  string
	// LabelSQL renders a row as a human-readable string when referenced by
	// other entities. It may only use columns of Table.
	LabelSQL string
	Fields   []Field
	// Validate, if set, runs after field parsing for rules that span fields.
	Validate func(*http.Request) error
}

var entities = []*Entity{
	{
		Key: "devices", Table: "devices", Singular: "device", Plural: "Devices",
		OrderBy: "name", LabelSQL: "name",
		Fields: []Field{
			{Name: "name", Label: "Name", Kind: kText, Required: true},
			{Name: "type", Label: "Device type", Kind: kSelect, Required: true, Options: deviceTypes, Default: "Server"},
			{Name: "role", Label: "Role", Kind: kText},
			{Name: "ram", Label: "RAM", Kind: kText},
			{Name: "storage", Label: "Storage capacity", Kind: kText},
			{Name: "drive_types", Label: "Drive types", Kind: kChecks, Options: driveTypes},
			{Name: "vlans", Label: "VLANs", Kind: kMulti, Ref: "vlans",
				Join: &Join{Table: "device_vlans", Owner: "device_id", Other: "vlan_id"}},
			{Name: "notes", Label: "Notes", Kind: kTextarea},
		},
	},
	{
		Key: "vlans", Table: "vlans", Singular: "VLAN", Plural: "VLANs",
		OrderBy: "number", LabelSQL: "'VLAN ' || number || ' - ' || name",
		Fields: []Field{
			{Name: "number", Label: "VLAN number", Kind: kNumber, Required: true},
			{Name: "name", Label: "Name", Kind: kText, Required: true},
			{Name: "subnet", Label: "Subnet", Kind: kText},
			{Name: "gateway", Label: "Gateway", Kind: kText},
			{Name: "purpose", Label: "Purpose", Kind: kTextarea},
		},
	},
	{
		Key: "services", Table: "services", Singular: "service", Plural: "Services",
		OrderBy: "name", LabelSQL: "name",
		Fields: []Field{
			{Name: "name", Label: "Name", Kind: kText, Required: true},
			{Name: "type", Label: "Type", Kind: kSelect, Required: true, Options: []string{"LXC", "VM", "bare-metal"}},
			{Name: "host_device_id", Label: "Host device", Kind: kRef, Ref: "devices"},
			{Name: "vlan_id", Label: "VLAN", Kind: kRef, Ref: "vlans"},
			{Name: "notes", Label: "Notes", Kind: kTextarea},
		},
	},
	{
		Key: "ips", Table: "ip_assignments", Singular: "IP assignment", Plural: "IP assignments",
		OrderBy: "id", LabelSQL: "ip",
		Fields: []Field{
			{Name: "ip", Label: "IP address", Kind: kText, Required: true},
			{Name: "device_id", Label: "Device", Kind: kRef, Ref: "devices"},
			{Name: "service_id", Label: "Service", Kind: kRef, Ref: "services"},
			{Name: "vlan_id", Label: "VLAN", Kind: kRef, Ref: "vlans"},
			{Name: "access_url", Label: "Access URL", Kind: kText},
			{Name: "notes", Label: "Notes", Kind: kTextarea},
		},
	},
	{
		Key: "firewall", Table: "firewall_rules", Singular: "firewall rule", Plural: "Firewall rules",
		OrderBy: "id", LabelSQL: "description",
		Fields: []Field{
			{Name: "from_vlan_id", Label: "From VLAN", Kind: kRef, Ref: "vlans", Required: true},
			{Name: "to_vlan_id", Label: "To VLAN", Kind: kRef, Ref: "vlans", Required: true},
			{Name: "allowed", Label: "Allowed", Kind: kBool},
			{Name: "description", Label: "Description", Kind: kText},
		},
	},
	{
		Key: "connections", Table: "connections", Singular: "connection", Plural: "Connections",
		OrderBy: "device_id", LabelSQL: "device_port", Validate: validateConnection,
		Fields: []Field{
			{Name: "device_id", Label: "Device", Kind: kRef, Ref: "devices", Required: true},
			{Name: "device_port", Label: "Device NIC / port", Short: "Device port", Kind: kText},
			{Name: "switch_id", Label: "Connected to (switch / router)", Short: "Switch", Kind: kRef, Ref: "devices", Required: true},
			{Name: "switch_port", Label: "Switch port", Kind: kText},
			{Name: "mode", Label: "Mode", Kind: kSelect, Required: true, Options: connectionModes, Default: "Access", Badge: true},
			{Name: "untagged_vlan_id", Label: "Untagged VLAN (access VLAN, or native VLAN on a trunk)", Short: "Untagged VLAN", Kind: kRef, Ref: "vlans"},
			{Name: "tagged_vlans", Label: "Tagged VLANs (trunk only)", Short: "Tagged VLANs", Kind: kMulti, Ref: "vlans",
				Join: &Join{Table: "connection_vlans", Owner: "connection_id", Other: "vlan_id"}},
			{Name: "notes", Label: "Notes", Kind: kTextarea},
		},
	},
}

var byKey = func() map[string]*Entity {
	m := make(map[string]*Entity, len(entities))
	for _, e := range entities {
		m[e.Key] = e
	}
	return m
}()

// scalarFields are the fields stored as columns of the entity's own table.
func (e *Entity) scalarFields() []Field {
	var out []Field
	for _, f := range e.Fields {
		if f.Kind != kMulti {
			out = append(out, f)
		}
	}
	return out
}

func (e *Entity) columnNames() []string {
	var names []string
	for _, f := range e.scalarFields() {
		names = append(names, f.Name)
	}
	return names
}

// listFields are the fields shown as table columns (long text is omitted).
func (e *Entity) listFields() []Field {
	var out []Field
	for _, f := range e.Fields {
		if f.Kind != kTextarea {
			out = append(out, f)
		}
	}
	return out
}

// listSQL selects id plus one display string per list field.
func (e *Entity) listSQL() string {
	exprs := []string{"t.id"}
	for _, f := range e.listFields() {
		switch f.Kind {
		case kBool:
			exprs = append(exprs, "CASE t."+f.Name+" WHEN 1 THEN 'yes' ELSE 'no' END")
		case kRef:
			r := byKey[f.Ref]
			exprs = append(exprs, "COALESCE((SELECT "+r.LabelSQL+" FROM "+r.Table+" WHERE id = t."+f.Name+"), '')")
		case kMulti:
			r := byKey[f.Ref]
			exprs = append(exprs, "COALESCE((SELECT group_concat("+r.LabelSQL+", ', ') FROM "+r.Table+
				" WHERE id IN (SELECT "+f.Join.Other+" FROM "+f.Join.Table+" WHERE "+f.Join.Owner+" = t.id)), '')")
		default:
			exprs = append(exprs, "t."+f.Name)
		}
	}
	return "SELECT " + strings.Join(exprs, ", ") + " FROM " + e.Table + " t ORDER BY t." + e.OrderBy + ", t.id"
}

// Header is the column title used in list tables.
func (f Field) Header() string {
	if f.Short != "" {
		return f.Short
	}
	return f.Label
}
