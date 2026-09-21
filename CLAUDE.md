# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

`homelab-docs-mvp-requirements.md` is the source of truth for scope, stack, and data model. Build phases 1-3 are done (schema/migrations, CRUD for all entities, automatic revisions plus a per-entity History page), plus dark mode and (v1.0.0) the topology designer. Not built yet: FTS5 search, Markdown pages, Markdown export. License: GPL-3.0.

**Deliberate scope deviation:** the requirements list a drag-and-drop diagram editor as out of MVP scope. The user explicitly asked for a topology designer with shareable/downloadable images, so it was built (interactive editor, server-rendered SVG, browser-rendered PNG, revocable public share links). Everything else in the out-of-scope list still stands.

## Commands

```bash
CGO_ENABLED=0 go build -o labdoc ./cmd/labdoc   # static build; keep CGO off
go vet ./... && go test ./...                     # tests: internal/db, internal/web (CGO off)
go test ./internal/web -run TestValidation -v   # single test
./labdoc -addr :5380 -db homelab.db             # also LABDOC_ADDR / LABDOC_DB
./labdoc -backup out.db                         # VACUUM INTO copy, safe while running
# stamp version (default lives in internal/version):
go build -ldflags "-s -w -X labdoc/internal/version.Version=X.Y.Z" ./cmd/labdoc
```

## Versioning

Bump `Version` in `internal/version/version.go` with every shipped change (feature = minor, fix = patch) and mention it in the summary. Release tags are `vX.Y.Z`; the SLSA workflow stamps the binary from the tag.

## Documentation

Update `README.md` in the same change whenever build/run commands, flags or env vars, deployment, backup/restore, or user-visible features change.

## Code layout notes

- `internal/web/entities.go` declares each editable entity (fields, kinds, refs). `crud.go` holds the generic list/form/save/delete/history handlers driven by it. To add an entity, add a migration plus one entry there; templates are generic.
- The SQLite pool is capped at one connection. Inside a transaction, only use the `tx`; touching `s.db` deadlocks. Read all rows (`queryAll`) before issuing the next query.
- Validation errors re-render the form with status 200 because htmx does not swap 4xx responses. The UI uses `hx-boost` on `<body>` with full-page handlers.
- Cross-field rules go in `Entity.Validate` (e.g. `validateConnection` in `connections.go`: Access = one untagged VLAN and no tagged ones; Trunk = at least one tagged VLAN, native VLAN not also tagged). Field `Badge`/`Short` control list badges and column headers.
- Field kinds: `kSelect` (one of fixed options), `kChecks` (any subset of fixed options, stored comma-separated in one column in option order, e.g. `SSD,HDD`; rendered as badges in lists), `kMulti` (many-to-many via a join table). Device types (`deviceTypes`) map to topology node types in `deviceTopoKind` (e.g. NAS -> `storage`, Hypervisor node -> `hypervisor`); adding a device type means updating both, plus `topoKinds` if it needs a new node type. Devices created before migration 0003 default to type Server, which is why old imports show routers/switches as SERVER.
- FK cascades (e.g. deleting a VLAN) do not write revisions for the affected child rows.
- Topology (`internal/web/topology.go`, `static/topology.js`): a diagram is one JSON document in `topologies.layout_json`, validated in `topoPayload.validate` on every save (public share pages render it, so keep that the only write path). `renderTopologySVG` (Go) produces the exported/shared image; `topology.js` draws the same shapes in the editor, so change node size/fonts in both. Node types live in `topoKinds` only (each has a `tier` used by Auto layout). PNG export rasterizes the server SVG in the browser (`static/topology-export.js`).
- Diagram links are not Connections records. The editor detects the pair of inventory devices (`linkSides` in topology.js) and links to `/connections/new?device_id=..&switch_id=..&mode=..` (the generic `new` handler prefills single-value fields from the query string). `/inventory.json` lets the open editor refresh on window focus so it sees connections recorded in another tab.
- Topology links have an optional `style` (`access`/`trunk`/`logical`); the effective type comes from `topoLinkType` (Go) / `linkType` (JS): a link touching a logical node (service, or legacy `vlan` box) is always logical (dashed, not a cable). Colors/widths/dashes live in `topoLinkStyle` and `topoLinkTypes` (Go) and `LINK_TYPES` (topology.js), keep them equal. Link labels are drawn in a layer above nodes; in the editor a labeled link has two `data-link` elements (line group + label), so count unique ids when testing.
- VLANs on a diagram are data, not nodes: `layout.vlans` (`{num,name}`) plus per-node `vlans` (numbers, max 6, must exist in `layout.vlans`) drawn as colored tags; a VLAN's color is its index in the number-sorted list (`topoVlanPalette` / `VLAN_PALETTE`, keep equal). Nodes also have `detail` (model/role line) and node size comes from `topoNodeWidth`/`topoNodeHeight` (Go) and `nodeW`/`nodeH` (JS). The `vlan` node kind is `Legacy` (still rendered, hidden from Add node); the editor's "Convert VLAN boxes to tags" replaces those boxes. `layout.hideLabels` turns off all link labels; otherwise every cable is labeled (own label, else its type word) and dashed logical links are not (the legend explains them). The legend (`topoLegend` / `renderLegend`) lists only what the diagram uses.
- Services are their own nodes but `Logical` kinds (`topoKinds[].Logical`, `logical` in the JSON): dashed border, and any link touching a logical node is a dashed logical link (`topoLinkType`/`linkType`), never a cable, whatever `style` says. Import and Auto layout (JS only) place nodes in tiers by `topoKinds[].tier` and add an Internet node above the edge router/firewall; `placeServices` lays each host's services out in a grid (4 columns) under it so 10+ services do not tangle.
- Share links are the only unauthenticated routes (`/share/{token}`, `/share/{token}/image.svg`): 128-bit random token, revocable, `noindex`/`no-referrer`, and the share template must never include app nav or inventory data. Sharing state is not revisioned.
- The topology editor is opened via `hx-boost="false"` links so its scripts run on a full page load (the rest of the UI is htmx-boosted).

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
- **Topology diagram**: originally a static auto-generated SVG; now an interactive designer (see Code layout notes). The exported image is still server-rendered SVG.
- **Deployment**: single binary + systemd unit in an LXC behind Nginx Proxy Manager; auth is Cloudflare Access or one shared-password middleware only. Backup is just copying `homelab.db`.

## Explicitly out of scope for the MVP

Resist these: multi-user/RBAC, real-time collaboration, SNMP/polling/auto-discovery, plugin system, external DB, WYSIWYG editor, mobile app, custom auth system, PDF generation (stretch only).

## Build order

1. Schema + migrations + skeleton → 2. CRUD (devices/VLANs/services/IPs/firewall) → 3. Auto revisions → 4. FTS5 search → 5. Markdown pages + export → 6. Topology SVG (done, as a designer in v1.0.0) → 7. Stretch (PDF, tags, comments).
