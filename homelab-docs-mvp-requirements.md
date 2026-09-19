# Homelab Documentation Tool — MVP Requirements

## 1. Problem Statement

The current process (Word docs `v1.0` → `v2.4`, manual changelog tables, copy-pasted
IP reference sections) has already produced real drift:

- Management VLAN documented as `VLAN 1`, then `192.168.1.x`, then `10.0.0.x`,
  then corrected to `VLAN 10` two versions later.
- Duplicate device/IP rows across versions that disagree with each other
  (`homelab_architecture_v2_2.docx` exists twice with different router IPs).
- No single source of truth — the "current" state has to be inferred by reading
  the changelog of the newest file.

The MVP exists to fix exactly this: **one running system of record for
devices, VLANs, IPs, and services, with automatic history — not another
document to keep in sync by hand.**

## 2. MVP Goals

- Single place to record: hardware inventory, VLANs/subnets, IP assignments,
  service placement, firewall/ACL rules.
- Every edit is versioned automatically (no manual changelog tables).
- Fast text search across the whole lab (find "which VLAN is Vaultwarden on").
- Runs comfortably on the weakest node in the lab (an 8GB Optiplex also running
  other LXCs) — this is a background utility, not a primary workload.
- Exportable to plain Markdown so it's still readable without the tool.

## 3. Explicit Boundaries — What This Is NOT

To keep the MVP minimal, the following are **out of scope** and should be
actively resisted if they creep in during development:

| Not in MVP | Why |
|---|---|
| Multi-user accounts / RBAC | Single operator lab; add later only if needed |
| Real-time collaboration | No second user to collaborate with |
| Visual network diagram editor (drag-and-drop) | High effort, low payoff at v1 — auto-generate a simple diagram from data instead |
| Live device polling / SNMP / auto-discovery | Turns this into a monitoring tool (that's Uptime Kuma / Grafana's job) |
| Plugin/extension system | Premature abstraction |
| External database (Postgres/MySQL) | Adds a service dependency for a single-user tool |
| Full WYSIWYG editor | Markdown textarea + preview is enough |
| Mobile app | Responsive web page is enough |
| Auth beyond a single shared password / reverse-proxy auth | It sits behind Nginx Proxy Manager / Cloudflare Access already |
| PDF generation | Markdown export is enough; PDF can be a stretch goal |

If a feature request doesn't map to "record a fact" or "find a fact," it
belongs in a later phase, not the MVP.

## 4. Tech Stack (minimal-footprint by design)

### Backend — Go
- **Language**: Go (single static binary, no runtime to install on the LXC).
- **Router**: `net/http` + `chi` (small, no reflection-heavy magic, keeps
  binary size and complexity down). Avoid full frameworks (Gin/Echo/Fiber) —
  not needed at this scale.
- **Database**: SQLite via `modernc.org/sqlite` (pure Go, **no CGO**, so the
  binary stays fully static and cross-compiles trivially). One file:
  `homelab.db`.
- **Templating**: Go's built-in `html/template`. Server-rendered HTML
  fragments — no JSON API layer to maintain for the UI itself.
- **Migrations**: plain versioned `.sql` files run on startup (e.g. via
  `golang-migrate` or a ~30-line hand-rolled runner — prefer hand-rolled to
  avoid another dependency at this scale).

### Frontend — light, server-driven
- **htmx**: for partial page swaps (edit-in-place, search-as-you-type) without
  a JS build step, bundler, or SPA framework.
- **Pico.css** (or similar classless/minimal CSS): vendored as a single file,
  no CDN dependency, no Tailwind build pipeline.
- No React/Vue/Svelte, no npm, no `node_modules`. The entire frontend is
  static assets served by the Go binary.

### Why this combination
- Zero external runtime dependencies (no Node, no Python, no JVM).
- One binary + one SQLite file = trivial backup (fits directly into the
  existing PBS/Nextcloud backup story) and trivial restore.
- Idle RAM footprint target: **< 50 MB**. Binary size target: **< 20 MB**.
- Fits as one more LXC (or even a container on an existing LXC) without
  competing for resources with Nextcloud, Grafana, etc.

## 5. Data Model (MVP)

```
Device        (id, name, role, ram, storage, vlan_ids[], notes, created_at)
VLAN          (id, number, name, subnet, gateway, purpose)
IPAssignment  (id, device_id/service_id, ip, vlan_id, access_url, notes)
Service       (id, name, type[LXC/VM/bare-metal], host_device_id, vlan_id, notes)
FirewallRule  (id, from_vlan_id, to_vlan_id, allowed:bool, description)
Page          (id, slug, title, markdown_body, updated_at)   -- free-text docs
Revision      (id, entity_type, entity_id, snapshot_json, changed_at, note)
```

- `Revision` is written automatically on every create/update/delete — this
  **replaces** the manual changelog table pattern in the existing docs.
- `Page` covers anything that doesn't fit the structured tables (topology
  narrative, CCNA roadmap, storage strategy notes) as plain Markdown.

## 6. Core Features (MVP scope only)

1. **Inventory CRUD** — devices, VLANs, IPs, services, firewall rules via
   simple forms (htmx partial swaps, no full page reloads).
2. **Automatic revision history** — every entity edit stores a diffable
   snapshot; a "History" tab per entity shows what changed and when,
   generated from data, never typed by hand.
3. **Global search** — single search box, SQLite `FTS5` full-text index over
   devices/services/pages. This is the single highest-value feature relative
   to effort.
4. **Markdown pages** — freeform docs (topology notes, CCNA roadmap) with
   live preview, stored like everything else with revision history.
5. **Markdown export** — one button that renders the current state (all
   tables + pages) into a single `.md` file, matching the existing document
   format, for offline reading or pasting into GitHub.
6. **Simple auto-diagram** — one static SVG generated server-side from
   VLAN/device data (boxes + lines), not an editor. Good enough to replace
   the manual topology diagrams in the existing docs.

## 7. Non-Functional Requirements

| Requirement | Target |
|---|---|
| Idle memory | < 50 MB RSS |
| Binary size | < 20 MB |
| Startup time | < 1s (SQLite embedded, no external DB handshake) |
| Deployment | Single LXC, 1 vCPU, 256 MB RAM cap is sufficient |
| Storage | SQLite file, WAL mode, nightly copy into existing PBS backup job |
| Dependencies at runtime | None — single static binary + one `.db` file |
| Access | Behind existing Nginx Proxy Manager reverse proxy; auth via Cloudflare Access or a single shared password middleware — no auth system to build |

## 8. Deployment Plan (fits the existing lab)

- Ship as a single compiled binary + `systemd` unit, run inside its own
  lightweight LXC (or share an existing low-load LXC such as the one running
  Uptime Kuma).
- Reverse-proxied through the existing Nginx Proxy Manager instance —
  no new TLS/cert handling needed.
- Backed up by adding the `homelab.db` file path to the existing PBS backup
  job — no new backup tooling.
- Optional: expose read-only via the existing Cloudflare Tunnel for
  reference while away from the lab.

## 9. Suggested Build Order (phases)

1. SQLite schema + migrations + Go project skeleton.
2. Device/VLAN/Service/IP CRUD (server-rendered forms, htmx swaps).
3. Automatic revision snapshots on every write.
4. Full-text search (FTS5) across all entities.
5. Markdown pages + export-to-Markdown.
6. Auto-generated topology SVG.
7. (Stretch, post-MVP) PDF export, tagging, per-entity comments.

## 10. Definition of Done for MVP

- All hardware/VLAN/IP/service data from the current `v2.4` document can be
  entered once and never re-typed — future changes are edits, not new files.
- Search returns the right answer for "what IP is Vaultwarden on" in under
  a second.
- History view shows exactly what changed and when for any entity, with no
  manual changelog maintenance.
- The whole app runs, backed up, and reverse-proxied within the existing
  lab's resource envelope without displacing any current service.
