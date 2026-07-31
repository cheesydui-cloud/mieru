# Changelog

## [0.2.2] - 2026-07-31

### UI — light admin console (weir-style)
- Light canvas, white cards with thin borders, compact left sidebar + top bar
- Dashboard metric cards: `标签` + large `n / total` numbers
- Nodes / Users / Routes: filter toolbar, bordered table shell, green online status
- Login and shell rebranded as **Mieru 控制台**

## [0.2.1] - 2026-07-30

### Fixed — data plane actually listens (OneClick-aligned)
- **mita daemon**: before `apply/start`, ensure management process is up — prefer official **deb/rpm + systemd `mita run`**, else managed `mita run` with private `MITA_UDS_PATH` (root cause of Exit connection refused).
- **User injection**: convert `[]AgentUser` → `[]map` before plugins; plugins no longer miss credentials via failed type assert.
- **Partial apply**: if mieru fails, still start public `socks_in` so Relay port is not dead.
- **Agent upgrade**: install-agent stops old process before replacing binary.
- **Robust user parse** in mita/mieru/socks_in (JSON round-trip safe).

### Topology (unchanged)
```
Client → (external entry DNAT) → Relay:socks_in → 127.0.0.1:mieru → Exit:mita → Internet
```

### Ops
- Upgrade **panel + every node agent** to v0.2.1, then re-probe routes.
- Need ≥1 active panel user (mieru↔mita backbone credentials).
- Open firewall: relay listen port (e.g. 10401), exit mita port (e.g. 10001).
- Logs: `journalctl -u mieru-agent -f` — expect `mita ... RUNNING` / `socks_in listening`.

## [0.2.0] - 2026-07-30

### Breaking / Data plane
- **Real proxy path**: Agent no longer only writes JSON files.
- **Exit**: starts **mita** (`mita apply/start`) on node listen port; auto-downloads mita binary if missing.
- **Relay**: starts **mieru client** to Exit, exposes local SOCKS5; public `socks_in` upstreams via mieru.
- **Entry / external entry landing on Relay**: in-process **SOCKS5** (user/pass) listens on node port range start.

### Topology
```
Client → (external entry DNAT) → Relay:socks_in → 127.0.0.1:mieru → Exit:mita → Internet
```

### Notes
- Need at least one active panel user (credentials shared on mieru↔mita backbone).
- Open firewall for node listen ports (e.g. 10401 on relay, 10001 on exit).
- First apply may download mieru/mita from GitHub releases (~few MB).


All notable changes to this project will be documented in this file.

## [0.1.11] - 2026-07-30

### Fixed
- **Upgrade actually replaces running binary**: install script stops service/pkill before install, verifies `mieru-panel --version` and live `/api/version`.
- **index.html no-cache** so browsers pick new hashed assets after upgrade.
- Removed misleading auto `--reset-admin` on every upgrade (was stopping/restarting and confusing ops).

### Added
- `mieru-panel --version` for binary self-check.
- Sidebar + topbar always show panel version (from `/api/version`).
- Route list: edit + probe (from 0.1.10) with clearer subtitle.

## [0.1.10] - 2026-07-30

### Added
- **Route 测通断**: `POST /api/admin/routes/:id/probe` — TCP probe each hop from panel, update health (ok/degraded/down).
- Route list actions: **编辑** + **测通断** + 删除 (more visible action column).

### Note
- Live panels still on v0.1.7 need upgrade to see edit/probe UI.

## [0.1.9] - 2026-07-30

### Added
- **Route edit** in UI (name / strategy / hops).
- **External entry** on routes: choose entry from node list **or** hand-fill IP/domain + port (merchant IX without Agent).
- Subscription prefers the bound route's entry hop (including external entry).
- Relay with external-entry routes gets `socks_in` so DNAT landing works.

## [0.1.8] - 2026-07-30

### Fixed
- Agent stayed **offline** until first ticker: now sends heartbeat immediately on start.
- Install-agent prints heartbeat probe result (connect / 401 / OK).
- Clipboard copy works on plain HTTP panels (execCommand fallback).
- Nodes list auto-refreshes every 5s so online status appears without manual reload.

## [0.1.7] - 2026-07-30

### Changed
- Node ports UI simplified to **start port / end port** only (like common panels). Removed separate "主监听端口" field; start port is the primary client port.

## [0.1.6] - 2026-07-30

### Added
- **Settings page**: panel URL / panel name (used for Agent install commands & absolute subscription links).
- **Admin password change** in UI (`POST /api/admin/admin-password`).
- **Node edit** UI (name/role/hostname/IP/ports).
- **Custom listen port + port range** on nodes (`listen_port`, `port_min`, `port_max`); used by configgen/subscription.
- **Agent one-line install command** after create and via "安装命令" button (`GET /api/admin/nodes/:id/install`).

### Fixed
- Normal start no longer force-overwrites admin password from env (so UI password changes persist). Use `--reset-admin` or `PANEL_ADMIN_FORCE_SYNC=1` only when intentional.
- Empty list APIs still return `[]` (from 0.1.5).

## [0.1.5] - 2026-07-30
