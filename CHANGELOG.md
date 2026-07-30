# Changelog

All notable changes to this project will be documented in this file.

## [0.1.5] - 2026-07-30

### Fixed
- Routes page blank: empty `ListRoutes`/`ListNodes` returned JSON `null`, Vue crashed on `.length`. Now always `[]`.
- Routes empty state + create modal clearer; frontend guards `Array.isArray`.

### Changed
- Install scripts default `v0.1.5`.

## [0.1.4] - 2026-07-30

### Fixed
- **Root cause of `invalid credentials`**: `EnsureAdmin` only inserted when `admins` table was empty. After first install, `/etc/mieru-panel.env` could drift from SQLite password hash; pasting env password always failed.
- **Fix**: every panel start now syncs `PANEL_ADMIN_USER` / `PANEL_ADMIN_PASS` into SQLite via `SetAdminPassword`. Env is source of truth.
- Installer prints login probe result after install/upgrade; supports `PANEL_ADMIN_PASS=...` on upgrade to force new password.


## [0.1.3] - 2026-07-30

### Fixed
- Admin login `invalid credentials` after upgrade: password in `/etc/mieru-panel.env` was only applied on **first** DB create; later env changes did not update SQLite. Added `mieru-panel --reset-admin` and `scripts/reset-admin.sh`.

### Added
- One-line admin password reset for Linux servers.
- Install/upgrade scripts default to `v0.1.3` (Linux only).

## [0.1.2] - 2026-07-30

### Fixed
- Remote installs still returning Gin default `404 page not found` on `/` because upgrade replaced the binary **without restarting** the running process. Installer now always `systemctl restart mieru-panel`.
- Added explicit `GET /` and `GET /index.html` routes (not only `NoRoute`) so the SPA never hits Gin’s default 404 handler.
- Added `/api/version` and version field on `/api/health` to verify the running binary after upgrade.

### Changed
- Install scripts default to `v0.1.2`; upgrade keeps existing `/etc/mieru-panel.env`.

## [0.1.1] - 2026-07-30

### Fixed
- **404 page not found** after one-line install: Vue UI is now **embedded** into the `panel` binary via `go:embed`. Opening `http://IP:8080/` serves the login page without a separate `web/dist` directory.
- Install scripts default to `v0.1.1`.

### Changed
- Static assets served from embed FS first, with on-disk `./web/dist` as dev fallback.

## [0.1.0] - 2026-07-30

### Added

#### Control plane (Panel)
- Go HTTP API with admin JWT auth and user portal login
- SQLite persistence for admins, nodes, routes, users, traffic, audit logs
- Generic multi-hop model: `entry` / `relay` / `exit` / `hybrid` (not vendor-locked)
- Route hops JSON orchestration (`sticky` / `wrr` / `failover` fields)
- User lifecycle: create, expire_at, traffic quota, status machine (`active` / `expired` / `over_quota` / `disabled`)
- Domain-first node fields (`hostname`, `alt_hostnames`) for client delivery
- Clash Meta style subscription endpoint `/sub/:token` (Entry hosts only)
- Exit-authoritative traffic ingest `/api/agent/traffic` + in-memory realtime rates
- Agent config versioning and desired-config pull `/api/agent/config`
- Agent heartbeat `/api/agent/heartbeat`
- Config generator rebuilding per-node plugin payloads after node/user/route changes
- Dark SaaS Vue 3 console: Dashboard, Nodes, Routes, Users, User Portal
- Docker Compose / Dockerfile skeleton under `deploy/`

#### Data plane (Agent)
- Node agent with heartbeat, config pull, last-good version file
- Plugin registry:
  - `nft_forward` — render/apply nftables DNAT rules (dry-run by default)
  - `mieru_client` — write mieru client config fragment
  - `mita_server` — write mita user list for exit nodes
  - `socks_in` — write entry socks allowlist config
- Exit traffic reporter loop (hook-ready; real byte counters to be integrated)

### Security notes for operators
- Change `PANEL_JWT_SECRET` and admin password before production
- Keep panel off public internet when possible; agents pull outbound
- Firewall exit mita to relay IPs only; users should only see entry domains
- Use only for lawful, authorized networking

### Known limitations (MVP)
- Does not yet hot-reload official `mita` / `mieru` binaries
- Exit traffic deltas are hook-ready; production needs counters on the real data path
- Entry socks server is config export first; in-process socks auth is planned
- SQLite single-node control plane (PostgreSQL planned)

### Upgrade
Initial public release — no migration path.
