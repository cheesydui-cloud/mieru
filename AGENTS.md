# Agent notes (this repo)

## User ops preference

**After every code change that ships to production (panel/agent/scripts), always end the reply with fixed one-line upgrade commands** — do not wait for the user to ask.

### Default (always latest from GitHub)

```bash
# 面板
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash

# 任意已安装过的节点 agent（自动读 /etc/mieru-agent.env）
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash
```

### Pin a version

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=vX.Y.Z bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=vX.Y.Z bash
```

### CN mirror for raw GitHub

```bash
curl -fsSL https://ghfast.top/https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
curl -fsSL https://ghfast.top/https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash
```

After agent upgrade: tell user to click **重建配置** on the panel when config/plugins changed.

## Reply template (end of ship notes)

```
### 一键升级
**面板**
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash

**节点（cm7 / US 等）**
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash

然后面板点「重建配置」。
```
