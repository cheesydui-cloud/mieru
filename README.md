# Mieru Panel

Linux 多跳代理编排面板（Go Panel + Agent + 内嵌 Web UI）。

```text
用户 → Entry → Relay(mieru) → Exit(mita, 计量)
              ↑
           Panel
```

## 一键安装（Linux）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
```

装完终端会打印 **管理员账号和密码**，请立刻保存。也可随时查看：

```bash
sudo cat /etc/mieru-panel.env
# PANEL_ADMIN_USER=...
# PANEL_ADMIN_PASS=...
```

浏览器打开：`http://服务器IP:8080`

指定密码安装：

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | \
  PANEL_ADMIN_PASS='你的密码' bash
```

## 一键升级（Linux）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
```

升级会：

- 下载最新 Linux 二进制并覆盖
- **自动 `systemctl restart mieru-panel`**
- **保留** `/etc/mieru-panel.env`（账号密码、JWT 不变）

验证：

```bash
curl -s http://127.0.0.1:8080/api/version
# 期望含 "version":"v0.1.2" 和 "ui":"embedded"

curl -sI http://127.0.0.1:8080/ | head -3
# 期望 HTTP/1.1 200
```

指定版本升级：

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | \
  MIERU_VERSION=v0.1.2 bash
```

## 账号密码

| 项 | 位置 |
|----|------|
| 用户名 | `/etc/mieru-panel.env` → `PANEL_ADMIN_USER`（默认 `admin`） |
| 密码 | `/etc/mieru-panel.env` → `PANEL_ADMIN_PASS`（安装时随机生成，除非你指定了） |
| 查看命令 | `sudo cat /etc/mieru-panel.env` |
| 改密码 | 编辑 env 后 `sudo systemctl restart mieru-panel`（仅影响**新库首次创建**的管理员；已有库改 env 不会自动改库内密码，见下） |

**已安装过、要重置管理员密码：**

```bash
# 停服务、删库后用新密码重装（会清空面板数据，慎用）
sudo systemctl stop mieru-panel
sudo rm -f /var/lib/mieru-panel/panel.db
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | \
  PANEL_ADMIN_PASS='新密码' bash
```

## 节点 Agent（Linux）

面板里创建节点，拿到 `node_id` / `token` 后：

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
  bash -s -- --panel-url http://面板IP:8080 --node-id n_xxx --token tok_xxx --role exit
```

`role`：`entry` | `relay` | `exit`

## 常用命令

```bash
sudo systemctl status mieru-panel
sudo systemctl restart mieru-panel
sudo journalctl -u mieru-panel -f

sudo cat /etc/mieru-panel.env
curl -s http://127.0.0.1:8080/api/health
curl -s http://127.0.0.1:8080/api/version
```

## 目录

| 路径 | 说明 |
|------|------|
| `/usr/local/bin/mieru-panel` | 面板二进制（UI 已内嵌） |
| `/usr/local/bin/mieru-agent` | 节点 Agent |
| `/opt/mieru-panel/` | 安装目录 |
| `/var/lib/mieru-panel/` | 数据（SQLite） |
| `/etc/mieru-panel.env` | 环境变量 / 管理员密码 |
| `/etc/systemd/system/mieru-panel.service` | 服务 |

## 发布包（仅 Linux）

- [linux-amd64](https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.2/mieru-panel-v0.1.2-linux-amd64.tar.gz)
- [linux-arm64](https://github.com/cheesydui-cloud/mieru/releases/download/v0.1.2/mieru-panel-v0.1.2-linux-arm64.tar.gz)

## License

MIT
