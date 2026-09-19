CREATE TABLE vlans (
    id      INTEGER PRIMARY KEY,
    number  INTEGER NOT NULL UNIQUE,
    name    TEXT NOT NULL,
    subnet  TEXT NOT NULL DEFAULT '',
    gateway TEXT NOT NULL DEFAULT '',
    purpose TEXT NOT NULL DEFAULT ''
);

CREATE TABLE devices (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL DEFAULT '',
    ram        TEXT NOT NULL DEFAULT '',
    storage    TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE device_vlans (
    device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    vlan_id   INTEGER NOT NULL REFERENCES vlans(id) ON DELETE CASCADE,
    PRIMARY KEY (device_id, vlan_id)
);

CREATE TABLE services (
    id             INTEGER PRIMARY KEY,
    name           TEXT NOT NULL,
    type           TEXT NOT NULL CHECK (type IN ('LXC', 'VM', 'bare-metal')),
    host_device_id INTEGER REFERENCES devices(id) ON DELETE SET NULL,
    vlan_id        INTEGER REFERENCES vlans(id) ON DELETE SET NULL,
    notes          TEXT NOT NULL DEFAULT ''
);

-- An assignment belongs to a device or a service (at least one).
CREATE TABLE ip_assignments (
    id         INTEGER PRIMARY KEY,
    device_id  INTEGER REFERENCES devices(id) ON DELETE CASCADE,
    service_id INTEGER REFERENCES services(id) ON DELETE CASCADE,
    ip         TEXT NOT NULL,
    vlan_id    INTEGER REFERENCES vlans(id) ON DELETE SET NULL,
    access_url TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    CHECK (device_id IS NOT NULL OR service_id IS NOT NULL)
);

CREATE TABLE firewall_rules (
    id           INTEGER PRIMARY KEY,
    from_vlan_id INTEGER NOT NULL REFERENCES vlans(id) ON DELETE CASCADE,
    to_vlan_id   INTEGER NOT NULL REFERENCES vlans(id) ON DELETE CASCADE,
    allowed      INTEGER NOT NULL CHECK (allowed IN (0, 1)),
    description  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE pages (
    id            INTEGER PRIMARY KEY,
    slug          TEXT NOT NULL UNIQUE,
    title         TEXT NOT NULL,
    markdown_body TEXT NOT NULL DEFAULT '',
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Written automatically on every create/update/delete; never edited by hand.
CREATE TABLE revisions (
    id            INTEGER PRIMARY KEY,
    entity_type   TEXT NOT NULL,
    entity_id     INTEGER NOT NULL,
    snapshot_json TEXT NOT NULL,
    changed_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    note          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX revisions_entity ON revisions (entity_type, entity_id, id);
