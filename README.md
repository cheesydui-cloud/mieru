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
- **保留** `/etc/mieru-panel.env`（env 内容不变）

> 升级**不会**自动改数据库里的管理员密码。若登录失败，见下方「重置密码」。

验证：

```bash
curl -s http://127.0.0.1:8080/api/version
# 期望含 "version":"v0.2.4" 和 "ui":"embedded"

curl -sI http://127.0.0.1:8080/ | head -3
# 期望 HTTP/1.1 200
```

## 账号密码

| 项 | 位置 |
|----|------|
| 用户名 | `/etc/mieru-panel.env` → `PANEL_ADMIN_USER`（默认 `admin`） |
| 密码 | `/etc/mieru-panel.env` → `PANEL_ADMIN_PASS` |
| 查看 | `sudo cat /etc/mieru-panel.env` |

密码真正生效在 **SQLite**（`/var/lib/mieru-panel/panel.db`）。  
env 只在「首次建库」或「执行重置」时写入库；只改 env 再重启**不会**改登录密码。

### 重置管理员密码（登录 invalid credentials 时用）

```bash
# 把 env 里的密码写回数据库（不删节点/用户）
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/reset-admin.sh | bash

# 或指定新密码
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/reset-admin.sh | \
  PANEL_ADMIN_PASS='新密码' bash
```

手动方式（当前 v0.1.2 也可）：

```bash
NEWPASS='你的新密码'
sudo sed -i "s|^PANEL_ADMIN_PASS=.*|PANEL_ADMIN_PASS=${NEWPASS}|" /etc/mieru-panel.env
sudo systemctl stop mieru-panel
sudo sqlite3 /var/lib/mieru-panel/panel.db "DELETE FROM admins;"
sudo systemctl start mieru-panel
# 登录: admin / 你的新密码
```

## 节点 Agent（Linux）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | \
  bash -s -- --panel-url http://面板IP:8080 --node-id n_xxx --token tok_xxx --role exit
```

`role`：`entry` | `relay` | `exit`

## 面板内设置

侧边栏 **设置**：
- 填写 **面板地址**（如 `http://IP:8080`），节点安装命令会用这个地址
- 修改 **管理员用户名/密码**

节点页：
- **编辑** 可改域名、IP、主端口、端口范围
- **安装命令** 复制一键 Agent 安装脚本

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

- [linux-amd64](https://github.com/cheesydui-cloud/mieru/releases/download/v0.2.3/mieru-panel-v0.2.3-linux-amd64.tar.gz)
- [linux-arm64](https://github.com/cheesydui-cloud/mieru/releases/download/v0.2.3/mieru-panel-v0.2.3-linux-arm64.tar.gz)

## License

MIT
