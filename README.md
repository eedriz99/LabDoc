# LabDoc

A single-operator homelab documentation tool: one system of record for devices, VLANs, IP assignments, services, and firewall rules, with automatic revision history instead of hand-kept changelogs.

It is a web app: one static Go binary plus one SQLite file. No runtime dependencies, no Node, no external database.

## Features

- Create, edit, and delete devices, VLANs, services, IP assignments, and firewall rules from the browser.
- Devices have a **type** (server, PC / workstation, laptop, NAS, switch, router, firewall, access point, other) and optional **drive types**: tick any of SSD, HDD, and NVMe (combine them freely). Drive types show as colored badges in the device list. Devices that existed before v1.1.0 were set to "Server", so change your switches, routers, and firewalls to their real type.
- **Connections:** record how each device plugs into a switch or router: device NIC and switch port, and whether the link is an **Access** port (one untagged VLAN) or a **Trunk** (one or more tagged VLANs, plus an optional native VLAN). One NIC carrying, say, VLAN 10 for hypervisor management and VLAN 30 for services is a single trunk connection. A switch or device port can only be used once.
- Automatic revision history: every change is versioned, and each record has a History page showing what changed and when.
- Light and dark themes: follows your OS setting by default, with a toggle in the nav bar (choice is remembered per browser).
- Consistent online backup via `-backup`, and a version shown in the page footer.
- **Network topology designer:** drag-and-drop diagrams (routers, firewalls, switches, servers, VLANs, and more) with labeled links. Add nodes from your inventory (or import it all: a device becomes a switch, router, firewall, etc. node according to its type, network gear goes in its own row, devices are linked to their VLANs and services to their hosts, and each Connection becomes a link labeled like `Trunk: 10,30 (native 1)` or `Access: 20`), then download as SVG or PNG or share a read-only link.

Not built yet: full-text search, and Markdown pages and export.

## Topology designer

Open **Topology** in the nav, create a diagram, and:

- **Add node** places a node of the chosen type. Click it to rename it or change its type, and drag it to move it (positions snap to a grid).
- **Link mode** connects two nodes: click one, then the other. Click a link to label it, set its **line type** (plain, access = thin green, trunk = thick purple), or delete it. **Delete** also works with the Delete key.
- **Add from inventory** and **Import all inventory** create nodes from your devices, VLANs, and services. Only names are used, never IP addresses, unless you type them into a label yourself.
- **Recording links as Connections:** a line in the diagram is only a picture. To keep it in the Connections records, click the link and choose **Record as connection**. It opens the Connections form with the device, switch and mode already filled in, and you add the ports and VLANs. Both ends must be inventory devices: pick a node and set its **Inventory device**, or add it with Add from inventory. A note above the canvas counts links that are not recorded yet, and the panel shows "Recorded in Connections" once they are.
- **Save** stores the diagram. Each changed save is recorded in the revision log (there is no History page for diagrams yet). **Download SVG/PNG** save first, then export the saved diagram. PNGs are rendered in your browser.

### Sharing a diagram

**Share…** creates a link like `/share/<random token>`. Anyone with the link can view that one diagram, read-only, without signing in. The link exposes only the diagram image, not your inventory or the rest of the app. **Stop sharing** revokes it immediately, and deleting the diagram does too. A new share gets a new token.

Because LabDoc has no login of its own, your reverse proxy or Cloudflare Access is what protects everything else. To make share links work for people outside your network, that layer must let `/share/*` and `/static/*` through without authentication while still protecting all other paths. If you don't want any public link, don't use Share.

## Build and run

Requires Go 1.25+.

```bash
CGO_ENABLED=0 go build -o labdoc ./cmd/labdoc
./labdoc                      # http://localhost:8080, creates ./homelab.db
```

| Flag | Env | Default | |
|---|---|---|---|
| `-addr` | `LABDOC_ADDR` | `:8080` | listen address |
| `-db` | `LABDOC_DB` | `homelab.db` | SQLite file |
| `-backup PATH` | | | write a consistent copy and exit |
| `-version` | | | print version and exit |

Stamp a release version and cross-compile for a Linux LXC:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X labdoc/internal/version.Version=1.0.0" -o labdoc ./cmd/labdoc
```

Test and vet: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`. CI runs the same on every push and pull request.

## Deploy

1. Copy the binary to `/usr/local/bin/labdoc` and create a `labdoc` user.
2. Install `deploy/labdoc.service` to `/etc/systemd/system/`, then `systemctl enable --now labdoc`.
3. Put it behind a reverse proxy (e.g. Nginx Proxy Manager). **LabDoc has no authentication of its own**; use Cloudflare Access or proxy-level auth, and keep it bound to `127.0.0.1` or a trusted network.

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
