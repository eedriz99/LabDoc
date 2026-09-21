package web

import (
	"net/url"
	"strings"
	"testing"
)

func device(name, typ string, drives ...string) url.Values {
	v := url.Values{"name": {name}, "type": {typ}}
	if len(drives) > 0 {
		v["drive_types"] = drives
	}
	return v
}

func TestDeviceTypesIncludeNetworkGear(t *testing.T) {
	e := newEnv(t)
	for _, typ := range []string{"Switch", "Router", "Firewall", "Access point", "PC / Workstation"} {
		e.created("/devices", device("dev-"+typ, typ))
	}
	_, list := e.get("/devices")
	for _, want := range []string{"Switch", "Router", "Firewall", "Access point", "PC / Workstation"} {
		if !strings.Contains(list, want) {
			t.Errorf("device list missing type %q", want)
		}
	}

	// New form offers every type with Server pre-selected.
	_, form := e.get("/devices/new")
	if !strings.Contains(form, `<option value="Server" selected>`) {
		t.Error("new device form should pre-select Server")
	}
	if !strings.Contains(form, `value="Firewall"`) {
		t.Error("new device form missing Firewall option")
	}

	if code, body := e.post("/devices", device("bad", "Toaster")); code != 200 || !strings.Contains(body, "invalid value") {
		t.Errorf("unknown type = %d, want 200 with error", code)
	}
	if code, body := e.post("/devices", url.Values{"name": {"notype"}}); code != 200 || !strings.Contains(body, "Device type is required") {
		t.Errorf("missing type = %d, want 200 with error", code)
	}
}

func TestDriveTypesCombineAndHighlight(t *testing.T) {
	e := newEnv(t)
	e.created("/devices", device("ssd-only", "Server", "SSD"))
	e.created("/devices", device("hdd-only", "NAS", "HDD"))
	// Posted in reverse order: stored canonically as SSD,HDD.
	e.created("/devices", device("mixed", "Server", "HDD", "SSD"))
	e.created("/devices", device("switch1", "Switch"))

	stored := func(name string) string {
		var s string
		if err := e.db.QueryRow("SELECT drive_types FROM devices WHERE name = ?", name).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for name, want := range map[string]string{"ssd-only": "SSD", "hdd-only": "HDD", "mixed": "SSD,HDD", "switch1": ""} {
		if got := stored(name); got != want {
			t.Errorf("%s drive_types = %q, want %q", name, got, want)
		}
	}

	_, list := e.get("/devices")
	if !strings.Contains(list, `class="tag tag-ssd"`) || !strings.Contains(list, `class="tag tag-hdd"`) {
		t.Error("list should highlight SSD and HDD as badges")
	}
	// The mixed device shows both badges in its row.
	row := list[strings.Index(list, "mixed"):]
	row = row[:strings.Index(row, "</tr>")]
	if !strings.Contains(row, "tag-ssd") || !strings.Contains(row, "tag-hdd") {
		t.Errorf("mixed device row should show both badges:\n%s", row)
	}

	// Edit form pre-checks the stored selection.
	_, form := e.get("/devices/3/edit")
	if !strings.Contains(form, `name="drive_types" value="SSD" checked`) || !strings.Contains(form, `name="drive_types" value="HDD" checked`) {
		t.Error("edit form should pre-check both drive types")
	}
	_, form = e.get("/devices/1/edit")
	if !strings.Contains(form, `name="drive_types" value="SSD" checked`) || strings.Contains(form, `name="drive_types" value="HDD" checked`) {
		t.Error("edit form for the SSD-only device should check only SSD")
	}

	// Changing the combination is recorded in history; re-saving the same selection is not.
	e.created("/devices/1", device("ssd-only", "Server", "SSD", "HDD"))
	e.created("/devices/1", device("ssd-only", "Server", "HDD", "SSD"))
	if n := e.count("SELECT count(*) FROM revisions WHERE entity_type = 'devices' AND entity_id = 1"); n != 2 {
		t.Fatalf("revisions = %d, want 2 (create + one change)", n)
	}
	_, hist := e.get("/devices/1/history")
	if !strings.Contains(hist, "drive_types: SSD → SSD,HDD") {
		t.Error("history should show the drive type change")
	}

	// Clearing all boxes is allowed (drive types are optional).
	e.created("/devices/1", device("ssd-only", "Server"))
	if got := stored("ssd-only"); got != "" {
		t.Errorf("cleared drive_types = %q, want empty", got)
	}

	if code, body := e.post("/devices", device("bad", "Server", "Tape")); code != 200 || !strings.Contains(body, "invalid value") {
		t.Errorf("unknown drive type = %d, want 200 with error", code)
	}
}

func TestFormErrorKeepsDriveSelection(t *testing.T) {
	e := newEnv(t)
	// Missing name re-renders the form; the checked drives must survive.
	_, body := e.post("/devices", url.Values{"type": {"Server"}, "drive_types": {"SSD", "HDD"}})
	if !strings.Contains(body, "Name is required") {
		t.Fatal("expected a name error")
	}
	if !strings.Contains(body, `name="drive_types" value="SSD" checked`) || !strings.Contains(body, `name="drive_types" value="HDD" checked`) {
		t.Error("drive selection lost after validation error")
	}
}

func TestTopologyImportUsesDeviceType(t *testing.T) {
	e := newEnv(t)
	for name, typ := range map[string]string{"sw1": "Switch", "gw": "Router", "fw": "Firewall", "ap1": "Access point", "pc1": "PC / Workstation", "box": "Server"} {
		e.created("/devices", device(name, typ))
	}
	newTopo(e, "Lab")
	_, page := e.get("/topologies/1")
	for label, kind := range map[string]string{"sw1": "switch", "gw": "router", "fw": "firewall", "ap1": "ap", "pc1": "client", "box": "server"} {
		want := `"label":"` + label + `","kind":"` + kind + `"`
		if !strings.Contains(page, want) {
			t.Errorf("inventory JSON missing %s", want)
		}
	}
}
