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

## 快速开始

### 要求

- Go 1.22+（开发构建）
- Node.js 20+（构建前端）
- Linux 节点若要真正 `nft apply` 需要 root + nftables（默认 dry-run 只写规则文件）

### 从源码运行

```bash
git clone https://github.com/cheesydui-cloud/mieru.git
cd mieru

# 后端
export PANEL_JWT_SECRET='replace-me'
export PANEL_ADMIN_USER=admin
export PANEL_ADMIN_PASS='strong-password'
go run ./cmd/panel

# 前端（另开终端）
cd web && npm install && npm run dev
# UI: http://127.0.0.1:5173  API 代理到 :8080
```

生产可先构建静态资源再由 panel 托管：

```bash
cd web && npm ci && npm run build
# 然后在仓库根目录启动 panel，访问 http://127.0.0.1:8080
go build -o bin/panel ./cmd/panel
./bin/panel
```

### 节点 Agent

在面板 **节点 → 新建** 后保存 `node_id` 与 `agent_token`：

```bash
go build -o bin/agent ./cmd/agent

export AGENT_PANEL_URL='https://panel.example.com'
export AGENT_NODE_ID='n_xxx'
export AGENT_TOKEN='tok_xxx'
export AGENT_ROLE='exit'          # entry | relay | exit
export AGENT_NFT_DRYRUN=1         # 0 = 尝试真正执行 nft
./bin/agent
```

### Docker（面板）

```bash
cd deploy
export PANEL_JWT_SECRET=replace-me
export PANEL_ADMIN_PASS=strong-password
docker compose up -d --build
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
