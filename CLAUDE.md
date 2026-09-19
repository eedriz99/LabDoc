# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Pre-implementation. The only file is `homelab-docs-mvp-requirements.md`, which is the source of truth for scope, stack, and data model. There is no code, build system, or git repo yet. Update this file (especially a Commands section) once the Go skeleton exists; no build/lint/test commands have been defined yet.

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
