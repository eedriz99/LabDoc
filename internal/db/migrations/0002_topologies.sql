-- A topology is one saved diagram. Nodes and links live in a single JSON
-- document (layout_json) because they are always read and written together.
-- share_token is NULL unless the diagram is shared via a public read-only link.
CREATE TABLE topologies (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    layout_json TEXT NOT NULL DEFAULT '{"nodes":[],"links":[]}',
    share_token TEXT UNIQUE,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
