# Changelog

## [0.4.36] - 2026-08-02

### 操作反馈贴按钮

- 取消右下角 / 页顶飘字 toast
- 成功/失败提示改到**对应操作按钮附近**（设置各区块、弹窗创建/保存底栏、页面操作条）
- 弹窗保存成功会短暂显示提示再关闭，避免一闪而过看不到

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.36 bash
```

## [0.4.35] - 2026-08-02

### Cloudflare 三按钮互不联动

- **保存 CF 配置**：只写 Zone ID / Token / 橙云默认
- **测试连接**：验证 Token+Zone（表单有新 Token 时先静默落库再测），不再带动「保存」loading
- **清除 Token**：只删 API Token，保留 Zone ID；独立 loading
- 三个按钮各自 disabled，点一个不会三个一起变灰
- Zone ID 提示改为「32 位 ID，不是域名」

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.35 bash
```

## [0.4.34] - 2026-08-02

### 设置页保存按钮互不影响

- 「保存」面板品牌 与 「保存 CF 配置」拆成独立请求状态
- 点 CF 保存时，上方「保存」不再一起变灰/抖动
- 改密 / 备份下载各自 loading，互不抢用

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.34 bash
```

## [0.4.33] - 2026-08-02

### 侧栏视觉重设计

- 侧栏加宽至 **248px**，间距与圆角更松弛
- 导航分组：**工作台** / **系统**，每项带图标 + 副标题
- 品牌区展示「管理控制台」；底部链路卡片「手机 → 前置 → 家宽」
- 当前项左侧强调条 + 白底浮起，桌面仍固定侧栏（抽屉仅 ≤768px）

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.33 bash
```

## [0.4.32] - 2026-08-02

### 桌面端侧栏

- 汉堡菜单 / 侧栏「关闭」仅在 **≤768px**（手机/小平板）出现
- 电脑端始终固定左侧导航，不再误进抽屉模式

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.32 bash
```

## [0.4.31] - 2026-08-02

### 查询页 / 退出 / Cloudflare 加域名

- **查询页入口只显示主机名**，不再展示 `:端口`（扫码 / YAML 仍带完整端口）
- 查询页右上角增加 **退出**（关闭页/空白页）；管理台与用户 Portal 按钮改为「退出登录」
- **Cloudflare 一键加域名**
  - 设置页配置 API Token + Zone ID
  - 新建/编辑节点：填公网 IP + 接入域名 →「CF 添加/更新解析」写 A/AAAA
  - 默认仅 DNS（灰云）；橙云可选但不建议用于自定义端口入口
  - API：`POST /api/admin/cloudflare/dns`、`POST /api/admin/cloudflare/test`

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.31 bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.31 bash
```

## [0.4.30] - 2026-08-02

### 运营优化合集（P0–P3）

**P0 可见性 / 安全**
- 总览展示最近一次「重建配置」状态（成功/失败、时间、原因）
- 心跳超时阈值统一为 **60 秒**，超时节点在列表/诊断中视为离线
- 设置页强调 **面板地址（panel_url）** 对查询页/订阅外链的影响
- 查询页提示订阅泄露可找管理员重置；后台「更多」增加 **重置订阅**

**P1 体验**
- 手机端侧栏抽屉导航（☰）
- 用户批量：启用 / 停用 / 删除 / +30 天 / +50G
- 开户成功可一键复制查询页；列表在倍率时显示「页显」已用
- 隧道入口展示与分享一致：**填域名用域名，否则 IP**

**P2 运维**
- 总览卡片：3 天内到期 / 超流 / 已到期
- 落地计量提示（Agent 版本 / 上报沉默）
- 审计日志搜索与动作过滤

**P3 加固**
- 设置页提示默认 JWT / 宽松 CORS
- `GET /api/admin/backup` 导出 JSON 快照（不含 agent_token / 密码）
- `POST /api/admin/users/batch`、`GET /api/admin/rebuild-status`

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.30 bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.30 bash
```

升完强制刷新浏览器；配置/插件有变时在面板点「重建配置」。

## [0.4.27] - 2026-08-03

### 填了域名就用域名当入口

- 节点填了 **域名（hostname）** 时，查询页「入口」、扫码 `mierus://`、订阅/YAML 客户端地址优先用域名
- **未填域名** 时仍显示公网 IP（与以前一致）
- 占位域名（`*.example.com` 等）仍会回退到 IP，避免无效主机名

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.27 bash
```

升完强制刷新浏览器。改完节点域名后建议点一次「重建配置」让订阅/YAML 同步。

## [0.4.26] - 2026-08-02

### 查询页显示倍率

- 用户「更多」→ **倍率设置**（0.1～100，默认 1）
- 仅放大用户查询页的 **已用 / 今日 / 实时**；**配额不变**（圆环会随倍率变满）
- 管理员用户列表仍显示真实流量；列表用户名旁显示 `×N` 标记
- `POST /api/admin/users/:id/display-multiplier`；公开 `GET /api/u/:token` 返回已缩放数值 + `display_multiplier`
- 真实计量、超流判定、节点配置不受影响

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.26 bash
```

升完强制刷新浏览器。

## [0.4.25] - 2026-08-02

### 查询页：扫码 + YAML

- 公开查询页 `/u/{token}` 增加与管理端「扫码/配置」相同内容：
  - mierus:// 节点二维码与链接
  - Mihomo/Clash Meta YAML 预览、复制、下载
  - 多入口时列出全部入口
- `GET /api/u/:token` 返回 `share_url` / `entries` / `mihomo_yaml`（不单独返回 `proxy_password` 字段）

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.25 bash
```

升完强制刷新浏览器。

## [0.4.24] - 2026-08-02

### 用户查询页（一键分享）

- 用户列表「更多」→ **复制查询页**：生成可分享链接 `/u/{sub_token}`
- 免登录只读页：用户名、状态、到期、已用/配额、今日流量、实时速率、隧道/入口（不展示代理密码）
- 公开 API：`GET /api/u/:token`（与订阅 token 相同，无密码字段）
- 用户列表接口附带 `info_url` 字段

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.24 bash
```

升完强制刷新浏览器。本功能以面板为主；节点 agent 可选同升。

## [0.4.23] - 2026-08-02

### 自动重建配置（不用再手动点）

- **面板启动/升级后**自动 RebuildAll，节点下次心跳拉新配置
- **Agent 启动/自升级后**强制 apply 一次（版本号不变也会重新拉起插件）
- **用户到期/超流**翻转时自动重建，尽快从落地 mita 踢掉
- 开户/改隧道仍同步重建；「重建配置」按钮保留应急

### 修复用户「更多」菜单被裁切

- 「更多」下拉改为 Teleport 到 body 的 fixed 浮层，不再被隧道分组 / 表格 overflow 裁掉
- 点击外部、滚动、缩放自动关闭；空间不够时向上展开
- 表格容器改为 overflow:auto，窄屏可横滑操作列

> 版本说明：当前线已是 0.4.x，本次发 **v0.4.23**（不能回退到 0.2.3）。

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.23 bash
```

升完强制刷新。节点建议同步升 agent（启动强制 apply 依赖新 agent）。

## [0.4.22] - 2026-08-02

### 用户页：每条隧道分组 + 组内开户

- 所有已创建隧道都会出现在用户页（含 0 人空组），不再只显示「已有用户」的隧道
- 组头隧道名旁增加 **开户**，点开自动绑定该隧道
- 空组内也有「在此隧道开户」快捷入口
- 修复用户页复制密码/链接（恢复 copy 封装）

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.22 bash
```

升完强制刷新。

## [0.4.21] - 2026-08-02

### 用户按隧道分组 + 节点升级提示精简

- **用户列表**：按隧道分组展示，每组显示人数、入口与路径摘要，一眼看出谁在哪条隧道
- **节点**：去掉与 Agent 版本列重复的「已升至 vX」黄条，仅保留排队/升级中/失败/配置未生效

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.21 bash
```

升完强制刷新。

## [0.4.20] - 2026-08-02

### 面板体验大包

- **用户**：顶部小统计芯片（正常/3天内到期/超流/停用/到期）一点即筛；重置密码改为弹窗 + 一键复制
- **隧道**：路径显示「前置名 IP:入口 → 落地名 mita口 · 用户数」；点行复制入口；探测显示上次时间
- **总览**：按真实隧道画边；问题可点跳转；新增 Agent 落后 / 配置未生效 / 流量上报沉默统计
- **节点**：未心跳高亮；安装命令含防火墙说明；升级失败/排队写进节点行；落地计量状态提示
- **品牌**：登录副标题可配；自定义 favicon/logo 上传
- **安全**：登录失败限速；会话过期明确提示；设置页展示操作审计；删用户/重置密码记审计
- **简化**：节点默认只显示前置/落地；隧道策略收进高级

### 升级（面板必升）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.20 bash
```

升完强制刷新。节点 Agent 可选升（计量提示依赖 ≥v0.4.17）。

## [0.4.19] - 2026-08-02

### 新建表单：去掉多余预设，默认留白

- **用户开户**：不再默认 100G、不再自动选第一条隧道；到期/入口 IP/端口均可空
- **新建隧道**：不再预选第一个前置/落地，也不自动填入口端口（placeholder 仍提示建议空闲口；保存时若留空仍会自动占下一个空闲口）
- **编辑节点**：无真实端口时不再伪造 10401/10001
- 套餐快捷按钮（体验/月卡/不限）仍保留，需要时手动点

### 升级（面板）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.19 bash
```

升完强制刷新页面。

## [0.4.18] - 2026-07-30

### 面板名称 / 浏览器图标

- **根因**：设置里的「面板名称」只写入数据库，侧栏/登录页/浏览器标签仍写死 `Mieru`，且没有 favicon
- 新增公开接口 `GET /api/brand`（登录前可用）
- 侧栏、登录页读取 `panel_name`；保存后即时刷新
- 浏览器标题改为「{名称} 控制台」
- 根据名称首字生成标签页图标（favicon），并提供默认 `/favicon.svg`

### 升级（面板必升）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.18 bash
```

升完强制刷新页面（Ctrl/Cmd+Shift+R）。设置里名称保存后侧栏与标签应立即变化。


## [0.4.17] - 2026-07-30

### 流量 / 实时网速（根因修复）

认真扫完整条链路后定位并修复：

1. **Agent 是否采样看错了开关**：以前只看安装时的 `AGENT_ROLE`。若落地机 env 写成 entry/relay，或改过节点角色，**永远不跑 reportTraffic**，UI 永远 0。现在按 **desired 配置里是否有 `mita_server` / role=exit|hybrid** 决定，并在每次 pull 同步 role。
2. **计量数据源太脆**：只解析 `mita get users` 的 1 日人类可读表。现优先读 **`mita get metrics` JSON 绝对计数**（`users.*.DownloadBytes/UploadBytes`），再回退到 get users 表。
3. 保留 map 加锁、UDS、诊断日志。

### 升级（落地 Agent 必须升）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.17 bash
```

升完后落地机：

```bash
journalctl -u mieru-agent -n 50 --no-pager | grep -E 'traffic|metering'
# 应看到: traffic metering enabled=true
# 有流量时: traffic: ok matched=...
```


## [0.4.16] - 2026-07-30

### 流量 / 实时网速（再修）

- 计量 map 加锁：pull 与 1s 采样并发不再竞态
- system 安装的 mita 也带上 `MITA_UDS_PATH`，CLI 与 daemon 同一 socket
- 优先用 agent 自带 `bin/mita`，与 mita_server 插件一致
- 解析 `mita get users` 更稳（按 size token 找上下行列）
- 限速诊断日志：`traffic: ok …` / `post failed` / `mita sample failed`

### 升级（落地 Agent 必须升）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.16 bash
```


## [0.4.15] - 2026-07-30

### 流量 / 实时网速修复

- **根因**：Agent 重启后配置版本未变会跳过 `apply`，内存里的 `userByName` 为空，`mita get users` 统计对不上面板用户 ID，流量与实时 bps 一直为 0
- 每次 pull 即使跳过 apply 也会 **重建计量用户表**
- 启动时从 `desired.json` 恢复用户映射，立刻可上报
- 解析 0 行 / 跳过未映射用户时打日志，方便 journalctl 排查

### 升级（落地 Agent 必须升）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.15 bash
```

面板可选同步：

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.15 bash
```


## [0.4.14] - 2026-07-30

### 同一前置 · 多落地 · 强制不同入口端口

- 创建/编辑隧道时：同前置上每条隧道的入口端口必须不同（冲突直接拒绝）
- 留空入口端口 → 自动占用池内下一个空闲口并**写死到 hops**（避免两条隧道抢同一口）
- 同一前置 + 同一落地只允许一条隧道（下拉禁用「此前置已用」）
- 表单显示已占用端口与建议空闲口；保存成功提示实际入口端口

示例：前置 10401→JP，再加落地自动 10402→Rightlayer。

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.14 bash
```


## [0.4.13] - 2026-07-30

### 用户列表

- **删除**提到操作主列（扫码 / 编辑 / 续期 / 停用 / **删除**），不再藏在「更多」里被挤没
- 操作列加宽，确认文案带用户名

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.13 bash
```


## [0.4.12] - 2026-07-30

### 新建节点表单

- 新建时**全部留白**（不再预填 10401–10499 / cn）
- 端口与区域用 placeholder 提示，须自行填写

### 删除节点 = 真正停用

- 删除节点时：删除经过它的隧道、解绑相关用户、清除 desired 配置
- 在线 Agent 下次心跳 **401** 后 **Stop 全部服务**（tcp_forward / socks_in / mita / mieru / nft）
- 删除确认提示影响范围

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.12 bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.12 bash
```


## [0.4.11] - 2026-07-30

### 删隧道后前置真正停监听

- 前置 entry/relay **零隧道时不再回退 socks_in**（以前删光隧道端口仍在听，节点看起来“还有网络”）
- Agent `apply`：desired 里没有的 `tcp_forward` / `socks_in` / `nft_forward` 会 **Stop 释放端口**
- 落地 mita 用户只下发 **绑定了经过该落地的隧道** 的用户，避免幽灵账号

### 端口占用提示

- 冲突错误写明占用隧道名：`入口端口 10401 已被隧道「CM7-LAYER」(#id) 占用`
- 隧道表单已占用列表显示 `10401（CM7-LAYER）`

> 截图里若仍提示 10401 被占用，请先确认列表里是否还有 **CM7-LAYER**（或其它钉死 10401 的隧道）；删掉它或换端口即可。

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.11 bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.11 bash
```


## [0.4.10] - 2026-08-01

### 前置端口池

- **不是写死 10403**：自定义入口端口校验读节点真实端口池
- 历史前置只存了单端口（如 cm7 `10401–10401`）时，自动按 `min..(min+98)` 展开（默认 10401–10499），与隧道 UI 一致
- 重建配置时把展开后的池写回节点，编辑页不再显示成单端口

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.10 bash
```


## [0.4.9] - 2026-08-01

### UI

- 侧栏顺序：**总览 → 用户 → 隧道 → 节点 → 设置**（原「线路」更名为「隧道」）
- 隧道列表新增 **入口端口 / 落地端口** 列（与扫码、configgen 分配一致）
- 新建/编辑隧道可 **自定义前置入口端口**（须在前置端口池内；留空自动分配；冲突校验）
- 总览隧道表同步显示入口端口

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.9 bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.9 bash
```


## [0.4.8] - 2026-08-01

### 发布完整性

- `pack-release.sh` 生成 **SHA256SUMS**（与每个 `.tar.gz` 一并上传）
- `install-panel.sh` / `install-agent.sh` 下载后自动校验；失败拒绝安装
- 无 SHA256SUMS 的旧 release 仍可安装（跳过校验）；`MIERU_SKIP_CHECKSUM=1` 可强制跳过

### 含 v0.4.7 全部修复

- 扫码显示名：`profile=用户名-M月D日`（不再是 default）
- 前置节点端口起/止 UI（10401–10499）
- 多落地 per-route 端口 + 远程升级 Agent

### 升级

```bash
# 面板
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.8 bash

# 节点 agent（cm7 等多落地必须）
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.8 bash
```


## [0.4.7] - 2026-08-01

### 扫码显示名修复

- 官方客户端用查询参数 **`profile`** 作为节点名，不是 `#fragment`
- 之前写死 `profile=default`，扫码后客户端只显示 default
- 现改为 `profile=用户名-M月D日`（永久用户仅用户名），并保留 fragment 作备用

### 节点端口 UI

- 前置节点编辑改为 **端口起 / 端口止**（默认 10401–10499），列表显示 `10401–10499`
- 落地仍为单端口（mita）
- 说明：端口池是分配上限，实际只监听「有线路」的端口；商家 DNAT 需放行整段

### 升级（仅面板即可；Agent 可选）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.7 bash
```


## [0.4.6] - 2026-08-01

### 节点一键升级 Agent

- 节点列表每行 **升级** 按钮 + 工具栏 **全部升级 Agent**
- 面板经心跳下发 `upgrade_job`，Agent 自动下载 release 包、替换二进制并 `systemctl restart`
- 旧 Agent（< v0.4.6）不识别该任务：首次仍需在机器上跑一次「安装」命令，之后即可远程推送
- 状态：排队中 / 升级中 / 失败原因；版本与面板一致后自动清除

### 多落地 / 多线路（同一前置）

- **根因**：cm7 等前置只生成「单端口 → 第一条线路的落地」，新线路用户仍走旧出口 IP
- **修复**：每条启用线路在前置各占一个 listen 端口，`tcp_forward` 支持 `rules[]` 多目标
  - 端口分配：hop.Port 优先 → 否则从节点 PortMin 池顺序分配（如 10401、10402）
  - 单线路 / 单端口节点仍兼容原来的 PublicServicePort
- 用户扫码 / 分享 / 列表入口端口 = 该用户线路对应的前置端口
- 运营商 DNAT：每个公开端口需映射到 cm7 内网同端口（不要只开一个）

### 升级（面板 + **cm7 前置 agent** 都要升）

```bash
# 面板
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.6 bash

# cm7 前置 agent（多端口转发必须）
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.6 bash
```

升级后在面板「重建配置」或等 agent 拉取；确认新线路用户入口端口与旧线路不同，并在商家侧放行对应 DNAT。


## [0.4.5] - 2026-08-01

### UX

- 扫码 / 节点链接 / YAML 显示名改为 **用户名-M月D日**（如 `aaa-8月31日`），导入客户端后备注一致
- 用户列表去掉多余 **YAML** 按钮（扫码弹窗内仍可下载）

### 升级（仅面板）

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.5 bash
```


## [0.4.4] - 2026-08-01

### 用户运营

- 列表：线路名、入口、备注、到期临近标黄、流量进度条
- 搜索 / 状态筛选；开户套餐快捷（体验 / 月卡 / 不限）
- 一键 **续期** / **加流量** / **停用·启用**（重置密码、删除收进「更多」）

### 实时网速

- Exit agent 每秒读 `mita get users`，按 1 日累计差算上下行 bps
- 用户页每秒拉 `/api/admin/metrics/rates`；超过 8s 无上报显示 0（不卡死旧值）

### 升级

面板 + **落地 exit agent**（前置可不动）：

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.4 bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.4 bash
```


## [0.4.3] - 2026-08-01

### UX

- 去掉表单输入框里的预设提示文字（placeholder），字段留空自行填写

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.3 bash
```

（仅面板）


## [0.4.2] - 2026-08-01

### Added — Mihomo YAML + 用户编辑

- 用户页可 **下载 / 复制 Mihomo（Clash Meta）YAML**（`type: mieru`，连前置，出口家宽）
- 管理端 `GET /api/admin/users/:id/mihomo.yaml`；公开订阅 `GET /sub/:token/mihomo.yaml`
- 用户 **编辑**：状态 / 到期 / 流量 / 线路 / 入口 IP·端口 / 备注
- 扫码弹窗：去掉「订阅地址」、二维码居中、YAML 预览与下载

### Fixed

- 用户列表表格列宽对齐（状态 / 到期 / 流量 / 线路 / 操作）
- 解绑线路：编辑时 `route_id=0` 清空绑定

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.2 bash
```

（仅面板；agent 可不动）


## [0.4.1] - 2026-08-01

### Fixed — 线路探测「商家入口不通」误报

- 商家前置公网 IP 无 Agent，无法从入口侧 dial；此前标「不通」拉低健康度为「部分通」
- 现标为 **不可测**（skip），不计入失败；健康度只看 **前置 Agent → 落地 mita**
- 探测弹窗说明与 hop 展示同步

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.1 bash
```

（仅面板；agent 可不动）


## [0.4.0] - 2026-08-01

### UI — 线框层次 + 产品精简

- 视觉：更深描边、下划线 Tab、表格外框、链接式操作（对齐常见面板截图风格）
- 导航：去掉独立「落地」页；节点页 Tab = 全部 / 前置 / 落地
- 总览：拓扑健康（前置→落地→家宽）、diagnose 问题列表、一键重建
- 节点：单端口表单（不再填 port 范围）；角色文案前置/落地
- 线路：简化为 前置 + 落地；capability 用 tcp_forward
- 诊断：relay/entry 期望 tcp_forward（不再误报缺 socks_in）

### 升级

```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.4.0 bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.4.0 bash
```


## [0.3.13] - 2026-08-01

### Fixed — 连上约 30 秒就断网

- Agent 每 10s 拉配置后**不再重复 apply**（版本未变则跳过），避免 `tcp_forward` / `mita` 周期性 stop 掐断会话。
- `tcp_forward` / `socks_in` / `mieru_client` / `mita` 配置未变时幂等，不 rebind、不 stop/start。
- `tcp_forward` 开启 TCP keepalive，长连接更稳。

### 升级

```bash
# 面板
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | MIERU_VERSION=v0.3.13 bash

# 所有 agent（cm7 前置 + 美国家宽 exit 都要升）
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.3.13 bash
```

升完后手机重连；`journalctl -u mieru-agent -f` 不应再每 10s 出现 `applied config` / `[tcp_forward] 10401 →`。


## [0.3.12] - 2026-08-01

### Fixed — 侧栏版本显示旧号
- `/api/version` 加 `Cache-Control: no-store`，前端带时间戳请求，避免浏览器/代理缓存旧版本。
- **说明**：侧栏版本 = 浏览器访问的那台 panel 的版本。若在 A 机升级 panel，却打开 B 机面板地址，会一直显示 B 的旧版本。


## [0.3.11] - 2026-08-01

### Fixed — 面板节点版本与 `mieru-agent -version` 不一致
- 心跳上报的 `agent_version` 改为与 CLI 同一来源（`main.Version` / ldflags），不再只靠包内常量。
- 列表 API 规范化版本字符串（去掉多余 `v` 前缀）。

### Ops
```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | MIERU_VERSION=v0.3.11 bash -s -- \
  --panel-url ... --node-id ... --token ... --role relay
```
升 agent 后等一次心跳（约 5s），硬刷新面板。


## [0.3.10] - 2026-08-01

### Fixed — tcp_forward 宽端口段 bind 冲突
- 日志 `listen 10401: address already in use` + 打开 10401–10465 数十个监听：前置只应监听**扫码那一个口**。
- entry/relay 的 `tcp_forward` 固定单端口（`listen_port`），不再展开 `port_min..port_max`。
- 重载配置时先完整 stop 再 listen，并 `SO_REUSEADDR`，避免 rebuild 时端口占死。

### Ops
```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash
```
**cm7 agent 必升**；升完重建配置。节点端口建议改成 `10401~10401`。


## [0.3.9] - 2026-08-01

### Added — 国内前置 + 美国家宽落地（TK 目标链路）
- 明确产品路径：`手机 mierus → 国内前置(entry/relay) → 透明 TCP → 美国 mita → 家宽出口`。
- 新增 agent 插件 **`tcp_forward`**：entry/relay 不再用 socks5 给客户端，而是把公网端口字节流转到出口 mita。
- 扫码/订阅 advertise **前置 host:port**；用户密码仍在 **美国 mita** 校验，出口 IP 是家宽。
- 单机 exit 线路行为不变（直连 mita）。

### Ops
```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash
```
**panel + cm7 agent 都要升**；美国 exit 建议也升。升完后面板「重建配置」。

配置：
- cm7：角色 **relay**（或 entry），公网 IP=`211.136.162.184`，端口 `10401~10401`，接入域名空
- 美国：角色 **exit**，公网/可达地址 + mita 端口，用户已同步
- 线路：`cm7 → 美国`；用户绑此线路
- 扫码应是 `mierus://…@211.136.162.184?port=10401…`


## [0.3.8] - 2026-08-01

### Fixed — 扫码对齐 OneClick：连客户端可触达的 mita，不是美国落地
- 对照 **ike-sh/mieru-OneClick**：客户端永远连 **mita**；`mierus://` 的 host/port 可以是「展示入口」（前置 IP），服务端仍监听真实端口，前置 DNAT 过去。
- 分享解析改为优先线路上 **第一台 exit/hybrid**（如 cm7 前置），不再默认最后一跳美国 exit。
- 用户 `entry_host`/`entry_port` = OneClick `--advertise-host` / `--advertise-port`（仅改链接展示）。
- 节点扫码 host 优先 **公网 IP**，忽略 `*.example.com` 等占位接入域名。

### Ops
```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
```
面板升级即可。节点请设：角色 exit/hybrid、公网 IP=前置 IP、端口=商家映射口、接入域名留空。


## [0.3.7] - 2026-08-01

### Changed — 客户端分享改为官方 mierus://（直连出口 mita）
- 运营商要求客户端必须使用 **mieru 协议**，不再生成 `socks5://` 入口链接。
- 扫码 / 节点链接 / 订阅均为 `mierus://user:pass@host?handshake-mode=HANDSHAKE_NO_WAIT&mtu=1400&multiplexing=MULTIPLEXING_OFF&port=N&profile=default&protocol=TCP`。
- 默认指向线路 **exit/hybrid 的 mita**（用户账号已同步到 mita）；`entry_host`/`entry_port` 仍可作为公网 IP/域名覆盖。
- 订阅 `/sub/:token` 改为纯文本一行一个 `mierus://`（非 Clash SOCKS YAML）。
- 管理端文案同步为「官方 mieru 客户端 / OneClick」。

### Ops
```bash
curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-panel.sh | bash
```
面板升级即可（分享逻辑在 panel；agent 可不升）。

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
