# LabDoc

A single-operator homelab documentation tool: one system of record for devices, VLANs, IP assignments, services, and firewall rules, with automatic revision history instead of hand-kept changelogs.

It is a web app: one static Go binary plus one SQLite file. No runtime dependencies, no Node, no external database.

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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X labdoc/internal/version.Version=0.2.0" -o labdoc ./cmd/labdoc
```

Test and vet: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`. CI runs the same on every push and pull request.

Pull requests to `main` are also reviewed and merged automatically by `.github/workflows/pr-review-merge.yml`: gofmt, vet, tests, build with a 20 MB binary budget, a check that `internal/version/version.go` was bumped, and golangci-lint. When all pass, the PR is squash-merged and its branch deleted. Draft PRs, fork PRs, and PRs labeled `hold` are never merged. In repository settings, allow squash merging.

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
