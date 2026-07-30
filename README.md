# Mieru Panel

[![Release](https://img.shields.io/github/v/release/cheesydui-cloud/mieru)](https://github.com/cheesydui-cloud/mieru/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

通用 **多跳代理编排面板**：Go 控制面 + 节点 Agent + Vue3 深色控制台。

面向「入口可换、中继加密、落地主计量」的运维场景；节点角色抽象为 `entry` / `relay` / `exit`，不绑定特定云厂商。

```text
用户客户端
    │  只接触 Entry 域名 / 订阅
    ▼
┌──────────┐   nft / 转发    ┌──────────┐   mieru 隧道   ┌──────────┐
│  Entry   │ ──────────────► │  Relay   │ ─────────────► │  Exit    │
│  Agent   │                 │  Agent   │                │  Agent   │
└──────────┘                 └──────────┘                │ + mita   │
                                                         │ 主计量   │
                                                         └──────────┘
        ▲                         ▲                           ▲
        └─────────────────────────┴───────────────────────────┘
                         Panel（配置下发 / 用户 / 配额 / 到期）
```

## 特性

| 能力 | 说明 |
|------|------|
| **通用 hops** | 线路为有序节点链，可 1～N 跳 |
| **域名优先** | 节点 `hostname` 下发给客户端；换入口只改 DNS/订阅 |
| **落地主计量** | Exit Agent 上报流量与实时网速；换前置不影响账本 |
| **到期 / 配额** | `expire_at`、流量上限 → 自动 `expired` / `over_quota` |
| **插件化 Agent** | `nft_forward` · `mieru_client` · `mita_server` · `socks_in` |
| **订阅交付** | `/sub/:token` 导出 Clash Meta 风格配置（仅 Entry） |
| **深色控制台** | 管理端总览/节点/线路/用户 + 用户门户 |

> 骨干加密段推荐使用 [mieru/mita](https://github.com/enfein/mieru)；本仓库是 **编排与运维面板**，不重新实现协议。

## 安装

### 方式一：一键安装预编译二进制（推荐服务器）

> 需要 `curl` + `tar`。默认安装到 `/usr/local/bin`，前端静态资源放到 `/opt/mieru-panel/web/dist`（可改环境变量）。

**Linux amd64：**

```bash
export MIERU_VERSION=v0.1.0
export MIERU_ARCH=linux-amd64
curl -fsSL -o /tmp/mieru-panel.tgz \
  "https://github.com/cheesydui-cloud/mieru/releases/download/${MIERU_VERSION}/mieru-panel-${MIERU_VERSION}-${MIERU_ARCH}.tar.gz"
sudo mkdir -p /opt/mieru-panel
sudo tar -xzf /tmp/mieru-panel.tgz -C /opt/mieru-panel --strip-components=1
sudo install -m 755 /opt/mieru-panel/panel /usr/local/bin/mieru-panel
sudo install -m 755 /opt/mieru-panel/agent /usr/local/bin/mieru-agent
```

**Linux arm64：** 将上面的 `MIERU_ARCH=linux-arm64`。

**macOS Apple Silicon：**

```bash
export MIERU_VERSION=v0.1.0
export MIERU_ARCH=darwin-arm64
curl -fsSL -o /tmp/mieru-panel.tgz \
  "https://github.com/cheesydui-cloud/mieru/releases/download/${MIERU_VERSION}/mieru-panel-${MIERU_VERSION}-${MIERU_ARCH}.tar.gz"
mkdir -p "$HOME/.local/mieru-panel"
tar -xzf /tmp/mieru-panel.tgz -C "$HOME/.local/mieru-panel" --strip-components=1
install -m 755 "$HOME/.local/mieru-panel/panel" "$HOME/.local/bin/mieru-panel"
install -m 755 "$HOME/.local/mieru-panel/agent" "$HOME/.local/bin/mieru-agent"
# 确保 PATH 含 $HOME/.local/bin
```

**macOS Intel：** `MIERU_ARCH=darwin-amd64`。

**Windows x64（PowerShell）：**

```powershell
$ver = "v0.1.0"
$url = "https://github.com/cheesydui-cloud/mieru/releases/download/$ver/mieru-panel-$ver-windows-amd64.zip"
$out = "$env:USERPROFILE\mieru-panel"
New-Item -ItemType Directory -Force -Path $out | Out-Null
Invoke-WebRequest -Uri $url -OutFile "$out\mieru-panel.zip"
Expand-Archive -Path "$out\mieru-panel.zip" -DestinationPath $out -Force
# 运行: $out\mieru-panel-v0.1.0-windows-amd64\panel.exe
```

**校验下载：**

```bash
curl -fsSL -o SHA256SUMS.txt \
  "https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.0/SHA256SUMS.txt"
# 将对应包与 SUMS 放在同一目录后：
shasum -a 256 -c SHA256SUMS.txt --ignore-missing
```

预编译包文件一览：

| 平台 | 资源 |
|------|------|
| Linux x86_64 | [mieru-panel-v0.1.0-linux-amd64.tar.gz](https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.0/mieru-panel-v0.1.0-linux-amd64.tar.gz) |
| Linux arm64 | [mieru-panel-v0.1.0-linux-arm64.tar.gz](https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.0/mieru-panel-v0.1.0-linux-arm64.tar.gz) |
| macOS Intel | [mieru-panel-v0.1.0-darwin-amd64.tar.gz](https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.0/mieru-panel-v0.1.0-darwin-amd64.tar.gz) |
| macOS Apple Silicon | [mieru-panel-v0.1.0-darwin-arm64.tar.gz](https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.0/mieru-panel-v0.1.0-darwin-arm64.tar.gz) |
| Windows x64 | [mieru-panel-v0.1.0-windows-amd64.zip](https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.0/mieru-panel-v0.1.0-windows-amd64.zip) |
| 校验和 | [SHA256SUMS.txt](https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.0/SHA256SUMS.txt) |

### 启动 Panel（二进制）

```bash
# 数据与前端目录（按需修改）
export PANEL_LISTEN=':8080'
export PANEL_DB='/var/lib/mieru-panel/panel.db'
export PANEL_DATA='/var/lib/mieru-panel'
export PANEL_JWT_SECRET='replace-me-with-long-random'
export PANEL_ADMIN_USER='admin'
export PANEL_ADMIN_PASS='strong-password'

sudo mkdir -p /var/lib/mieru-panel
# 前端：从源码构建后拷贝，或暂时用开发模式（见下文）
# sudo mkdir -p /opt/mieru-panel/web && sudo cp -a web/dist /opt/mieru-panel/web/
cd /opt/mieru-panel   # 保证相对路径 ./web/dist 可访问，或自行改工作目录
mieru-panel
# 浏览器打开 http://服务器IP:8080
```

**systemd 示例（Linux）：**

```bash
sudo tee /etc/systemd/system/mieru-panel.service >/dev/null <<'EOF'
[Unit]
Description=Mieru Panel
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/mieru-panel
Environment=PANEL_LISTEN=:8080
Environment=PANEL_DB=/var/lib/mieru-panel/panel.db
Environment=PANEL_DATA=/var/lib/mieru-panel
Environment=PANEL_JWT_SECRET=replace-me-with-long-random
Environment=PANEL_ADMIN_USER=admin
Environment=PANEL_ADMIN_PASS=strong-password
ExecStart=/usr/local/bin/mieru-panel
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now mieru-panel
sudo systemctl status mieru-panel --no-pager
```

### 启动 Agent（二进制）

先在面板 **节点 → 新建**，复制 `node_id` 与 `agent_token`：

```bash
export AGENT_PANEL_URL='http://面板IP或域名:8080'
export AGENT_NODE_ID='n_xxx'
export AGENT_TOKEN='tok_xxx'
export AGENT_ROLE='exit'          # entry | relay | exit | hybrid
export AGENT_DATA='/var/lib/mieru-agent'
export AGENT_NFT_DRYRUN=1         # 生产 Entry 需要真实 nft 时设为 0，并确保有权限

sudo mkdir -p /var/lib/mieru-agent
mieru-agent
```

**systemd 示例（节点）：**

```bash
sudo tee /etc/systemd/system/mieru-agent.service >/dev/null <<'EOF'
[Unit]
Description=Mieru Node Agent
After=network.target

[Service]
Type=simple
User=root
Environment=AGENT_PANEL_URL=http://面板地址:8080
Environment=AGENT_NODE_ID=n_xxx
Environment=AGENT_TOKEN=tok_xxx
Environment=AGENT_ROLE=exit
Environment=AGENT_DATA=/var/lib/mieru-agent
Environment=AGENT_NFT_DRYRUN=1
ExecStart=/usr/local/bin/mieru-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now mieru-agent
```

### 方式二：从源码安装

```bash
git clone https://github.com/cheesydui-cloud/mieru.git
cd mieru

# 构建后端
go build -trimpath -ldflags='-s -w' -o bin/panel ./cmd/panel
go build -trimpath -ldflags='-s -w' -o bin/agent ./cmd/agent
sudo install -m 755 bin/panel /usr/local/bin/mieru-panel
sudo install -m 755 bin/agent /usr/local/bin/mieru-agent

# 构建前端（panel 静态托管 ./web/dist）
cd web && npm ci && npm run build && cd ..
```

开发热更新：

```bash
# 终端 1：API
export PANEL_JWT_SECRET='dev-secret'
export PANEL_ADMIN_PASS='admin123'
go run ./cmd/panel

# 终端 2：UI（代理 /api 到 :8080）
cd web && npm install && npm run dev
# 打开 http://127.0.0.1:5173
```

### 方式三：Docker（仅 Panel）

```bash
git clone https://github.com/cheesydui-cloud/mieru.git
cd mieru/deploy
export PANEL_JWT_SECRET=replace-me
export PANEL_ADMIN_USER=admin
export PANEL_ADMIN_PASS=strong-password
docker compose up -d --build
# http://127.0.0.1:8080
```

### 前端静态资源说明

预编译 `panel` 默认从**当前工作目录**加载 `./web/dist`。任选其一：

```bash
# A) 源码构建后拷贝
git clone https://github.com/cheesydui-cloud/mieru.git /tmp/mieru-src
cd /tmp/mieru-src/web && npm ci && npm run build
sudo mkdir -p /opt/mieru-panel/web
sudo cp -a dist /opt/mieru-panel/web/
cd /opt/mieru-panel && mieru-panel

# B) 开发期只用 Vite
cd web && npm run dev   # :5173 → 代理 API :8080
```

## 推荐上线流程

1. 创建 **Exit**（落地）→ **Relay**（中继）→ **Entry**（入口，填写接入域名）
2. **线路** 编排：Entry → Relay → Exit
3. **开户**：到期日、流量上限、绑定线路；保存代理密码与订阅链接
4. 各机器运行对应角色的 Agent
5. 用户只分发 **订阅 URL** 或 Entry 域名，不暴露 Relay/Exit

## 环境变量

### Panel

| 变量 | 默认 | 说明 |
|------|------|------|
| `PANEL_LISTEN` | `:8080` | 监听地址 |
| `PANEL_DB` | `data/panel.db` | SQLite 路径 |
| `PANEL_JWT_SECRET` | 内置弱密钥 | **生产必改** |
| `PANEL_ADMIN_USER` | `admin` | 初始管理员 |
| `PANEL_ADMIN_PASS` | `admin123` | 初始密码 |
| `PANEL_DATA` | `data` | 数据目录 |
| `PANEL_CORS` | `*` | CORS |

### Agent

| 变量 | 默认 | 说明 |
|------|------|------|
| `AGENT_PANEL_URL` | `http://127.0.0.1:8080` | 面板地址 |
| `AGENT_NODE_ID` | | 节点 ID |
| `AGENT_TOKEN` | | 节点 Token |
| `AGENT_ROLE` | `entry` | 角色 |
| `AGENT_DATA` | `data/agent` | 本地数据目录 |
| `AGENT_HEARTBEAT_SEC` | `15` | 心跳间隔 |
| `AGENT_PULL_SEC` | `10` | 拉配置间隔 |
| `AGENT_NFT_DRYRUN` | 非 `0` 为 dry-run | nft 是否真实 apply |
| `AGENT_PUBLIC_IP` / `AGENT_HOSTNAME` | | 可选上报 |

## HTTP API 摘要

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/auth/login` | 管理员或用户登录 |
| `GET` | `/api/admin/dashboard` | 总览统计 |
| `CRUD` | `/api/admin/nodes` | 节点（含域名） |
| `CRUD` | `/api/admin/routes` | 线路 hops |
| `CRUD` | `/api/admin/users` | 用户 / 到期 / 配额 |
| `GET` | `/sub/:token` | 订阅（仅 Entry） |
| `GET` | `/api/me/profile` | 用户门户资料 |
| `POST` | `/api/agent/heartbeat` | Agent 心跳 |
| `GET` | `/api/agent/config` | Agent 拉配置 |
| `POST` | `/api/agent/traffic` | Exit 流量上报 |

完整变更见 [CHANGELOG.md](CHANGELOG.md)。

## 项目结构

```text
cmd/panel              面板进程
cmd/agent              节点 Agent
internal/api           REST API
internal/store         SQLite 存储与状态机
internal/configgen     按角色生成 desired config
internal/agentcore     Agent 主循环
internal/plugins/      nft / mieru / mita / socks_in
web/                   Vue3 + Vite 深色 UI
deploy/                Docker 部署
```

## 安全建议

1. 修改默认管理员密码与 `PANEL_JWT_SECRET`
2. 管理端限制访问（VPN / IP 白名单 / 独立管理域名）
3. Exit 上 mita **仅允许 Relay IP**；用户永不直连落地
4. Agent 使用一机一 token，可在面板轮换（重建节点）
5. 仅用于合法、授权的网络接入与运维

## 路线图

- [ ] 嵌入/热重载官方 mita、mieru 进程
- [ ] Entry 进程内 socks5 鉴权与转发
- [ ] Exit 真实字节计数接入（替换占位上报）
- [ ] 配置 atomic 应用 + 失败回滚
- [ ] 主备线路健康检查与自动切换
- [ ] PostgreSQL / Redis 生产化
- [ ] 泛域名证书状态页

## 许可证

[MIT](LICENSE)

## 致谢

- [enfein/mieru](https://github.com/enfein/mieru) — mieru / mita 代理协议实现
