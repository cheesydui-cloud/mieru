<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api, clearSession, getToken } from '../api'
import { setBrandMeta } from '../brand'
import { useFlash } from '../flash'

const pageFlash = useFlash()
const brandFlash = useFlash()
const cfFlash = useFlash()
const backupFlash = useFlash()
const migFlash = useFlash()
const pwFlash = useFlash()
const savingBrand = ref(false)
const savingCF = ref(false)
const clearingCF = ref(false)
const savingPw = ref(false)
const backupLoading = ref(false)
const migExportLoading = ref(false)
const migImportLoading = ref(false)
const syncURLLoading = ref(false)
const syncURLFlash = useFlash()
const migFile = ref(null)
const migPreview = ref(null)
const audit = ref([])
const auditQ = ref('')
const auditAction = ref('')
const securityHints = ref([])
const jwtDefault = ref(false)
const corsWide = ref(false)
const form = reactive({
  panel_url: '',
  panel_name: '',
  panel_subtitle: '',
  panel_favicon: '',
  panel_url_set: false,
  version: '',
  admin_user: 'admin',
  cf_zone_id: '',
  cf_api_token: '',
  cf_token_set: false,
  cf_configured: false,
  cf_proxied_default: false,
})
const cfTesting = ref(false)
const pw = reactive({
  current_password: '',
  new_password: '',
  new_password2: '',
  username: '',
})

const filteredAudit = computed(() => {
  let list = audit.value || []
  const q = auditQ.value.trim().toLowerCase()
  const act = auditAction.value.trim().toLowerCase()
  if (act) list = list.filter((a) => String(a.action || '').toLowerCase().includes(act))
  if (q) {
    list = list.filter((a) => {
      const hay = `${a.actor || ''} ${a.action || ''} ${a.target || ''} ${a.detail || ''}`.toLowerCase()
      return hay.includes(q)
    })
  }
  return list
})

async function load() {
  try {
    const [s, logs] = await Promise.all([
      api('/api/admin/settings'),
      api('/api/admin/audit?limit=200').catch(() => []),
    ])
    form.panel_url = s.panel_url || ''
    form.panel_name = s.panel_name || 'Mieru Panel'
    form.panel_subtitle = s.panel_subtitle || ''
    form.panel_favicon = s.panel_favicon || ''
    setBrandMeta({
      name: s.panel_name,
      subtitle: s.panel_subtitle,
      faviconData: s.panel_favicon || '',
    })
    form.panel_url_set = !!s.panel_url_set
    form.version = s.version || ''
    form.admin_user = s.admin_user || 'admin'
    form.cf_zone_id = s.cf_zone_id || ''
    form.cf_token_set = !!s.cf_token_set
    form.cf_configured = !!s.cf_configured
    form.cf_proxied_default = !!s.cf_proxied_default
    // never echo real token; placeholder if set
    form.cf_api_token = s.cf_token_set ? '********' : ''
    pw.username = form.admin_user
    securityHints.value = Array.isArray(s.security_hints) ? s.security_hints : []
    jwtDefault.value = !!s.jwt_is_default
    corsWide.value = !!s.cors_wide_open
    audit.value = Array.isArray(logs) ? logs : []
    pageFlash.clear()
  } catch (e) {
    pageFlash.err(e.message)
  }
}

async function downloadBackup() {
  backupLoading.value = true
  backupFlash.clear()
  try {
    const tok = getToken()
    const res = await fetch('/api/admin/backup', {
      headers: {
        Accept: 'application/json',
        ...(tok ? { Authorization: `Bearer ${tok}` } : {}),
      },
      cache: 'no-store',
    })
    const text = await res.text()
    if (!res.ok) {
      let msg = text
      try {
        msg = JSON.parse(text).error || text
      } catch {
        /* keep */
      }
      throw new Error(msg || res.statusText)
    }
    const blob = new Blob([text], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `mieru-panel-backup-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.json`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(a.href)
    backupFlash.ok('备份已下载（不含密钥，不能用于换机）')
  } catch (e) {
    backupFlash.err(e.message)
  } finally {
    backupLoading.value = false
  }
}

async function downloadMigration() {
  if (
    !confirm(
      '将下载【含密钥】的完整迁移包。\n\n包含：agent_token、用户代理密码、管理员密码哈希、backbone/CF 等。\n任何拿到该文件的人可控制节点与用户。\n\n确认导出？',
    )
  ) {
    return
  }
  migExportLoading.value = true
  migFlash.clear()
  try {
    const tok = getToken()
    const res = await fetch('/api/admin/migration/export', {
      headers: {
        Accept: 'application/json',
        ...(tok ? { Authorization: `Bearer ${tok}` } : {}),
      },
      cache: 'no-store',
    })
    const text = await res.text()
    if (!res.ok) {
      let msg = text
      try {
        msg = JSON.parse(text).error || text
      } catch {
        /* keep */
      }
      throw new Error(msg || res.statusText)
    }
    // Guard: never save a non-migration payload as "完整迁移包"
    let parsed
    try {
      parsed = JSON.parse(text)
    } catch {
      throw new Error('导出响应不是合法 JSON，请检查面板版本是否 ≥ v0.5.8')
    }
    if (parsed?.format !== 'mieru-panel-migration' || !parsed?.secrets_included) {
      throw new Error(
        '导出内容不是完整迁移包（缺少 format/secrets）。请升级面板到最新版后再导出，不要使用上方「下载备份」。',
      )
    }
    const nNodes = Array.isArray(parsed.nodes) ? parsed.nodes.length : 0
    const nUsers = Array.isArray(parsed.users) ? parsed.users.length : 0
    const blob = new Blob([text], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `mieru-panel-migration-full-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.json`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(a.href)
    migFlash.ok(
      `完整迁移包已下载（含密钥）：节点 ${nNodes} / 用户 ${nUsers}。文件名含 migration-full，请离线保管。`,
    )
  } catch (e) {
    migFlash.err(e.message)
  } finally {
    migExportLoading.value = false
  }
}

function onMigrationFile(ev) {
  migFlash.clear()
  migPreview.value = null
  migFile.value = null
  const f = ev.target.files && ev.target.files[0]
  if (!f) return
  // Soft filename hint (not hard-fail): backup vs migration-full
  const fname = String(f.name || '').toLowerCase()
  const reader = new FileReader()
  reader.onload = () => {
    try {
      const data = JSON.parse(String(reader.result || ''))
      if (data.format !== 'mieru-panel-migration') {
        // Common mistake: selecting the soft backup (no format / no secrets)
        const looksLikeBackup =
          data.format === 'mieru-panel-backup' ||
          (!data.format &&
            data.exported_at &&
            (Array.isArray(data.nodes) || Array.isArray(data.users)) &&
            !data.secrets_included &&
            !data.admins)
        if (looksLikeBackup || fname.includes('backup')) {
          throw new Error(
            '这是「安全备份」（不含密钥），不能用于换机导入。请在旧机点红色按钮「导出完整迁移包」，文件名应含 migration-full。',
          )
        }
        throw new Error(
          `不是完整迁移包（format=${data.format || '空'}）。请用「导出完整迁移包」生成的文件（不是上方「下载备份」）。`,
        )
      }
      if (!data.secrets_included) {
        throw new Error('该文件未包含密钥，不能用于导入')
      }
      // Soft check: nodes should carry agent_token
      const nodes = Array.isArray(data.nodes) ? data.nodes : []
      const missingTok = nodes.filter((n) => !String(n?.agent_token || '').trim()).length
      if (nodes.length && missingTok === nodes.length) {
        throw new Error('迁移包节点缺少 agent_token，无法恢复节点登录。请重新在旧机导出完整迁移包。')
      }
      const users = Array.isArray(data.users) ? data.users : []
      const missingPw = users.filter((u) => !String(u?.proxy_password || '').trim()).length
      if (users.length && missingPw === users.length) {
        throw new Error('迁移包用户缺少 proxy_password，无法恢复代理密码。请重新导出完整迁移包。')
      }
      migFile.value = data
      migPreview.value = {
        format_version: data.format_version,
        exported_at: data.exported_at || '—',
        panel_version: data.panel_version || '—',
        nodes: nodes.length,
        routes: Array.isArray(data.routes) ? data.routes.length : 0,
        users: users.length,
        announcements: Array.isArray(data.announcements) ? data.announcements.length : 0,
        traffic: Array.isArray(data.traffic_hourly) ? data.traffic_hourly.length : 0,
        settings: data.settings ? Object.keys(data.settings).length : 0,
        admins: Array.isArray(data.admins) ? data.admins.length : 0,
        file_name: f.name,
      }
    } catch (e) {
      migFlash.err(e.message || '文件解析失败')
      if (ev.target) ev.target.value = ''
    }
  }
  reader.onerror = () => {
    migFlash.err('读取文件失败')
  }
  reader.readAsText(f)
}

async function doImportMigration() {
  if (!migFile.value) {
    migFlash.err('请先选择迁移包文件')
    return
  }
  if (
    !confirm(
      '导入将【覆盖】本机全部面板数据：\n设置 / 节点 / 隧道 / 用户 / 公告 / 流量历史 / 管理员密码。\n\n当前本机数据会被清空且不可恢复（除非你另有备份）。\n确认继续？',
    )
  ) {
    return
  }
  if (!confirm('再次确认：这是不可逆的覆盖导入。确定导入？')) {
    return
  }
  migImportLoading.value = true
  migFlash.clear()
  try {
    const tok = getToken()
    const res = await fetch('/api/admin/migration/import', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Confirm-Import': '1',
        ...(tok ? { Authorization: `Bearer ${tok}` } : {}),
      },
      body: JSON.stringify(migFile.value),
    })
    const text = await res.text()
    let data = null
    try {
      data = JSON.parse(text)
    } catch {
      /* keep */
    }
    if (!res.ok) {
      throw new Error((data && data.error) || text || res.statusText)
    }
    const n = data || {}
    migFlash.ok(
      `导入成功：节点 ${n.nodes ?? 0} / 隧道 ${n.routes ?? 0} / 用户 ${n.users ?? 0}` +
        (n.rebuild_ok === false ? `（配置重建失败：${n.rebuild_error || '未知'}）` : '，配置已重建') +
        '。请用旧面板管理员密码重新登录；若换了 IP/域名，改设置「面板公网地址」并点「同步 PANEL_URL 到节点」。',
    )
    migFile.value = null
    migPreview.value = null
    // credentials may have changed — force re-login shortly
    setTimeout(() => {
      clearSession()
      location.href = '/login'
    }, 3500)
  } catch (e) {
    migFlash.err(e.message)
  } finally {
    migImportLoading.value = false
  }
}

function onFaviconFile(ev) {
  const f = ev.target.files && ev.target.files[0]
  if (!f) return
  if (f.size > 100 * 1024) {
    brandFlash.err('图标请 ≤100KB')
    return
  }
  const reader = new FileReader()
  reader.onload = () => {
    form.panel_favicon = String(reader.result || '')
  }
  reader.readAsDataURL(f)
}

function clearFavicon() {
  form.panel_favicon = ''
}

function brandPayload() {
  return {
    panel_url: form.panel_url,
    panel_name: form.panel_name,
    panel_subtitle: form.panel_subtitle,
    panel_favicon: form.panel_favicon,
  }
}

function applySettingsResponse(res, { touchBrand = false, touchCF = false } = {}) {
  if (touchBrand) {
    form.panel_url = res.panel_url
    form.panel_name = res.panel_name
    form.panel_subtitle = res.panel_subtitle || ''
    form.panel_favicon = res.panel_favicon || ''
    form.panel_url_set = true
    setBrandMeta({
      name: res.panel_name,
      subtitle: res.panel_subtitle,
      faviconData: res.panel_favicon || '',
    })
  }
  if (touchCF || res.cf_configured !== undefined) {
    form.cf_configured = !!res.cf_configured
    form.cf_token_set = !!res.cf_token_set
    if (res.cf_zone_id !== undefined && res.cf_zone_id !== null) {
      form.cf_zone_id = res.cf_zone_id || form.cf_zone_id
    }
    form.cf_api_token = form.cf_token_set ? '********' : ''
  }
}

async function putSettingsBody(body) {
  return api('/api/admin/settings', {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}


/** 向在线节点下发当前面板地址（改写 /etc/mieru-agent.env 并重启 agent） */
async function syncPanelURLToNodes() {
  const url = (form.panel_url || '').trim()
  if (!url) {
    syncURLFlash.err('请先填写并保存「面板公网地址」')
    return
  }
  if (
    !confirm(
      `向所有在线节点推送 PANEL_URL？\n\n目标：${url}\n\n` +
        `• 节点须仍能连上当前面板一次（心跳）\n` +
        `• Agent 会改写 /etc/mieru-agent.env 并 systemctl restart\n` +
        `• 离线节点会被跳过，需 SSH 手动改\n` +
        `• 建议先点上方「保存」再同步`,
    )
  ) {
    return
  }
  syncURLLoading.value = true
  syncURLFlash.clear()
  try {
    // ensure settings saved first so panel_url in DB matches
    await putSettingsBody(brandPayload())
    const res = await api('/api/admin/nodes/sync-panel-url', {
      method: 'POST',
      body: JSON.stringify({ panel_url: url }),
    })
    const q = Array.isArray(res.queued) ? res.queued.length : 0
    const s = Array.isArray(res.skipped) ? res.skipped.length : 0
    syncURLFlash.ok(res.message || `已排队 ${q} 个节点` + (s ? `，跳过 ${s}` : ''))
  } catch (e) {
    syncURLFlash.err(e.message)
  } finally {
    syncURLLoading.value = false
  }
}

/** 仅保存面板品牌 / 地址（不改动 CF Token） */
async function saveBrandSettings() {
  savingBrand.value = true
  brandFlash.clear()
  try {
    const url = (form.panel_url || '').trim()
    const res = await putSettingsBody(brandPayload())
    applySettingsResponse(res, { touchBrand: true, touchCF: true })
    let extra = ''
    // Auto-push panel URL to all *online* nodes after save (offline still need 复制修复命令).
    if (url) {
      try {
        const sync = await api('/api/admin/nodes/sync-panel-url', {
          method: 'POST',
          body: JSON.stringify({ panel_url: url }),
        })
        const q = Array.isArray(sync.queued) ? sync.queued.length : 0
        const s = Array.isArray(sync.skipped) ? sync.skipped.length : 0
        extra =
          q || s
            ? `；已向 ${q} 个在线节点纠正面板地址` + (s ? `，${s} 个离线请用节点页「复制修复命令」` : '')
            : ''
      } catch (e) {
        extra = `；在线节点自动纠正失败：${e.message || e}`
      }
    }
    brandFlash.ok('面板设置已保存（名称 / 副标题 / 图标 / 地址）' + extra)
  } catch (e) {
    brandFlash.err(e.message)
  } finally {
    savingBrand.value = false
  }
}

function cfPayload() {
  const body = {
    ...brandPayload(),
    cf_zone_id: form.cf_zone_id,
    cf_proxied_default: !!form.cf_proxied_default,
  }
  const tok = (form.cf_api_token || '').trim()
  if (tok === 'clear') {
    body.cf_api_token = 'clear'
  } else if (tok && tok !== '********') {
    body.cf_api_token = tok
  }
  return body
}

function hasUnsavedCFToken() {
  const tok = (form.cf_api_token || '').trim()
  return !!tok && tok !== '********' && tok !== 'clear'
}

/** 写入 CF 字段到后端（内部用）。showToast=false 时不弹成功提示 */
async function persistCFSettings({ showToast = true } = {}) {
  const res = await putSettingsBody(cfPayload())
  applySettingsResponse(res, { touchBrand: true, touchCF: true })
  if (showToast) {
    cfFlash.ok(
      res.cf_configured
        ? 'Cloudflare 配置已保存'
        : '已保存（请填写 Token + Zone ID 后即可在节点页一键解析）',
    )
  }
  return res
}

/** 保存 CF 配置：Zone ID / Token / 橙云默认 */
async function saveCFSettings() {
  if (savingCF.value || clearingCF.value || cfTesting.value) return
  savingCF.value = true
  cfFlash.clear()
  try {
    await persistCFSettings({ showToast: true })
  } catch (e) {
    cfFlash.err(e.message)
  } finally {
    savingCF.value = false
  }
}

/**
 * 测试连接：只验证已保存（或表单里新输入）的 Token + Zone。
 * 若输入框里有新 Token，会先静默写入再测；不会驱动「保存/清除」按钮 loading。
 */
async function testCF() {
  if (cfTesting.value || savingCF.value || clearingCF.value) return
  cfTesting.value = true
  cfFlash.clear()
  try {
    // 新 Token / 改了 Zone 时先静默落库，否则后端只能测到旧值
    const zone = (form.cf_zone_id || '').trim()
    if (!zone) {
      throw new Error('请先填写 Zone ID（32 位，不是域名）')
    }
    if (hasUnsavedCFToken() || zone) {
      await persistCFSettings({ showToast: false })
    }
    const res = await api('/api/admin/cloudflare/test', { method: 'POST', body: '{}' })
    cfFlash.ok(
      res.zone_name
        ? `连接正常 · Zone ${res.zone_name}`
        : 'Cloudflare 连接正常',
    )
  } catch (e) {
    cfFlash.err(e.message)
  } finally {
    cfTesting.value = false
  }
}

/** 清除 Token：只删 API Token，保留 Zone ID / 橙云选项 */
async function clearCFTokenAndSave() {
  if (clearingCF.value || savingCF.value || cfTesting.value) return
  cfFlash.clear()
  if (!form.cf_token_set && !(form.cf_api_token || '').trim()) {
    cfFlash.err('当前没有已保存的 Token')
    return
  }
  clearingCF.value = true
  try {
    form.cf_api_token = 'clear'
    form.cf_token_set = false
    const body = {
      ...brandPayload(),
      cf_zone_id: form.cf_zone_id,
      cf_proxied_default: !!form.cf_proxied_default,
      cf_api_token: 'clear',
    }
    const res = await putSettingsBody(body)
    applySettingsResponse(res, { touchBrand: true, touchCF: true })
    form.cf_api_token = ''
    form.cf_token_set = false
    form.cf_configured = false
    cfFlash.ok('Cloudflare Token 已清除（Zone ID 仍保留）')
  } catch (e) {
    cfFlash.err(e.message)
  } finally {
    clearingCF.value = false
  }
}

async function changePassword() {
  pwFlash.clear()
  if (pw.new_password !== pw.new_password2) {
    pwFlash.err('两次新密码不一致')
    return
  }
  if (pw.new_password.length < 6) {
    pwFlash.err('新密码至少 6 位')
    return
  }
  savingPw.value = true
  try {
    await api('/api/admin/admin-password', {
      method: 'POST',
      body: JSON.stringify({
        username: pw.username || form.admin_user,
        current_password: pw.current_password,
        new_password: pw.new_password,
      }),
    })
    pwFlash.ok('管理员密码已修改，请牢记新密码')
    pw.current_password = ''
    pw.new_password = ''
    pw.new_password2 = ''
    await load()
  } catch (e) {
    pwFlash.err(e.message)
  } finally {
    savingPw.value = false
  }
}

function fmtTime(t) {
  if (!t) return '—'
  try {
    const d = new Date(t)
    if (Number.isNaN(d.getTime())) return String(t).slice(0, 19)
    return d.toLocaleString()
  } catch {
    return String(t)
  }
}

onMounted(load)
</script>

<template>
  <div
    v-if="pageFlash.msg"
    class="action-feedback page-action-feedback"
    :class="pageFlash.kind"
    @click="pageFlash.clear()"
  >{{ pageFlash.msg }}</div>

  <div class="page-tabs">
    <div class="page-tab active">设置</div>
  </div>

  <div
    v-if="securityHints.length || !form.panel_url_set"
    class="panel"
    style="border-color: var(--warning)"
  >
    <div class="panel-hd">
      <div>
        <h2>安全 / 外链提示</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">上线前建议处理</div>
      </div>
    </div>
    <div class="panel-bd" style="padding:14px 16px">
      <ul style="margin:0;padding-left:18px;line-height:1.6;font-size:13px">
        <li v-if="!form.panel_url_set" style="color:var(--warning)">
          未永久保存「面板地址」—— 用户查询页 / 订阅链接可能变成当前访问的 IP:端口，请在下方填写并保存。
        </li>
        <li v-for="(h, i) in securityHints" :key="i" style="color:var(--warning)">{{ h }}</li>
        <li v-if="jwtDefault" class="muted" style="font-size:12px">
          例：在 <code class="mono">/etc/mieru-panel.env</code> 设置
          <code class="mono">PANEL_JWT_SECRET=$(openssl rand -hex 32)</code> 后
          <code class="mono">systemctl restart mieru-panel</code>
        </li>
        <li v-if="corsWide" class="muted" style="font-size:12px">
          跨域 env：<code class="mono">PANEL_CORS</code>（默认 <code class="mono">*</code>，同域 SPA 可忽略）
        </li>
      </ul>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>面板</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          Agent 回连 · 查询页/订阅外链基准 · 品牌 · 安装命令
        </div>
      </div>
      <span class="badge mono" v-if="form.version">{{ form.version }}</span>
    </div>
    <div class="panel-bd" style="padding:16px">
      <div class="field" style="margin-bottom:14px">
        <label>面板地址（对外 URL，必填）</label>
        <input
          v-model="form.panel_url"
          placeholder="https://panel.example.com 或 http://IP:8080"
          :style="!form.panel_url_set ? 'border-color:var(--warning)' : ''"
        />
        <p class="help-text" style="margin-top:6px">
          用户查询页、订阅、扫码分享、Agent 回连都基于此地址。建议用域名；只写 IP:端口 会自动补 http://。
          保存后会<strong>自动纠正所有在线节点</strong>的 PANEL_URL；离线节点请到节点页点「复制修复命令」。
          <span v-if="!form.panel_url_set" style="color:var(--warning);font-weight:600">
            尚未永久保存
          </span>
          <span v-else style="color:var(--success)">已保存</span>
        </p>
      </div>
      <div class="field" style="margin-bottom:14px">
        <label>面板名称</label>
        <input v-model="form.panel_name" placeholder="例如：微动传媒" />
        <p class="help-text" style="margin-top:6px">
          显示在左侧栏、登录页、浏览器标签标题与图标首字。
        </p>
      </div>
      <div class="field" style="margin-bottom:14px">
        <label>登录页副标题</label>
        <input v-model="form.panel_subtitle" placeholder="管理节点、用户、隧道与落地计量" />
      </div>
      <div class="field" style="margin-bottom:16px">
        <label>自定义图标（favicon / logo，可选）</label>
        <div class="row-actions" style="align-items:center;gap:12px;margin-bottom:8px">
          <div
            class="brand-mark"
            style="width:40px;height:40px;font-size:16px;overflow:hidden"
          >
            <img
              v-if="form.panel_favicon"
              :src="form.panel_favicon"
              alt=""
              style="width:100%;height:100%;object-fit:cover"
            />
            <span v-else>{{ (form.panel_name || 'M').slice(0, 1) }}</span>
          </div>
          <input type="file" accept="image/png,image/jpeg,image/svg+xml,image/webp" @change="onFaviconFile" />
          <button v-if="form.panel_favicon" type="button" class="btn btn-ghost btn-sm" @click="clearFavicon">
            清除自定义
          </button>
        </div>
        <p class="help-text">PNG/SVG/WebP，建议 ≤100KB。清空后恢复名称首字图标。</p>
      </div>
      <div class="action-bar">
        <button type="button" class="btn btn-primary" :disabled="savingBrand" @click="saveBrandSettings">
          {{ savingBrand ? '保存中…' : '保存' }}
        </button>
        <button
          type="button"
          class="btn btn-ghost"
          :disabled="syncURLLoading || !form.panel_url"
          @click="syncPanelURLToNodes"
          title="向在线节点心跳下发 AGENT_PANEL_URL 并重启 agent"
        >
          {{ syncURLLoading ? '同步中…' : '同步 PANEL_URL 到节点' }}
        </button>
        <div
          v-if="brandFlash.msg"
          class="action-feedback"
          :class="brandFlash.kind"
          @click="brandFlash.clear()"
        >{{ brandFlash.msg }}</div>
      </div>
      <p class="help-text" style="margin:10px 0 0;line-height:1.55">
        向<strong>当前仍在心跳的节点</strong>下发本页地址（改 env 并重启 agent）。
        <strong>换机推荐</strong>：旧面板还在跑、节点仍 online 时，把地址改成<strong>新 URL</strong> → 保存 → 同步；节点连上新机后再停旧机/导入。
        若已迁完且节点失联，只能 SSH 改
        <code class="mono">/etc/mieru-agent.env</code>。
      </p>
      <div
        v-if="syncURLFlash.msg"
        class="action-feedback"
        :class="syncURLFlash.kind"
        style="margin-top:10px"
        @click="syncURLFlash.clear()"
      >{{ syncURLFlash.msg }}</div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>Cloudflare</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          新建/编辑节点时一键添加域名 A 记录 · API Token 需 Zone.DNS 编辑权限
        </div>
      </div>
      <span class="badge" :class="form.cf_configured ? 'ok' : 'warn'">
        {{ form.cf_configured ? '已配置' : '未配置' }}
      </span>
    </div>
    <div class="panel-bd" style="padding:16px">
      <div class="form-grid">
        <div class="field">
          <label>Zone ID</label>
          <input
            v-model="form.cf_zone_id"
            class="mono"
            placeholder="32 位 Zone ID（不是域名）"
          />
        </div>
        <div class="field">
          <label>API Token</label>
          <input
            v-model="form.cf_api_token"
            type="password"
            autocomplete="off"
            :placeholder="form.cf_token_set ? '已保存（留空不改，输入新 Token 覆盖）' : 'Bearer Token'"
          />
        </div>
      </div>
      <label class="muted" style="display:flex;align-items:center;gap:8px;margin:12px 0;font-size:13px">
        <input type="checkbox" v-model="form.cf_proxied_default" />
        默认开启橙云代理（入口自定义端口请勿开，建议仅 DNS）
      </label>
      <p class="help-text" style="margin:0 0 12px">
        Zone ID 在 Cloudflare 域名概览右侧（一串 32 位字符），不要填域名本身。
        Token：My Profile → API Tokens → 权限 <code class="mono">Zone.DNS Edit</code>。
        节点弹窗「CF 添加/更新解析」把公网 IP 写到接入域名。
      </p>
      <div class="row-actions">
        <button
          type="button"
          class="btn btn-primary"
          :disabled="savingCF"
          :aria-busy="savingCF ? 'true' : 'false'"
          @click="saveCFSettings"
        >
          {{ savingCF ? '保存中…' : '保存 CF 配置' }}
        </button>
        <button
          type="button"
          class="btn btn-ghost"
          :disabled="cfTesting"
          :aria-busy="cfTesting ? 'true' : 'false'"
          @click="testCF"
        >
          {{ cfTesting ? '测试中…' : '测试连接' }}
        </button>
        <button
          v-if="form.cf_token_set || (form.cf_api_token && form.cf_api_token !== '********')"
          class="btn btn-ghost"
          type="button"
          :disabled="clearingCF"
          :aria-busy="clearingCF ? 'true' : 'false'"
          @click="clearCFTokenAndSave"
        >
          {{ clearingCF ? '清除中…' : '清除 Token' }}
        </button>
      </div>
      <div
        v-if="cfFlash.msg"
        class="action-feedback"
        :class="cfFlash.kind"
        @click="cfFlash.clear()"
      >{{ cfFlash.msg }}</div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>备份导出</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          安全快照（无密钥）— 仅供查阅，不能用于换机
        </div>
      </div>
      <button type="button" class="btn btn-ghost btn-sm" :disabled="backupLoading" @click="downloadBackup">
        {{ backupLoading ? '导出中…' : '下载备份' }}
      </button>
    </div>
    <div class="panel-bd" style="padding:14px 16px">
      <div
        v-if="backupFlash.msg"
        class="action-feedback"
        :class="backupFlash.kind"
        style="margin-top:0;margin-bottom:10px"
        @click="backupFlash.clear()"
      >{{ backupFlash.msg }}</div>
      <p class="help-text" style="margin:0">
        不含 agent_token、管理员密码、用户代理密码。
        <strong>换服务器请用下方「完整迁移」</strong>。
      </p>
    </div>
  </div>

  <div class="panel" style="border-color: var(--danger)">
    <div class="panel-hd">
      <div>
        <h2>完整迁移（含密钥）</h2>
        <div class="muted" style="font-size:12px;margin-top:3px;color:var(--danger)">
          换面板服务器专用 · 导出/导入会触及全部密钥
        </div>
      </div>
      <button
        type="button"
        class="btn btn-danger btn-sm"
        :disabled="migExportLoading"
        @click="downloadMigration"
      >
        {{ migExportLoading ? '导出中…' : '导出完整迁移包' }}
      </button>
    </div>
    <div class="panel-bd" style="padding:14px 16px">
      <div
        v-if="migFlash.msg"
        class="action-feedback"
        :class="migFlash.kind"
        style="margin-top:0;margin-bottom:12px"
        @click="migFlash.clear()"
      >{{ migFlash.msg }}</div>

      <p class="help-text" style="margin:0 0 12px;line-height:1.6">
        <strong>换机步骤：</strong><br />
        ① 旧机点本卡片右上角红色「导出完整迁移包」并离线保存<br />
        ② 新机安装同版本面板并登录<br />
        ③ 在此选择文件 →「导入并覆盖」<br />
        ④ <strong>理想顺序</strong>：旧机仍在线时把公网地址改成新 URL → 保存 →「同步面板地址到节点」→ 再停旧机并在新机导入<br />
        ⑤ 若已导入新机、节点仍指旧地址：只能 SSH 改
        <code class="mono">/etc/mieru-agent.env</code>（token 不用改）
      </p>
      <p class="help-text" style="margin:0 0 12px;color:var(--danger);line-height:1.55">
        不要用上方「下载备份」的文件导入。正确文件名类似
        <code class="mono">mieru-panel-migration-full-….json</code>，
        打开后应有 <code class="mono">"format":"mieru-panel-migration"</code>。
      </p>

      <div class="field" style="margin-bottom:10px">
        <label>选择完整迁移包（.json，文件名含 migration-full）</label>
        <input type="file" accept=".json,application/json" @change="onMigrationFile" />
      </div>

      <div
        v-if="migPreview"
        class="mono"
        style="font-size:12px;line-height:1.7;padding:10px 12px;background:var(--bg-muted,#f8fafc);border-radius:10px;margin-bottom:12px"
      >
        <div v-if="migPreview.file_name">文件：{{ migPreview.file_name }}</div>
        <div>导出时间：{{ migPreview.exported_at }} · 源版本 {{ migPreview.panel_version }}</div>
        <div>
          节点 {{ migPreview.nodes }} · 隧道 {{ migPreview.routes }} · 用户 {{ migPreview.users }}
          · 公告 {{ migPreview.announcements }} · 流量行 {{ migPreview.traffic }}
          · 设置 {{ migPreview.settings }} · 管理员 {{ migPreview.admins }}
        </div>
      </div>

      <div class="action-bar">
        <button
          type="button"
          class="btn btn-danger"
          :disabled="migImportLoading || !migFile"
          @click="doImportMigration"
        >
          {{ migImportLoading ? '导入中…' : '导入并覆盖本机数据' }}
        </button>
      </div>
      <p class="help-text" style="margin:10px 0 0;color:var(--danger)">
        导入会清空本机现有数据并写入迁移包内容；管理员密码以迁移包为准，成功后需重新登录。
      </p>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>管理员</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">修改登录账号</div>
      </div>
    </div>
    <div class="panel-bd" style="padding:16px">
      <div class="form-grid">
        <div class="field">
          <label>用户名</label>
          <input v-model="pw.username" />
        </div>
        <div class="field">
          <label>当前密码</label>
          <input v-model="pw.current_password" type="password" autocomplete="current-password" />
        </div>
        <div class="field">
          <label>新密码</label>
          <input v-model="pw.new_password" type="password" autocomplete="new-password" />
        </div>
        <div class="field">
          <label>确认新密码</label>
          <input v-model="pw.new_password2" type="password" autocomplete="new-password" />
        </div>
      </div>
      <p class="help-text" style="margin:12px 0 14px">
        改密立即生效。勿开 <code class="mono">PANEL_ADMIN_FORCE_SYNC=1</code>，否则重启会用 env 覆盖。
      </p>
      <div class="action-bar">
        <button type="button" class="btn btn-primary" :disabled="savingPw" @click="changePassword">
          {{ savingPw ? '提交中…' : '修改密码' }}
        </button>
        <div
          v-if="pwFlash.msg"
          class="action-feedback"
          :class="pwFlash.kind"
          @click="pwFlash.clear()"
        >{{ pwFlash.msg }}</div>
      </div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>操作审计</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          最近 200 条 · 支持关键词 / 动作过滤
        </div>
      </div>
      <button class="btn btn-ghost btn-sm" @click="load">刷新</button>
    </div>
    <div class="panel-bd">
      <div class="panel-toolbar" style="padding:12px 16px 0;gap:8px;flex-wrap:wrap">
        <input class="input-filter" v-model="auditQ" placeholder="搜索 操作者/动作/对象/详情" />
        <input class="input-filter" v-model="auditAction" placeholder="动作含… 如 rebuild" style="min-width:140px" />
      </div>
      <table class="data" v-if="filteredAudit.length">
        <thead>
          <tr>
            <th>时间</th>
            <th>操作者</th>
            <th>动作</th>
            <th>对象</th>
            <th>详情</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in filteredAudit" :key="a.id">
            <td class="mono" style="font-size:12px;white-space:nowrap">{{ fmtTime(a.created_at) }}</td>
            <td>{{ a.actor }}</td>
            <td class="mono">{{ a.action }}</td>
            <td class="mono" style="font-size:12px">{{ a.target }}</td>
            <td class="muted" style="font-size:12px">{{ a.detail || '—' }}</td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty">{{ audit.length ? '无匹配记录' : '暂无审计记录' }}</div>
    </div>
  </div>
</template>
