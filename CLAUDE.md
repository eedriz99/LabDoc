# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

`homelab-docs-mvp-requirements.md` is the source of truth for scope, stack, and data model. Build phases 1-3 are done (schema/migrations, CRUD for all entities, automatic revisions plus a per-entity History page), plus dark mode, (v1.0.0) the topology designer, (v1.9.0, hardened in v1.9.1 with a CSRF layer) built-in login, (v1.10.0) a grouped/dropdown nav, and (v1.11.0) optional email verification + password reset. Not built yet: FTS5 search, Markdown pages, Markdown export. License: GPL-3.0.

**Deliberate scope deviations:**
- The requirements list a drag-and-drop diagram editor as out of MVP scope. The user explicitly asked for a topology designer with shareable/downloadable images, so it was built (interactive editor, server-rendered SVG, browser-rendered PNG, revocable public share links).
- The out-of-scope list also names "custom auth system" and "multi-user/RBAC", and the fixed stack decisions originally described auth as an external concern (Cloudflare Access or a single shared-password middleware). The user explicitly asked for a signup page plus a security layer locking down the static endpoint, so `internal/web/auth.go` adds a self-contained, AdGuard Home-style login: bcrypt-hashed accounts and server-side sessions in SQLite, a one-time `/setup` page for the first account, and a session-cookie middleware in front of every route (including `/static/*`). It deliberately stops short of the out-of-scope RBAC: every account has identical, single-tier access, so this is a login gate, not a permissions system.

Everything else in the out-of-scope list still stands.

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
- Share links are the only unauthenticated *inventory* routes (`/share/{token}`, `/share/{token}/image.svg`): 128-bit random token, revocable, `noindex`/`no-referrer`, and the share template must never include app nav or inventory data. Sharing state is not revisioned.
- The topology editor is opened via `hx-boost="false"` links so its scripts run on a full page load (the rest of the UI is htmx-boosted).
- Nav (`layout.html`): the entity tabs (`.Nav`, i.e. `entities`) are grouped under one "Inventory" dropdown; Topology stays a top-level link (a distinct feature, not a CRUD entity); the signed-in username is its own dropdown (Account, Log out). Dropdowns are plain `<details class="navgroup"><summary>...<ul class="navmenu">`, no JS framework, matching the `<details>` already used for the topology editor's VLAN tag manager; a small script in `layout.html` only adds what the native element doesn't (accordion between the two dropdowns, close on outside click/Escape/choosing a menu item, and marking a dropdown's summary current when one of its own links is). Add a new top-level tab as another `<li>` there, or a new entity to the `{{range .Nav}}` loop to have it picked up by the Inventory dropdown automatically.
- Auth (`internal/web/auth.go`): `sessionAuth` middleware wraps the whole router in `server.go` and blocks everything, including `/static/*` (no unauthenticated directory listing either), except an exact-path allowlist (`publicExact`: `/login`, `/setup`, `/healthz`, and the two static files the login/setup pages render with, plus `topology-export.js` for the public share page) and the `/share/` prefix. `/setup` additionally 404s-via-redirect once `users` is non-empty, regardless of the allowlist, so it can't be used to create a second account anonymously. Sessions are opaque tokens in the `sessions` table (migration `0005_auth.sql`), not JWTs; `userFromContext` reads the authenticated `*authUser` that the middleware attaches. `newBase` (crud.go) and every page's `base` need a `*http.Request` now so templates can show the signed-in username and CSRF token — thread `r` through if you add a handler. Password rules and the failed-login rate limiter (`loginLimiter`, in-memory, 5 attempts / 15 min lockout) live in the same file.
- CSRF (`internal/web/auth.go`, `csrfProtect`): a second, independent layer on top of the session cookie's `SameSite=Lax`, so a proxy stripping that attribute or an older browser ignoring it doesn't fully undo the protection. A separate, HttpOnly `labdoc_csrf` cookie (double-submit token, unrelated to the session) is set on every response that lacks one — including redirects, and including `/login`/`/setup` — and stashed in the request context (`csrfFromContext`) for the page being rendered to embed. Every POST/PUT/PATCH/DELETE must echo that value back, as the hidden `csrf_token` field every `<form>` now carries or an `X-CSRF-Token` header, or it gets a 403; a mismatch never reaches a handler. htmx-driven requests (hx-boost, `hx-post` delete links) get the header automatically from a `htmx:configRequest` listener in `layout.html` reading a `<meta name="csrf-token">` tag — add that listener's equivalent, or a hidden field, for any new form. The topology editor's raw `fetch()` calls (`static/topology.js`) are the one place nothing does this automatically; they read the same meta tag via a local `csrfToken()` helper — do the same for any new fetch call there. Test helpers (`env.csrfToken()`/`env.post()` in crud_test.go, `postJSON` in topology_test.go) inject a valid token transparently; `TestCSRFRejectsMissingOrWrongToken` in auth_test.go is the one test that deliberately bypasses that to exercise rejection.
- Email verification + password reset (`internal/web/email.go`, `mail.go`; migration `0006_email.sql`): an account's `email`/`email_verified_at` are optional and orthogonal to login — nothing about signing in requires either. `email_tokens` (`purpose` = `verify` or `reset`) is single-use and short-lived (`verifyTokenTTL` 24h, `resetTokenTTL` 1h); `issueEmailToken` deletes any prior token of that purpose for the user first, so only the most recently sent link works. `lookupEmailToken` doesn't consume, so a GET (loading `/reset-password/{token}`) or a rejected form (bad confirm) doesn't burn the link — only `verifyEmail`'s success path and `resetPasswordPost`'s success path call `deleteEmailToken`. A successful reset also deletes every session for that user (`DELETE FROM sessions WHERE user_id = ?`), since the old password may be compromised. `forgotPasswordPost` never reveals whether an address matched an account (same response either way) and rate-limits by IP (`s.resetLimiter`, a second `loginLimiter` instance) rather than by address. `MailConfig` (`mail.go`) wraps `net/smtp` directly — no mail dependency — and reports `configured() == false` (the zero value) as a friendly error rather than silently no-op'ing; wired from `-smtp-*` flags in `cmd/labdoc/main.go` via `Server.SetMail`, called after `web.New`. `requestBaseURL` builds mailed links' origin from the triggering request's Host/X-Forwarded-* headers, not a dedicated flag. `/verify-email/{token}` and `/reset-password/{token}` are public by token, like `/share/`, not by session (see `publicPrefixes`); `notice.html` is the standalone landing page for a token that turns out invalid/expired, reachable whether or not the visitor has a session. Tests fake SMTP with a minimal hand-rolled server (`startFakeSMTP` in email_test.go) rather than mocking `MailConfig`, so the real `net/smtp` codepath is exercised.

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
- **Deployment**: single binary + systemd unit in an LXC behind Nginx Proxy Manager; auth is LabDoc's own login (session cookie + bcrypt accounts, see Code layout notes), optionally layered with Cloudflare Access or proxy-level auth for defense in depth. Backup is just copying `homelab.db`.

## Explicitly out of scope for the MVP

Resist these: multi-user/RBAC, real-time collaboration, SNMP/polling/auto-discovery, plugin system, external DB, WYSIWYG editor, mobile app, custom auth system, PDF generation (stretch only).

## Build order

1. Schema + migrations + skeleton → 2. CRUD (devices/VLANs/services/IPs/firewall) → 3. Auto revisions → 4. FTS5 search → 5. Markdown pages + export → 6. Topology SVG (done, as a designer in v1.0.0) → 7. Stretch (PDF, tags, comments).
