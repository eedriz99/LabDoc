# LabDoc

A single-operator homelab documentation tool: one system of record for devices, VLANs, IP assignments, services, and firewall rules, with automatic revision history instead of hand-kept changelogs.

It is a web app: one static Go binary plus one SQLite file. No runtime dependencies, no Node, no external database.

## Features

- Create, edit, and delete devices, VLANs, services, IP assignments, and firewall rules from the browser.
- Devices have a **type** (server, PC / workstation, laptop, NAS, switch, router, firewall, access point, other) and optional **drive types**: tick any of SSD, HDD, and NVMe (combine them freely). Drive types show as colored badges in the device list. Devices that existed before v1.1.0 were set to "Server", so change your switches, routers, and firewalls to their real type.
- **Connections:** record how each device plugs into a switch or router: device NIC and switch port, and whether the link is an **Access** port (one untagged VLAN) or a **Trunk** (one or more tagged VLANs, plus an optional native VLAN). One NIC carrying, say, VLAN 10 for hypervisor management and VLAN 30 for services is a single trunk connection. A switch or device port can only be used once.
- Automatic revision history: every change is versioned, and each record has a History page showing what changed and when.
- Light and dark themes: follows your OS setting by default, with a toggle in the nav bar (choice is remembered per browser).
- Grouped nav: the record types live under one **Inventory** dropdown, and your account under a second one, so the top bar stays to three items (Inventory, Topology, your username) instead of a long row of tabs.
- Consistent online backup via `-backup`, and a version shown in the page footer.
- **Network topology designer:** drag-and-drop diagrams (routers, firewalls, switches, servers, VLANs, and more) with labeled links. Add nodes from your inventory (or import it all: a device becomes a switch, router, firewall, etc. node according to its type, arranged in tiers under an Internet node, devices carry their VLANs as tags, services are dashed boxes linked to their host, and each Connection becomes a link labeled like `Trunk: 10,30 (native 1)` or `Access: 20`), then download as SVG or PNG or share a read-only link.
- **Built-in login:** a session-cookie gate protects every route, including static assets — nothing but the login/setup pages, `/healthz`, and public share links is reachable without signing in. First run redirects to a one-time setup page that creates the operator account; more accounts (all with equal access, no roles) can be added from **Account**. Failed logins are rate-limited per IP.
- **Password reset by email (optional):** add an email under **Account** and confirm it via a mailed link; once verified, "Forgot your password?" on the login page emails a one-hour reset link. Needs SMTP configured (see below) — without it, LabDoc works exactly as before, just without this recovery path.

Not built yet: full-text search, and Markdown pages and export.

## Topology designer

Open **Topology** in the nav, create a diagram, and:

- **Add node** places a node of the chosen type. Click it to rename it or change its type, and drag it to move it (positions snap to a grid).
- **Link mode** connects two nodes: click one, then the other. Click a link to label it, set its **line type** (cable, access cable = thin green, trunk cable = thick purple, logical link = dashed grey), or delete it. **Delete** also works with the Delete key.
- **VLANs are tags, not boxes.** A VLAN is a logical group, not something you plug into, so it is drawn as a colored `VLAN 30` tag on the devices that belong to it. Manage the diagram's VLANs under **VLAN tags** and tick them on a selected device. Old diagrams that draw VLANs as boxes show a **Convert VLAN boxes to tags** button.
- **Services are dashed.** A service (an app) is not equipment, so its box has a dashed border and connects to the device it runs on with a dashed line; solid lines are always cables. Auto layout puts each device's services in a grid under it, so a host running 10+ services stays readable.
- **A key on every image.** Exports and the editor show a legend for the line styles, VLAN tag colors, and device types the diagram uses. **Link labels** labels every link (its own label, else its type such as *Trunk* or *Access*; dashed service links are explained by the key instead, so a device with a dozen services is not covered in tags), or turns all labels off, so no link looks undocumented.
- **Clear device roles.** Device types include Hypervisor node and Storage / NAS as well as Router, Firewall, Switch, and so on, and each box shows a **Model / role** line (filled from the device's Role when imported). Set the device type on the Devices page; devices added before types existed default to Server.
- **Auto layout** arranges the diagram top to bottom (Internet, then firewall/router, switches, hosts, services) and adds an Internet node above your edge router or firewall if there is none, so a reader knows where to start.
- **Add from inventory** and **Import all** create nodes from your devices (VLANs become tags; services become dashed boxes linked to their host device) and place them in those tiers. Only names and roles are used, never IP addresses, unless you type them into a label yourself.
- **Recording links as Connections:** a line in the diagram is only a picture. To keep it in the Connections records, click the link and choose **Record as connection**. It opens the Connections form with the device, switch and mode already filled in, and you add the ports and VLANs. Both ends must be inventory devices: pick a node and set its **Inventory device**, or add it with Add from inventory. A note above the canvas counts links that are not recorded yet, and the panel shows "Recorded in Connections" once they are.
- **Save** stores the diagram. Each changed save is recorded in the revision log (there is no History page for diagrams yet). **Download SVG/PNG** save first, then export the saved diagram. PNGs are rendered in your browser.

### Sharing a diagram

**Share…** creates a link like `/share/<random token>`. Anyone with the link can view that one diagram, read-only, without signing in. The link exposes only the diagram image, not your inventory or the rest of the app. **Stop sharing** revokes it immediately, and deleting the diagram does too. A new share gets a new token.

Share links (`/share/*`) are the one deliberately public, read-only surface; everything else needs a LabDoc login (see below). If you also put LabDoc behind Cloudflare Access or another reverse-proxy auth layer, make sure that layer lets `/share/*` through unauthenticated too, or outside viewers won't be able to open the link. If you don't want any public link, don't use Share.

## Authentication

LabDoc has its own login: a session cookie, set after signing in, gates every route — including static assets — except `/login`, `/setup`, `/healthz`, and `/share/*`. On first run, any request redirects to **/setup**, a one-time page that creates the single operator account (bcrypt-hashed password, stored in `homelab.db`); once that account exists, `/setup` stops working and everything redirects to **/login** instead. From **Account** (top nav, once signed in) you can change your password or add further accounts — every account has identical access, there are no roles — and remove any account but the one you're currently using. Repeated failed logins from the same IP are locked out for 15 minutes after 5 attempts.

Every state-changing request (every form, plus the topology editor's save/share actions) also carries a CSRF token, checked against a dedicated cookie before anything is written — a second, independent layer on top of the session cookie's own `SameSite=Lax` protection.

This replaces needing Cloudflare Access or a proxy-level auth middleware just to keep LabDoc private; you can still add one in front for defense in depth (e.g. restricting the LXC to a trusted network), but it's no longer required.

### Password reset by email

An account's email is optional and separate from login — a username/password still works with no email on file. Add one under **Account**; LabDoc emails a confirmation link, and only a *verified* email is ever used to send a reset link (an unconfirmed one just sits there unused). "Forgot your password?" on the login page then emails a single-use link that expires in an hour and, once used, signs that account out everywhere.

This needs outgoing SMTP configured (`-smtp-host` etc., below); it talks SMTP directly (Go's standard library, no external mail dependency), with STARTTLS on the usual submission port 587. Implicit-TLS port 465 isn't supported. Leaving `-smtp-host` blank (the default) disables email entirely — the setup/login/account flows all work exactly as before, they just skip the email section and "Forgot your password?" reports that email isn't configured.

## Build and run

Requires Go 1.25+.

```bash
CGO_ENABLED=0 go build -o labdoc ./cmd/labdoc
./labdoc                      # http://localhost:5380, creates ./homelab.db
```

| Flag | Env | Default | |
|---|---|---|---|
| `-addr` | `LABDOC_ADDR` | `:5380` | listen address |
| `-db` | `LABDOC_DB` | `homelab.db` | SQLite file |
| `-backup PATH` | | | write a consistent copy and exit |
| `-version` | | | print version and exit |
| `-smtp-host` | `LABDOC_SMTP_HOST` | *(blank)* | SMTP server for outgoing email; blank disables email verification / password reset |
| `-smtp-port` | `LABDOC_SMTP_PORT` | `587` | SMTP port (587/STARTTLS; 465 implicit TLS not supported) |
| `-smtp-username` | `LABDOC_SMTP_USERNAME` | *(blank)* | SMTP username |
| `-smtp-password` | `LABDOC_SMTP_PASSWORD` | *(blank)* | SMTP password |
| `-smtp-from` | `LABDOC_SMTP_FROM` | *(blank)* | From address for outgoing email |

Stamp a release version and cross-compile for a Linux LXC:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X labdoc/internal/version.Version=1.0.0" -o labdoc ./cmd/labdoc
```

Test and vet: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`. CI runs the same on every push and pull request.

Pull requests to `main` are also reviewed and merged automatically by `.github/workflows/pr-review-merge.yml`: gofmt, vet, tests, build with a 20 MB binary budget, a check that `internal/version/version.go` was bumped, and golangci-lint. When all pass, the PR is squash-merged and its branch deleted. Draft PRs, fork PRs, and PRs labeled `hold` are never merged. In repository settings, enable "Allow GitHub Actions to create and approve pull requests" and allow squash merging.

## Deploy

1. Copy the binary to `/usr/local/bin/labdoc` and create a `labdoc` user.
2. Install `deploy/labdoc.service` to `/etc/systemd/system/`, then `systemctl enable --now labdoc`.
3. Put it behind a reverse proxy (e.g. Nginx Proxy Manager). LabDoc has its own login (see **Authentication** above); binding it to `127.0.0.1` or a trusted network, or layering Cloudflare Access in front, is optional defense in depth.

## Backup and restore

The database runs in WAL mode, so copying `homelab.db` alone while the app is running can miss recent writes. Use:

```bash
labdoc -db /var/lib/labdoc/homelab.db -backup /backups/labdoc-$(date +%F).db
```

This is safe while the app is serving and refuses to overwrite an existing file. Point your nightly backup job at the result (or snapshot the whole LXC).

To restore, stop the service and put the file back at the `-db` path. Schema migrations run on startup, so an older backup upgrades itself.

## Your data stays out of git

`*.db*` is gitignored. Clones start with an empty database, and your real IPs and topology are never committed.

## License

GPL-3.0, see `LICENSE`.
