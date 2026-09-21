package web

import (
	"net/url"
	"strings"
	"testing"
)

// seedNetwork creates VLAN 10 (id 1), VLAN 30 (id 2), a server (id 1) and a switch (id 2).
func seedNetwork(e *env) {
	e.created("/vlans", vlan("10", "Management"))
	e.created("/vlans", vlan("30", "Services"))
	e.created("/devices", device("hypervisor", "Server"))
	e.created("/devices", device("sw1", "Switch"))
}

func conn(mode string, untagged string, tagged ...string) url.Values {
	v := url.Values{
		"device_id": {"1"}, "device_port": {"eno1"},
		"switch_id": {"2"}, "switch_port": {"Gi1/0/5"},
		"mode": {mode},
	}
	if untagged != "" {
		v.Set("untagged_vlan_id", untagged)
	}
	if len(tagged) > 0 {
		v["tagged_vlans"] = tagged
	}
	return v
}

func TestTrunkCarriesMultipleVLANsOnOneNIC(t *testing.T) {
	e := newEnv(t)
	seedNetwork(e)

	// One NIC carrying VLAN 10 (hypervisor management) and VLAN 30 (services).
	e.created("/connections", conn("Trunk", "", "1", "2"))
	if n := e.count("SELECT count(*) FROM connection_vlans WHERE connection_id = 1"); n != 2 {
		t.Fatalf("tagged VLANs = %d, want 2", n)
	}

	_, list := e.get("/connections")
	for _, want := range []string{"hypervisor", "eno1", "sw1", "Gi1/0/5", `class="tag tag-trunk"`, "VLAN 10 - Management", "VLAN 30 - Services"} {
		if !strings.Contains(list, want) {
			t.Errorf("connections list missing %q", want)
		}
	}

	_, form := e.get("/connections/1/edit")
	if !strings.Contains(form, `name="tagged_vlans" value="1" checked`) || !strings.Contains(form, `name="tagged_vlans" value="2" checked`) {
		t.Error("edit form should pre-check both tagged VLANs")
	}
	if !strings.Contains(form, `<option value="Trunk" selected>`) {
		t.Error("edit form should select Trunk")
	}
}

func TestAccessAndNativeVLAN(t *testing.T) {
	e := newEnv(t)
	seedNetwork(e)
	e.created("/devices", device("printer", "Other"))

	// Access: exactly one untagged VLAN.
	a := conn("Access", "1")
	a.Set("device_id", "3")
	a.Set("device_port", "")
	a.Set("switch_port", "Gi1/0/9")
	e.created("/connections", a)
	_, list := e.get("/connections")
	if !strings.Contains(list, `class="tag tag-access"`) {
		t.Error("access connection should show an Access badge")
	}

	// Trunk with a native (untagged) VLAN plus tagged VLAN.
	e.created("/connections", conn("Trunk", "1", "2"))
	var untagged int
	if err := e.db.QueryRow("SELECT untagged_vlan_id FROM connections WHERE mode = 'Trunk'").Scan(&untagged); err != nil || untagged != 1 {
		t.Fatalf("native VLAN = %d (err %v), want 1", untagged, err)
	}
}

func TestConnectionValidation(t *testing.T) {
	e := newEnv(t)
	seedNetwork(e)
	e.created("/connections", conn("Trunk", "", "1", "2")) // occupies sw1 Gi1/0/5 and hypervisor eno1

	self := conn("Access", "1")
	self.Set("switch_id", "1")
	sameSwitchPort := conn("Access", "1")
	sameSwitchPort.Set("device_port", "eno2")
	sameDevicePort := conn("Access", "1")
	sameDevicePort.Set("switch_port", "Gi1/0/6")
	noSwitch := conn("Access", "1")
	noSwitch.Del("switch_id")

	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"access without a VLAN", conn("Access", ""), "access port needs its VLAN"},
		{"access with tagged VLANs", conn("Access", "1", "2"), "single VLAN"},
		{"trunk without tagged VLANs", conn("Trunk", "1"), "at least one tagged VLAN"},
		{"native also tagged", conn("Trunk", "1", "1", "2"), "cannot also be tagged"},
		{"connect to itself", self, "cannot connect to itself"},
		{"switch port already cabled", sameSwitchPort, "port is already used"},
		{"device port already cabled", sameDevicePort, "port is already used"},
		{"unknown mode", conn("Bonded", "1"), "invalid value"},
		{"missing switch", noSwitch, "required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := e.post("/connections", tc.form)
			if code != 200 || !strings.Contains(body, tc.want) {
				t.Fatalf("status %d, want 200 containing %q", code, tc.want)
			}
		})
	}
	if n := e.count("SELECT count(*) FROM connections"); n != 1 {
		t.Fatalf("rejected connections were stored: %d rows", n)
	}
	// A rejected form keeps what the user selected.
	_, body := e.post("/connections", conn("Access", "1", "2"))
	if !strings.Contains(body, `name="tagged_vlans" value="2" checked`) {
		t.Error("tagged VLAN selection lost after validation error")
	}
}

func TestConnectionHistoryAndCascade(t *testing.T) {
	e := newEnv(t)
	seedNetwork(e)
	e.created("/connections", conn("Access", "1"))
	e.created("/connections/1", conn("Trunk", "1", "2"))

	_, hist := e.get("/connections/1/history")
	for _, want := range []string{"mode: Access → Trunk", "tagged_vlans: [] → [2]"} {
		if !strings.Contains(hist, want) {
			t.Errorf("history missing %q", want)
		}
	}

	// Deleting the switch removes its connections (and their VLAN links).
	if code, _ := e.post("/devices/2/delete", nil); code != 200 {
		t.Fatalf("delete switch = %d", code)
	}
	if n := e.count("SELECT count(*) FROM connections"); n != 0 {
		t.Errorf("connections after deleting the switch = %d, want 0", n)
	}
	if n := e.count("SELECT count(*) FROM connection_vlans"); n != 0 {
		t.Errorf("connection_vlans after cascade = %d, want 0", n)
	}
}

func TestNVMeDriveType(t *testing.T) {
	e := newEnv(t)
	e.created("/devices", device("nas", "NAS", "HDD", "NVMe", "SSD"))
	var s string
	if err := e.db.QueryRow("SELECT drive_types FROM devices").Scan(&s); err != nil || s != "SSD,HDD,NVMe" {
		t.Fatalf("drive_types = %q (err %v), want SSD,HDD,NVMe", s, err)
	}
	if _, list := e.get("/devices"); !strings.Contains(list, "tag-nvme") {
		t.Error("list should highlight NVMe")
	}
}

func TestTopologyLinkStylesAndConnectionInventory(t *testing.T) {
	e := newEnv(t)
	seedNetwork(e)
	e.created("/connections", conn("Trunk", "", "1", "2"))
	newTopo(e, "Lab")

	// The editor gets connection data (with VLAN numbers) for import.
	_, page := e.get("/topologies/1")
	for _, want := range []string{`"connections":[{"device":1,"switch":2,"mode":"Trunk","untagged":0,"tagged":[1,2]}]`, `"num":10`, `"num":30`} {
		if !strings.Contains(page, want) {
			t.Errorf("editor inventory missing %s", want)
		}
	}

	d := m{
		"name": "Lab",
		"nodes": []m{
			{"id": "n1", "kind": "server", "label": "hypervisor", "x": 0, "y": 0},
			{"id": "n2", "kind": "switch", "label": "sw1", "x": 300, "y": 0},
			{"id": "n3", "kind": "client", "label": "printer", "x": 300, "y": 200},
		},
		"links": []m{
			{"id": "l1", "from": "n1", "to": "n2", "label": "Trunk: 10,30", "style": "trunk"},
			{"id": "l2", "from": "n3", "to": "n2", "label": "Access: 10", "style": "access"},
		},
	}
	if code, msg := e.postJSON("/topologies/1/save", d); code != 200 {
		t.Fatalf("save = %d %s", code, msg)
	}
	_, svg := e.get("/topologies/1/image.svg")
	if !strings.Contains(svg, `stroke="#7c3aed" stroke-width="4"`) {
		t.Error("trunk link should be drawn thick and purple")
	}
	if !strings.Contains(svg, `stroke="#15803d" stroke-width="2"`) {
		t.Error("access link should be drawn green")
	}

	d["links"] = []m{{"id": "l1", "from": "n1", "to": "n2", "style": "wireless"}}
	if code, _ := e.postJSON("/topologies/1/save", d); code != 400 {
		t.Errorf("unknown link type = %d, want 400", code)
	}
	// Links without a style still render as plain grey.
	d["links"] = []m{{"id": "l1", "from": "n1", "to": "n2"}}
	if code, _ := e.postJSON("/topologies/1/save", d); code != 200 {
		t.Fatal("styleless link should be accepted")
	}
	if _, svg := e.get("/topologies/1/image.svg"); !strings.Contains(svg, `stroke="#64748b" stroke-width="2"`) {
		t.Error("plain link should be grey")
	}
}

func TestConnectionFormPrefilledFromQuery(t *testing.T) {
	e := newEnv(t)
	seedNetwork(e)

	// The topology editor links here from a diagram line.
	_, form := e.get("/connections/new?device_id=1&switch_id=2&mode=Trunk")
	for _, want := range []string{
		`<option value="1" selected>hypervisor</option>`,
		`<option value="2" selected>sw1</option>`,
		`<option value="Trunk" selected>`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("prefilled form missing %s", want)
		}
	}
	// Without a query the form still defaults to Access and selects nothing else.
	_, plain := e.get("/connections/new")
	if !strings.Contains(plain, `<option value="Access" selected>`) || strings.Contains(plain, `value="1" selected`) {
		t.Error("plain form should default to Access with no device preselected")
	}
	// Multi-value fields are not prefilled from the query string.
	_, tagged := e.get("/connections/new?tagged_vlans=1")
	if strings.Contains(tagged, `name="tagged_vlans" value="1" checked`) {
		t.Error("tagged_vlans must not be prefilled from the query")
	}
}

func TestInventoryEndpointReflectsNewConnections(t *testing.T) {
	e := newEnv(t)
	seedNetwork(e)
	_, before := e.get("/inventory.json")
	if !strings.Contains(before, `"connections":[]`) {
		t.Fatalf("expected no connections yet: %s", before)
	}
	e.created("/connections", conn("Trunk", "", "1", "2"))
	_, after := e.get("/inventory.json")
	if !strings.Contains(after, `"connections":[{"device":1,"switch":2,"mode":"Trunk"`) {
		t.Errorf("inventory should include the new connection: %s", after)
	}
	if got := e.header("/inventory.json", "Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}
