# Changelog

## [0.3.6] - 2026-08-01

### Added — 开户可选手动公网入口
- 开户表单可填 **公网入口 IP/域名** 与端口；写入用户 `entry_host` / `entry_port`。
- 未填写时：扫码链接默认用线路**第一跳**的公网地址与端口（含 relay 角色，如 cm7）。
- 二维码仍为 `socks5://` 节点链接（客户端入口协议；骨干仍是 mieru，不影响隧道隐蔽性）。

### Ops
```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
```

## [0.3.5] - 2026-08-01

### Changed — 用户二维码改为可扫码节点链接
- 管理端「扫码使用」二维码内容改为 `socks5://user:pass@host:port#name`，手机客户端扫码即可导入。
- 不再把 Clash 订阅 URL 编进二维码（订阅链接仍可复制，仅作次要用途）。
- 新增 `GET /api/admin/users/:id/share` 返回节点链接与入口列表。

### Ops
```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
```
面板升级即可（节点 agent 可不升）。

## [0.3.4] - 2026-08-01

### Fixed — hop probe false timeout + apply blocking heartbeat
- Screenshot error `等待 Agent 拨测超时` often meant agent was stuck in `pullAndApply` (mieru apply/start) and could not pick dial jobs on heartbeat.
- Agent now runs apply **in background**; heartbeat keeps running every **5s** and reports dial results immediately.
- Panel waits **30s** for agent dial jobs (was 5s/22s).
- Concurrent version/apply_error state is mutex-protected.

### Ops
```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash
```

## [0.3.3] - 2026-08-01

### Fixed — restore working multi-hop after v0.3.2 regression
- v0.3.2 skipped `mieru apply` and started directly from a hand-written JSON store.
  That drops the official password hashing / store merge path; user reported line broken again.
- Restore **official flow**: write patch (no `httpProxyPort`) → wipe poisoned store →
  `mieru apply config <patch>` into a **separate** store file → stop → start.
- Still omit `httpProxyPort` (value 0 is invalid on mieru 3.x).
- Scrub any leftover JSON under agent data dir that still has `httpProxyPort: 0`.

### Ops
```bash
# 面板 + 节点都升，然后面板点「重建配置」
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash
```

## [0.3.2] - 2026-08-01

### Fixed — cm7 `mieru apply: exit status 1`
- Root cause: client JSON had `"httpProxyPort": 0`, which **mieru 3.x rejects** (`HTTP proxy port number 0 is invalid`).
- Agent now **omits** unused `httpProxyPort` and starts with a full config file via `MIERU_CONFIG_JSON_FILE` (no fragile apply-merge).

### Ops
```bash
# 只需升级 cm7（relay）agent；面板可一并升
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
  MIERU_VERSION=v0.3.2 bash -s -- \
  --panel-url http://面板:8080 --node-id n_5cd543dd56fd --token ... --role relay
# 面板点「重建配置」，等 cm7 变在线
```

## [0.3.1] - 2026-08-01

### Fixed — degraded 可见 + 国内下载
- Agent 心跳上报 **真实 apply 错误**；节点列表直接显示（无需 SSH journalctl）。
- 下载 mieru/mita / agent / panel 包时自动尝试 **GitHub 镜像**（ghfast / ghproxy / gitdl）。
- 安装脚本多镜像回退，国内 VPS 拉 GitHub 更稳。

### Ops
```bash
# 面板 + cm7 agent 都升到 v0.3.1 后看节点行下的红色错误文案
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.3.1 bash
# cm7 上（复制面板「安装命令」或）:
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
  MIERU_VERSION=v0.3.1 bash -s -- --panel-url ... --node-id n_5cd543dd56fd --token ... --role relay
```
若仍 degraded：节点名下方会显示具体错误（例如 `download mieru: all mirrors failed`）。可在 cm7 上手动：
```bash
# 任选能下到的方式把 mieru 放到 PATH
curl -fL -o /tmp/mieru.tgz 'https://ghfast.top/https://github.com/enfein/mieru/releases/download/v3.35.0/mieru_3.35.0_linux_amd64.tar.gz'
tar -xzf /tmp/mieru.tgz -C /usr/local/bin
chmod +x /usr/local/bin/mieru
systemctl restart mieru-agent
```

## [0.3.0] - 2026-08-01

### Data plane refactor (OneClick-aligned)

Based on **ike-sh/mieru-OneClick 2.1.1** + official mita lifecycle.

- **Stable backbone credentials** (`backbone_user` / `backbone_pass` in settings): relay↔exit tunnel no longer depends on whichever end-user was first in the list.
- **Hybrid rewrite**: `mita` + local `mieru_client(127.0.0.1)` + public `socks_in` — same shape as OneClick single-node (client → local socks → mita on same host).
- **Entry → Exit**: uses local mieru to mita (not bare SOCKS to mita port).
- **Entry → Relay**: still SOCKS chain with end-user auth.
- **mieru client**: `MULTIPLEXING_OFF`, `HANDSHAKE_NO_WAIT`, `mtu 1400`, profile `default` (OneClick defaults).
- **mita users**: always `allowPrivateIP` + `allowLoopbackIP` (IX / hybrid loopback).
- **socks_in**: wait for local mieru upstream before listen.
- **Admin**: `GET /api/admin/diagnose`, `GET /api/admin/nodes/:id/desired` (passwords redacted).

### Ops
```bash
# Panel
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | \
  MIERU_VERSION=v0.3.0 bash

# Every node agent (entry / relay / exit / hybrid)
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
  MIERU_VERSION=v0.3.0 bash -s -- \
  --panel-url http://面板:8080 --node-id ... --token ... --role exit|relay|entry|hybrid
```
Then in panel: **重建配置** → wait agents pull → 测通断. Fill **内网 IP** on IX hops.

## [0.2.5] - 2026-07-31

### Added
- **落地** 侧栏菜单：专门管理家宽 / 住宅 exit（及 hybrid），默认标签 `residential`、端口 8964，一键复制 Agent 安装命令。

### Fixed
- install-panel：新系统无监听时 `ss|grep` 在 pipefail 下不再静默退出。

## [0.2.4] - 2026-07-31

### Fixed — global scan
- **Port model**: default port range is a **single** `EffectiveListenPort` (no more exit 8964 vs mita 30000–40000 mismatch). Hybrid public SOCKS = listen; mita = listen+1.
- **Relay**: mieru `rpc_port` defaults to **18964** (not 8964) so it never collides with public `socks_in`.
- **Relay→Exit**: client uses exit `MitaPrimaryPort()`; backbone `link_user` always injected onto every exit (sticky-safe).
- **Partial apply**: required plugins must succeed before config version advances (failed mita/mieru retries next pull).
- **socks_in**: refuse empty users; negotiate USER/PASS method; retry listen on EADDRINUSE.
- **mita**: refuse zero users; bind primary port consistently.
- **install-panel**: always probe `http://127.0.0.1:$PORT`; kill bare `panel` + verify listener `/proc/pid/exe`.
- **install-agent**: separate `/opt/mieru-agent` (no longer overwrites panel install dir).
- **JWT**: remove duplicate `sub`; TTL 7d; login respects `?next=`.
- **CORS**: `*` no longer pairs with credentials.
- **Docker**: inject `-X main.Version`.

### Ops
```bash
# Panel (must show v0.2.4)
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash

# Every node agent
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
  bash -s -- --panel-url http://面板:8080 --node-id ... --token ... --role exit|relay|entry
```
Then: 无痕登录 → 节点页「重建配置」或改任意节点触发 rebuild → 测通断.

## [0.2.3] - 2026-07-31

### Fixed
- **Upgrade actually kills old panel**: install script force-kills by name + port (fuser/ss/lsof) before replace; verifies running `/api/version`.
- **401 unauthorized** on admin APIs redirects to login (expired JWT no longer shows empty node list).


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
