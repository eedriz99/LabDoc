-- A physical/logical link between a device and a switch (or router) port.
-- Access: carries exactly one untagged VLAN (untagged_vlan_id).
-- Trunk:  carries one or more tagged VLANs (connection_vlans) and optionally a
--         native/untagged VLAN (untagged_vlan_id).
CREATE TABLE connections (
    id               INTEGER PRIMARY KEY,
    device_id        INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    device_port      TEXT NOT NULL DEFAULT '',
    switch_id        INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    switch_port      TEXT NOT NULL DEFAULT '',
    mode             TEXT NOT NULL CHECK (mode IN ('Access', 'Trunk')),
    untagged_vlan_id INTEGER REFERENCES vlans(id) ON DELETE SET NULL,
    notes            TEXT NOT NULL DEFAULT '',
    CHECK (device_id <> switch_id)
);

-- A physical port can only be cabled once (ports left blank are not constrained).
CREATE UNIQUE INDEX connections_switch_port ON connections (switch_id, switch_port) WHERE switch_port <> '';
CREATE UNIQUE INDEX connections_device_port ON connections (device_id, device_port) WHERE device_port <> '';

CREATE TABLE connection_vlans (
    connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    vlan_id       INTEGER NOT NULL REFERENCES vlans(id) ON DELETE CASCADE,
    PRIMARY KEY (connection_id, vlan_id)
);
