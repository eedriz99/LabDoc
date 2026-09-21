package db

import (
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrationsIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := d.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v < 1 {
		t.Fatalf("user_version = %d, err = %v; want >= 1", v, err)
	}
	for _, table := range []string{"vlans", "devices", "device_vlans", "services", "ip_assignments", "firewall_rules", "pages", "revisions", "topologies"} {
		var n int
		if err := d.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must not re-run migrations (they would fail: tables exist).
	d, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := d.Exec("INSERT INTO firewall_rules (from_vlan_id, to_vlan_id, allowed) VALUES (99, 98, 1)"); err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Exec("INSERT INTO vlans (number, name) VALUES (10, 'Mgmt')"); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "it's backup.db")
	if err := Backup(d, dest); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := Backup(d, dest); err == nil {
		t.Fatal("backup must refuse to overwrite an existing file")
	}

	b, err := Open(dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	var name string
	if err := b.QueryRow("SELECT name FROM vlans WHERE number = 10").Scan(&name); err != nil || name != "Mgmt" {
		t.Fatalf("backup contents: name = %q, err = %v", name, err)
	}
}
