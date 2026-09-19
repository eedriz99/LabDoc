# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

`homelab-docs-mvp-requirements.md` is the source of truth for scope, stack, and data model. Build phases 1-3 are done (schema/migrations, CRUD for all entities, automatic revisions plus a per-entity History page). Not built yet: FTS5 search, Markdown pages, Markdown export, topology SVG. License: GPL-3.0.

## Commands

```bash
CGO_ENABLED=0 go build -o labdoc ./cmd/labdoc   # static build; keep CGO off
go vet ./... && go test ./...                     # tests: internal/db, internal/web (CGO off)
go test ./internal/web -run TestValidation -v   # single test
./labdoc -addr :8080 -db homelab.db             # also LABDOC_ADDR / LABDOC_DB
./labdoc -backup out.db                         # VACUUM INTO copy, safe while running
# stamp version (default lives in internal/version):
go build -ldflags "-s -w -X labdoc/internal/version.Version=X.Y.Z" ./cmd/labdoc
```

## Code layout notes

- `internal/web/entities.go` declares each editable entity (fields, kinds, refs). `crud.go` holds the generic list/form/save/delete/history handlers driven by it. To add an entity, add a migration plus one entry there; templates are generic.
- The SQLite pool is capped at one connection. Inside a transaction, only use the `tx`; touching `s.db` deadlocks. Read all rows (`queryAll`) before issuing the next query.
- Validation errors re-render the form with status 200 because htmx does not swap 4xx responses. The UI uses `hx-boost` on `<body>` with full-page handlers.
- FK cascades (e.g. deleting a VLAN) do not write revisions for the affected child rows.

## What this is

A single-operator homelab documentation tool: one system of record for devices, VLANs, IPs, services, and firewall rules, with automatic revision history. It replaces hand-maintained Word docs (`v1.0`→`v2.4`) that drifted (conflicting management VLAN/IPs, duplicate files disagreeing). Guiding test for any feature: it must map to "record a fact" or "find a fact"; otherwise it is out of MVP.

## Fixed stack decisions (do not swap without asking)

- **Go**, `net/http` + `chi`. No Gin/Echo/Fiber.
- **SQLite via `modernc.org/sqlite`** (pure Go, **no CGO**) so the binary stays static and cross-compiles. Single file `homelab.db`, WAL mode. No Postgres/MySQL.
- **`html/template`**, server-rendered HTML fragments. No JSON API layer for the UI.
- **htmx** for partial swaps; **Pico.css** vendored as one file (no CDN). No npm, node_modules, bundler, or JS framework.
- **Migrations**: plain versioned `.sql` files applied on startup via a small hand-rolled runner (preferred over `golang-migrate`).
- Budgets: idle RSS < 50 MB, binary < 20 MB, startup < 1s, runs in a 1 vCPU / 256 MB LXC. Keep dependencies minimal.

## Architecture to preserve

- **Data model**: Device, VLAN, IPAssignment, Service, FirewallRule, Page (markdown), Revision. See §5 of the requirements for fields.
- **Revisions are automatic**: every create/update/delete of any entity writes a `Revision` (`entity_type`, `entity_id`, `snapshot_json`, `changed_at`, `note`). Never add manual changelog tables. All writes must go through a path that records a revision, so a per-entity History tab can diff snapshots.
- **Search**: SQLite `FTS5` index over devices, services, and pages; a single global search box (highest-value feature).
- **Markdown export**: one action renders all tables + pages into a single `.md` matching the existing document format.
- **Topology diagram**: one static SVG generated server-side from VLAN/device data; not an editor.
- **Deployment**: single binary + systemd unit in an LXC behind Nginx Proxy Manager; auth is Cloudflare Access or one shared-password middleware only. Backup is just copying `homelab.db`.

## Explicitly out of scope for the MVP

Resist these: multi-user/RBAC, real-time collaboration, drag-and-drop diagram editor, SNMP/polling/auto-discovery, plugin system, external DB, WYSIWYG editor, mobile app, custom auth system, PDF generation (stretch only).

## Build order

1. Schema + migrations + skeleton → 2. CRUD (devices/VLANs/services/IPs/firewall) → 3. Auto revisions → 4. FTS5 search → 5. Markdown pages + export → 6. Topology SVG → 7. Stretch (PDF, tags, comments).
