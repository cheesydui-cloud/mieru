<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useFlash } from '../flash'
import { useRoute, useRouter } from 'vue-router'
import { api, copyText, statusBadge } from '../api'

const route = useRoute()
const router = useRouter()

const nodes = ref([])
const filter = ref('')
const tab = ref('all') // all | front | exit
const error = ref('')
const flash = useFlash()
const formFlash = useFlash()
const show = ref(false)
const mode = ref('create') // create | edit | created
const installShow = ref(false)
const installInfo = ref(null)
const created = ref(null)
const editingId = ref('')
const saving = ref(false)
const batchOpen = ref(false)
const moreId = ref(null)
const moreMenuStyle = ref({})
const moreNode = computed(() => (nodes.value || []).find((n) => n.id === moreId.value) || null)

const form = reactive({
  name: '',
  role: 'relay',
  region: '',
  tags: '',
  public_ip: '',
  private_ip: '',
  hostname: '',
  alt_hostnames: '',
  // 新建全部留白；落地用 listen_port，前置用 port_min–port_max
  listen_port: '',
  port_min: '',
  port_max: '',
})

// Cloudflare quick DNS from node modal
const cfConfigured = ref(false)
const cfBusy = ref(false)
const cfSyncBusy = ref(false)
const cfProxied = ref(false)
const cfMsg = ref('')

function isFrontRole(role) {
  return role === 'relay' || role === 'entry'
}

function blankForm(role) {
  const r = role || (tab.value === 'exit' ? 'exit' : 'relay')
  Object.assign(form, {
    name: '',
    role: r,
    region: '',
    tags: '',
    public_ip: '',
    private_ip: '',
    hostname: '',
    alt_hostnames: '',
    listen_port: '',
    port_min: '',
    port_max: '',
  })
}

function fillForm(n) {
  const role = n.role || 'relay'
  const front = isFrontRole(role)
  // 只回填真实数据；没有端口就留空，不伪造 10401/10001
  let pmin = n.port_min > 0 ? n.port_min : n.listen_port > 0 ? n.listen_port : ''
  let pmax = n.port_max > 0 ? n.port_max : pmin || ''
  // 前置若历史数据是单端口，编辑时展开为常用池（与后端 EffectivePortRange 一致）
  if (front && pmin && pmax && Number(pmax) <= Number(pmin)) {
    pmax = Math.min(65535, Number(pmin) + 98)
  }
  Object.assign(form, {
    name: n.name || '',
    role,
    region: n.region || '',
    tags: n.tags || '',
    public_ip: n.public_ip || '',
    private_ip: n.private_ip || '',
    hostname: n.hostname || '',
    alt_hostnames: n.alt_hostnames || '',
    listen_port: pmin === '' ? '' : pmin,
    port_min: pmin === '' ? '' : pmin,
    port_max: pmax === '' ? '' : pmax,
  })
}

function portLabel(n) {
  const a = n.port_min > 0 ? n.port_min : n.listen_port > 0 ? n.listen_port : 0
  const b = n.port_max > 0 ? n.port_max : a
  if (!a) return '—'
  if (isFront(n) && b > a) return `${a}–${b}`
  return String(a)
}

function statusLabel(s) {
  if (s === 'online') return '在线'
  if (s === 'degraded') return '异常'
  if (s === 'offline') return '离线'
  return s || '离线'
}

function roleLabel(role) {
  const m = {
    relay: '前置',
    entry: '前置',
    exit: '落地',
    hybrid: '混合',
  }
  return m[role] || role
}

function heartbeatLabel(n) {
  if (n.no_heartbeat || n.heartbeat_age_sec < 0 || n.heartbeat_age_sec == null) {
    if (!n.last_seen) return '未心跳'
    return '心跳超时'
  }
  const a = n.heartbeat_age_sec
  if (a < 60) return `${a}s 前`
  if (a < 3600) return `${Math.floor(a / 60)}m 前`
  return `${Math.floor(a / 3600)}h 前`
}

function meteringLabel(n) {
  if (!(n.role === 'exit' || n.role === 'hybrid')) return ''
  if (n.traffic_reporting) {
    const a = n.traffic_report_age_sec
    if (a != null && a >= 0) return `计量开 · ${a}s 前`
    return '计量开'
  }
  return n.metering_hint || '计量未上报'
}

function isFront(n) {
  return n.role === 'relay' || n.role === 'entry'
}
function isExit(n) {
  return n.role === 'exit' || n.role === 'hybrid'
}

const tabNodes = computed(() => {
  let list = nodes.value || []
  if (tab.value === 'front') list = list.filter(isFront)
  else if (tab.value === 'exit') list = list.filter(isExit)
  const q = (filter.value || '').trim().toLowerCase()
  if (!q) return list
  return list.filter((n) => {
    return (
      (n.name || '').toLowerCase().includes(q) ||
      (n.id || '').toLowerCase().includes(q) ||
      (n.hostname || '').toLowerCase().includes(q) ||
      (n.public_ip || '').toLowerCase().includes(q) ||
      (n.role || '').toLowerCase().includes(q)
    )
  })
})

const counts = computed(() => {
  const all = nodes.value || []
  return {
    all: all.length,
    front: all.filter(isFront).length,
    exit: all.filter(isExit).length,
  }
})

function setTab(t) {
  tab.value = t
  router.replace({ query: t === 'all' ? {} : { tab: t } })
}

async function load() {
  try {
    const ns = await api('/api/admin/nodes')
    nodes.value = Array.isArray(ns) ? ns : []
    error.value = ''
  } catch (e) {
    error.value = e.message
    nodes.value = []
  }
}

async function loadCFStatus() {
  try {
    const s = await api('/api/admin/settings')
    cfConfigured.value = !!s.cf_configured
    cfProxied.value = !!s.cf_proxied_default
  } catch {
    cfConfigured.value = false
  }
}

/** Cloudflare: create/update A record hostname → public_ip, then keep form.hostname */
async function cfAddDomain() {
  cfMsg.value = ''
  const name = (form.hostname || '').trim()
  const ip = (form.public_ip || '').trim()
  if (!name) {
    error.value = '请先填写接入域名'
    return
  }
  if (!ip) {
    error.value = '请先填写公网 IP（CF A 记录指向）'
    return
  }
  if (!cfConfigured.value) {
    error.value = '请先在「设置」配置 Cloudflare API Token 与 Zone ID'
    return
  }
  cfBusy.value = true
  try {
    const res = await api('/api/admin/cloudflare/dns', {
      method: 'POST',
      body: JSON.stringify({ name, ip, proxied: !!cfProxied.value }),
    })
    const host = res.name || name
    form.hostname = host
    cfMsg.value = `CF 已写入 ${res.type || 'A'} ${host} → ${res.content || ip}${
      res.proxied ? '（橙云代理）' : '（仅 DNS）'
    }`
    if (res.note) cfMsg.value += ' · ' + res.note
    if (mode.value === 'edit' && editingId.value && host) {
      try {
        await api(`/api/admin/nodes/${editingId.value}`, {
          method: 'PUT',
          body: JSON.stringify(payload()),
        })
        await load()
        formFlash.ok(`CF 已写入并保存接入域名 ${host}`)
      } catch (e) {
        formFlash.err(`CF 已写入，但节点保存失败：${e.message}`)
      }
    } else {
      formFlash.ok('Cloudflare 域名已添加（保存节点后列表可见）')
    }
  } catch (e) {
    error.value = e.message
  } finally {
    cfBusy.value = false
  }
}


/** Suggest subdomain from node name, e.g. NB.JP + zone → nb-jp.example.com */
function suggestHostname(zoneName) {
  const zone = String(zoneName || '').replace(/\.$/, '').toLowerCase()
  let base = String(form.name || form.region || 'node')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  if (!base) base = 'node'
  if (base.length > 40) base = base.slice(0, 40)
  return zone ? `${base}.${zone}` : base
}

/**
 * Pull existing Cloudflare A/AAAA for this public IP into 接入域名.
 * Prefer exact match already in form; else first record; else offer first.
 */
async function cfSyncFromCF() {
  const ip = (form.public_ip || '').trim()
  if (!ip) {
    error.value = '请先填写公网 IP'
    formFlash.err('请先填写公网 IP')
    return
  }
  if (!cfConfigured.value) {
    error.value = '请先在「设置」配置 Cloudflare'
    formFlash.err('请先在「设置」配置 Cloudflare')
    return
  }
  cfSyncBusy.value = true
  cfMsg.value = ''
  error.value = ''
  try {
    const res = await api('/api/admin/cloudflare/lookup?ip=' + encodeURIComponent(ip))
    const names = Array.isArray(res.names) ? res.names.filter(Boolean) : []
    if (!names.length) {
      formFlash.err('CF 中未找到指向该 IP 的 A/AAAA 记录')
      cfMsg.value = '未找到匹配记录。可先在上方填写域名再点「CF 添加 / 更新解析」。'
      return
    }
    const cur = (form.hostname || '').trim().toLowerCase()
    let pick = names[0]
    if (cur && names.some((n) => n.toLowerCase() === cur)) {
      pick = names.find((n) => n.toLowerCase() === cur) || pick
    }
    const prev = (form.hostname || '').trim()
    form.hostname = pick
    const extra = names.length > 1 ? `（共 ${names.length} 条：${names.join('、')}）` : ''
    // 编辑已有节点时直接落库，列表「公网/接入」立刻显示域名
    if (mode.value === 'edit' && editingId.value && pick && pick !== prev) {
      try {
        await api(`/api/admin/nodes/${editingId.value}`, {
          method: 'PUT',
          body: JSON.stringify(payload()),
        })
        await load()
        cfMsg.value = `已从 CF 同步并保存接入域名：${pick}${extra}`
        formFlash.ok(names.length > 1 ? `已保存 ${pick}（另有 ${names.length - 1} 条）` : `已保存接入域名 ${pick}`)
      } catch (e) {
        cfMsg.value = `已填入 ${pick}，但保存失败：${e.message}`
        formFlash.err(`域名已填入，保存失败：${e.message}`)
      }
    } else {
      cfMsg.value = `已从 CF 同步接入域名：${pick}${extra}`
      formFlash.ok(
        names.length > 1
          ? `已同步 ${pick}（另有 ${names.length - 1} 条可选，保存后列表可见）`
          : mode.value === 'edit'
            ? `接入域名已是 ${pick}`
            : `已同步 ${pick}（创建后保存生效）`,
      )
    }
  } catch (e) {
    error.value = e.message
    formFlash.err(e.message)
  } finally {
    cfSyncBusy.value = false
  }
}

function openCreate() {
  blankForm()
  created.value = null
  editingId.value = ''
  mode.value = 'create'
  cfMsg.value = ''
  show.value = true
  loadCFStatus()
}

function openEdit(n) {
  fillForm(n)
  created.value = null
  editingId.value = n.id
  mode.value = 'edit'
  cfMsg.value = ''
  error.value = ''
  formFlash.clear()
  show.value = true
  // 已有公网 IP、接入域名为空时，自动从 CF 按 IP 拉域名填入
  loadCFStatus().then(() => {
    if (!(form.hostname || '').trim() && (form.public_ip || '').trim() && cfConfigured.value) {
      return cfSyncFromCF()
    }
  })
}

function payload() {
  const front = isFrontRole(form.role)
  let pmin = Number(form.port_min) || 0
  let pmax = Number(form.port_max) || 0
  if (!front) {
    // 落地 / hybrid：单端口
    const port = Number(form.listen_port) || pmin || 0
    pmin = port
    pmax = port
  } else {
    if (!pmin) pmin = Number(form.listen_port) || 0
    if (!pmax) pmax = pmin
    if (pmax && pmin && pmax < pmin) {
      const t = pmin
      pmin = pmax
      pmax = t
    }
  }
  return {
    name: form.name,
    role: form.role,
    region: form.region,
    tags: form.tags,
    public_ip: form.public_ip,
    private_ip: form.private_ip,
    hostname: form.hostname,
    alt_hostnames: form.alt_hostnames,
    port_min: pmin,
    port_max: pmax,
    listen_port: pmin,
  }
}

function validatePorts() {
  if (!form.name.trim()) {
    error.value = '请填写名称'
    return false
  }
  if (isFrontRole(form.role)) {
    const a = Number(form.port_min) || 0
    const b = Number(form.port_max) || 0
    if (!a || a < 1 || a > 65535 || !b || b < 1 || b > 65535) {
      error.value = '请填写有效端口起止 (1–65535)'
      return false
    }
    if (b < a) {
      error.value = '端口止不能小于端口起'
      return false
    }
    if (b - a > 200) {
      error.value = '端口池过大（最多 200 个），请缩小范围'
      return false
    }
  } else {
    const port = Number(form.listen_port) || Number(form.port_min) || 0
    if (!port || port < 1 || port > 65535) {
      error.value = '请填写有效端口 (1–65535)'
      return false
    }
  }
  return true
}

async function create() {
  if (!validatePorts()) return
  saving.value = true
  try {
    const res = await api('/api/admin/nodes', {
      method: 'POST',
      body: JSON.stringify(payload()),
    })
    created.value = res
    mode.value = 'created'
    formFlash.ok(`已创建：${res.node.name}`)
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function saveEdit() {
  if (!editingId.value) return
  if (!validatePorts()) return
  saving.value = true
  try {
    await api(`/api/admin/nodes/${editingId.value}`, {
      method: 'PUT',
      body: JSON.stringify(payload()),
    })
    formFlash.ok('已更新，配置已自动下发（Agent 心跳后生效）')
    await load()
    setTimeout(() => { show.value = false }, 900)
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

// 切换类型时只清空另一套端口字段，不自动填默认值
watch(
  () => form.role,
  (r, prev) => {
    if (!show.value || mode.value === 'created' || mode.value === 'edit') return
    if (r === prev) return
    if (isFrontRole(r) && !isFrontRole(prev)) {
      // 落地 → 前置：把单端口带到起，止仍留白
      if (form.listen_port && !form.port_min) form.port_min = form.listen_port
      form.listen_port = form.port_min || ''
    } else if (!isFrontRole(r) && isFrontRole(prev)) {
      // 前置 → 落地：用端口起作单端口
      form.listen_port = form.port_min || form.listen_port || ''
      form.port_max = form.listen_port || ''
    }
  },
)

async function showInstall(id) {
  try {
    installInfo.value = await api(`/api/admin/nodes/${id}/install`)
    installShow.value = true
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

const upgrading = ref({}) // id -> true while request in flight

function needsUpgrade(n) {
  const cur = (n.agent_version || '').replace(/^v/, '')
  const want = (n.panel_version || '').replace(/^v/, '')
  if (!cur || !want) return !!n.agent_version // show if we have agent but no panel ver compare
  return cur !== want
}

function upgradeLabel(n) {
  const st = n.upgrade_status || ''
  if (st === 'pending' || n.upgrade_pending) return '排队中…'
  if (st === 'running') return '升级中…'
  if (upgrading.value[n.id]) return '推送中…'
  if (st === 'error') return '重试升级'
  if (needsUpgrade(n)) return '升级'
  return '升级'
}

function upgradeRowHint(n) {
  // Short row meta only — long text goes to title tooltip / install modal.
  const st = n.upgrade_status || ''
  if (st === 'error' && n.upgrade_error) return '升级失败'
  if (st === 'pending' || n.upgrade_pending) return '升级排队'
  if (st === 'running') return '升级中'
  const pu = n.panel_url_status || ''
  if (pu === 'error') return '地址同步失败'
  if (pu === 'pending' || n.panel_url_pending) return '同步地址中'
  if (n.panel_url_mismatch) return '面板地址不一致'
  if (n.apply_error) return '配置应用失败'
  if (n.config_stale) return '配置未生效'
  return ''
}

function rowHintTone(n) {
  const h = upgradeRowHint(n)
  if (!h) return ''
  if (h.includes('失败') || h.includes('不一致')) return 'err'
  return 'warn'
}

function closeMore() {
  moreId.value = null
  moreMenuStyle.value = {}
}

function toggleMore(n, e) {
  e?.stopPropagation?.()
  if (moreId.value === n.id) {
    closeMore()
    return
  }
  moreId.value = n.id
  batchOpen.value = false
  const el = e?.currentTarget
  if (!el?.getBoundingClientRect) {
    moreMenuStyle.value = { position: 'fixed', right: '16px', top: '80px', zIndex: 1200 }
    return
  }
  const rect = el.getBoundingClientRect()
  const menuH = 220
  const menuW = 148
  const pad = 8
  const openUp = rect.bottom + menuH + pad > window.innerHeight && rect.top > menuH + pad
  let left = rect.right - menuW
  if (left < pad) left = pad
  if (left + menuW > window.innerWidth - pad) left = window.innerWidth - menuW - pad
  const style = {
    position: 'fixed',
    left: `${Math.round(left)}px`,
    zIndex: 1200,
    minWidth: `${menuW}px`,
  }
  if (openUp) {
    style.top = 'auto'
    style.bottom = `${Math.round(window.innerHeight - rect.top + 4)}px`
  } else {
    style.top = `${Math.round(rect.bottom + 4)}px`
    style.bottom = 'auto'
  }
  moreMenuStyle.value = style
}

function onDocPointerDown(e) {
  const t = e.target
  if (moreId.value != null) {
    if (!t?.closest?.('.more-menu-float') && !t?.closest?.('.more-trigger')) closeMore()
  }
  if (batchOpen.value) {
    if (!t?.closest?.('.dropdown')) batchOpen.value = false
  }
}

function onWinReposition() {
  if (moreId.value != null) closeMore()
  batchOpen.value = false
}

function upgradeBusy(n) {
  return (
    !!upgrading.value[n.id] ||
    n.upgrade_status === 'pending' ||
    n.upgrade_status === 'running' ||
    !!n.upgrade_pending
  )
}

function agentSupportsRemoteUpgrade(n) {
  // Remote push needs agent that understands upgrade_job (shipped in v0.4.6+).
  const v = (n.agent_version || '').replace(/^v/, '')
  if (!v) return false
  const parts = v.split('.').map((x) => parseInt(x, 10) || 0)
  const [maj = 0, min = 0, patch = 0] = parts
  if (maj > 0) return true
  if (min > 4) return true
  if (min === 4 && patch >= 6) return true
  return false
}

async function pushUpgrade(n) {
  if (!n?.id || upgradeBusy(n)) return
  if (!agentSupportsRemoteUpgrade(n)) {
    const ok = confirm(
      `${n.name} 当前 Agent 为 v${n.agent_version || '?'}，还不支持远程推送升级。\n\n` +
        `需要先在该机器执行一次「安装」命令升到 v0.4.6+，之后即可点升级。\n\n` +
        `仍要排队推送吗？（旧 Agent 会忽略，无效果）`,
    )
    if (!ok) {
      // open install cmd for convenience
      await showInstall(n.id)
      return
    }
  }
  upgrading.value = { ...upgrading.value, [n.id]: true }
  try {
    const res = await api(`/api/admin/nodes/${n.id}/upgrade`, { method: 'POST' })
    flash.ok(res.message || `已推送升级 → ${res.version || ''}`)
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    const next = { ...upgrading.value }
    delete next[n.id]
    upgrading.value = next
  }
}


async function syncPanelURL(n) {
  if (!n?.id) return
  if (!confirm(`向节点「${n.name || n.id}」推送当前面板 PANEL_URL？`)) return
  try {
    const res = await api(`/api/admin/nodes/${n.id}/sync-panel-url`, { method: 'POST' })
    flash.ok(res.message || '已排队同步 PANEL_URL')
    await load()
  } catch (e) {
    error.value = e.message
  }
}

async function syncPanelURLAll() {
  if (!confirm('向所有在线节点推送当前设置中的面板地址？\n节点须仍能连上当前面板一次；离线节点请用「复制修复命令」。')) return
  try {
    const res = await api('/api/admin/nodes/sync-panel-url', { method: 'POST' })
    flash.ok(res.message || '已推送')
    await load()
  } catch (e) {
    error.value = e.message
  }
}

async function copyPanelURLFix(n) {
  const cmd = (n && n.panel_url_fix_cmd) || ''
  if (!cmd) {
    flash.err('无修复命令：请先在设置保存「面板公网地址」，并确认节点有 token')
    return
  }
  try {
    await copyText(cmd)
    flash.ok(`已复制「${n.name || n.id}」修复命令，SSH 到该机粘贴执行即可`)
  } catch (e) {
    flash.err(e.message || '复制失败')
  }
}

async function pushUpgradeAll() {
  if (!confirm('向所有在线节点推送 Agent 升级到面板版本？')) return
  try {
    const res = await api('/api/admin/nodes/upgrade-all', { method: 'POST' })
    flash.ok(res.message || '已推送')
    await load()
  } catch (e) {
    error.value = e.message
  }
}

async function remove(n) {
  const id = typeof n === 'string' ? n : n.id
  const name = typeof n === 'object' && n ? n.name || id : id
  if (
    !confirm(
      `确认删除节点「${name}」？\n\n` +
        `• 经过该节点的隧道会一并删除\n` +
        `• 绑定这些隧道的用户会解绑\n` +
        `• 在线 Agent 下次心跳后停用服务（释放端口）\n` +
        `此操作不可恢复。`,
    )
  ) {
    return
  }
  try {
    const res = await api(`/api/admin/nodes/${id}`, { method: 'DELETE' })
    const parts = ['已删除']
    if (res.routes_deleted) parts.push(`隧道 ${res.routes_deleted}`)
    if (res.users_unbound) parts.push(`解绑用户 ${res.users_unbound}`)
    flash.ok(parts.join(' · ') + '；Agent 将停用')
    await load()
  } catch (e) {
    error.value = e.message
  }
}

async function rebuild() {
  await api('/api/admin/rebuild', { method: 'POST' })
  flash.ok('已重建全部节点配置')
  await load()
}

async function copy(text) {
  try {
    await copyText(text)
    flash.ok('已复制')
  } catch {
    flash.err('复制失败，请手动选中')
  }
}

watch(
  () => route.query.tab,
  (t) => {
    if (t === 'front' || t === 'exit' || t === 'all') tab.value = t
    else if (t === 'relay' || t === 'entry') tab.value = 'front'
    else if (!t) {
      /* keep */
    }
  },
  { immediate: true },
)

let refreshTimer
onMounted(() => {
  const t = route.query.tab
  if (t === 'front' || t === 'exit') tab.value = t
  load()
  refreshTimer = setInterval(load, 5000)
  document.addEventListener('pointerdown', onDocPointerDown, true)
  window.addEventListener('resize', onWinReposition)
  window.addEventListener('scroll', onWinReposition, true)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  document.removeEventListener('pointerdown', onDocPointerDown, true)
  window.removeEventListener('resize', onWinReposition)
  window.removeEventListener('scroll', onWinReposition, true)
})
</script>

<template>
  <div v-if="error && !show" class="action-feedback err page-action-feedback" @click="error = ''">{{ error }}</div>
  <div
    v-if="flash.msg && !show"
    class="action-feedback page-action-feedback"
    :class="flash.kind"
    @click="flash.clear()"
  >{{ flash.msg }}</div>

  <Teleport to="body">
    <div
      v-if="moreId != null && moreNode"
      class="more-menu more-menu-float"
      :style="moreMenuStyle"
      @click.stop
    >
      <button type="button" @click="openEdit(moreNode); closeMore()">编辑</button>
      <button type="button" @click="showInstall(moreNode.id); closeMore()">安装 Agent</button>
      <button
        type="button"
        :disabled="upgradeBusy(moreNode) || moreNode.status === 'offline'"
        @click="pushUpgrade(moreNode); closeMore()"
      >
        {{ upgradeLabel(moreNode) }}
      </button>
      <button
        type="button"
        :disabled="moreNode.status === 'offline' || moreNode.panel_url_pending"
        @click="syncPanelURL(moreNode); closeMore()"
      >
        {{ moreNode.panel_url_pending ? '同步中…' : '同步地址' }}
      </button>
      <button
        v-if="moreNode.status === 'offline'"
        type="button"
        :disabled="!moreNode.panel_url_fix_cmd"
        @click="copyPanelURLFix(moreNode); closeMore()"
      >
        复制修复命令
      </button>
      <button type="button" class="danger" @click="remove(moreNode); closeMore()">删除</button>
    </div>
  </Teleport>

  <div class="page-tabs">
    <button class="page-tab" :class="{ active: tab === 'all' }" type="button" @click="setTab('all')">
      全部 ({{ counts.all }})
    </button>
    <button class="page-tab" :class="{ active: tab === 'front' }" type="button" @click="setTab('front')">
      前置 ({{ counts.front }})
    </button>
    <button class="page-tab" :class="{ active: tab === 'exit' }" type="button" @click="setTab('exit')">
      落地 ({{ counts.exit }})
    </button>
  </div>

  <div class="panel-toolbar">
    <input class="input-filter" v-model="filter" placeholder="搜索名称 / IP / 域名" />
    <div class="row-actions">
      <div class="dropdown">
        <button class="btn btn-ghost btn-sm" type="button" @click="batchOpen = !batchOpen">
          批量 ▾
        </button>
        <div v-if="batchOpen" class="dropdown-menu" @click.stop>
          <button type="button" @click="batchOpen = false; pushUpgradeAll()">全部升级 Agent</button>
          <button type="button" @click="batchOpen = false; syncPanelURLAll()">同步面板地址</button>
          <button type="button" @click="batchOpen = false; rebuild()">重建配置</button>
        </div>
      </div>
      <button class="btn btn-primary btn-sm" @click="openCreate">新增节点</button>
    </div>
  </div>

  <div class="table-wrap">
    <table class="data" v-if="tabNodes.length">
      <thead>
        <tr>
          <th>名称</th>
          <th>角色</th>
          <th>状态</th>
          <th>接入</th>
          <th>端口</th>
          <th>区域</th>
          <th>Agent</th>
          <th style="width:72px"></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="n in tabNodes" :key="n.id" :class="{ 'row-warn': n.no_heartbeat || n.config_stale }">
          <td>
            <div class="name-link">{{ n.name }}</div>
            <div
              v-if="upgradeRowHint(n)"
              class="row-meta"
              :class="rowHintTone(n)"
              :title="n.apply_error || n.upgrade_error || n.panel_url_error || upgradeRowHint(n)"
            >
              {{ upgradeRowHint(n) }}
            </div>
          </td>
          <td>
            <span class="badge">{{ roleLabel(n.role) }}</span>
          </td>
          <td>
            <span class="badge" :class="statusBadge(n.status)">
              <span class="dot"></span>{{ statusLabel(n.status) }}
            </span>
            <div
              class="row-meta"
              :class="n.no_heartbeat ? 'err' : ''"
              :title="heartbeatLabel(n)"
            >
              {{ heartbeatLabel(n) }}
            </div>
          </td>
          <td class="mono" style="font-size:12px">
            <div>{{ n.hostname || n.public_ip || '—' }}</div>
            <div v-if="n.hostname && n.public_ip" class="row-meta">{{ n.public_ip }}</div>
          </td>
          <td class="mono">{{ portLabel(n) }}</td>
          <td>{{ n.region || '—' }}</td>
          <td class="mono" style="font-size:12px">
            <div>
              {{ n.agent_version ? 'v' + n.agent_version : '—' }}
              <span
                v-if="needsUpgrade(n) && n.agent_version"
                class="badge warn"
                style="margin-left:4px;font-size:10px"
                title="面板版本更新"
              >可升</span>
            </div>
          </td>
          <td>
            <div class="row-actions">
              <button class="btn btn-link btn-sm" @click="openEdit(n)">编辑</button>
              <button
                type="button"
                class="btn btn-ghost btn-sm btn-icon more-trigger"
                title="更多"
                @click="toggleMore(n, $event)"
              >⋯</button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty">
      <div style="margin-bottom:12px">{{ nodes.length ? '无匹配节点' : '还没有节点' }}</div>
      <button v-if="!nodes.length" class="btn btn-primary" @click="openCreate">新增节点</button>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal" style="width:min(600px,100%)">
      <div class="modal-hd">
        <h3>
          <template v-if="mode === 'created'">节点已创建</template>
          <template v-else-if="mode === 'edit'">编辑节点</template>
          <template v-else>新建节点</template>
        </h3>
        <button class="btn btn-ghost btn-sm" @click="show = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div v-if="error && show" class="action-feedback err" style="margin:0" @click="error = ''">{{ error }}</div>
        <div
          v-if="formFlash.msg && show"
          class="action-feedback"
          :class="formFlash.kind"
          style="margin:0"
          @click="formFlash.clear()"
        >{{ formFlash.msg }}</div>
        <template v-if="mode !== 'created'">
          <div class="form-grid">
            <div class="field">
              <label>名称</label>
              <input v-model="form.name" />
            </div>
            <div class="field">
              <label>类型</label>
              <select v-model="form.role">
                <option value="relay">前置 — 商家入口，转发到落地</option>
                <option value="exit">落地 — 家宽 mita 出口</option>
              </select>
              <details class="adv-block" style="margin-top:8px">
                <summary>高级角色</summary>
                <select v-model="form.role" style="margin-top:8px;width:100%">
                  <option value="relay">前置 relay</option>
                  <option value="exit">落地 exit</option>
                  <option value="entry">前置 entry（同 relay）</option>
                  <option value="hybrid">混合 hybrid（单机自用）</option>
                </select>
              </details>
            </div>
            <div class="field">
              <label>公网 IP</label>
              <input v-model="form.public_ip" />
            </div>
            <template v-if="isFrontRole(form.role)">
              <div class="field">
                <label>端口起</label>
                <input
                  v-model.number="form.port_min"
                  type="number"
                  min="1"
                  max="65535"
                  placeholder="如 10401"
                />
              </div>
              <div class="field">
                <label>端口止</label>
                <input
                  v-model.number="form.port_max"
                  type="number"
                  min="1"
                  max="65535"
                  placeholder="如 10499"
                />
              </div>
            </template>
            <div v-else class="field">
              <label>公开端口（mita）</label>
              <input
                v-model.number="form.listen_port"
                type="number"
                min="1"
                max="65535"
                placeholder="如 10001"
              />
            </div>
            <div class="field">
              <label>内网 IP（可选）</label>
              <input v-model="form.private_ip" placeholder="可选" />
            </div>
            <div class="field">
              <label>接入域名（可选）</label>
              <input v-model="form.hostname" placeholder="如 node.example.com" />
            </div>
            <div class="field">
              <label>区域</label>
              <input v-model="form.region" placeholder="如 cn / us" />
            </div>
            <div class="field">
              <label>标签</label>
              <input v-model="form.tags" />
            </div>
          </div>

          <div
            class="field"
            style="margin-top:12px;padding:12px;border:1px dashed var(--border-line);border-radius:8px;background:var(--bg-elevated)"
          >
            <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap">
              <div>
                <strong style="font-size:13px">Cloudflare 一键加域名</strong>
                <div class="muted" style="font-size:12px;margin-top:2px">
                  用公网 IP 写 A/AAAA 到接入域名
                  <span v-if="!cfConfigured" style="color:var(--warning)"> · 未配置 Token，请先去设置</span>
                  <span v-else style="color:var(--success)"> · 已配置</span>
                </div>
              </div>
              <label class="muted" style="font-size:12px;display:flex;align-items:center;gap:6px">
                <input type="checkbox" v-model="cfProxied" />
                橙云代理（入口自定义端口请勿勾）
              </label>
            </div>
            <div class="row-actions" style="margin-top:10px;flex-wrap:wrap">
              <button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="cfSyncBusy || !form.public_ip || !cfConfigured"
                @click="cfSyncFromCF"
              >
                {{ cfSyncBusy ? '同步中…' : '从 CF 同步域名' }}
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-sm"
                :disabled="cfBusy || !form.hostname || !form.public_ip || !cfConfigured"
                @click="cfAddDomain"
              >
                {{ cfBusy ? '写入 CF…' : 'CF 添加 / 更新解析' }}
              </button>
              <button
                v-if="!cfConfigured"
                type="button"
                class="btn btn-ghost btn-sm"
                @click="router.push('/settings')"
              >
                去设置 CF
              </button>
            </div>
            <p v-if="cfMsg" class="help-text" style="margin-top:8px;color:var(--success)">{{ cfMsg }}</p>
            <p class="help-text" style="margin-top:6px">
              <strong>从 CF 同步域名</strong>：按公网 IP 在 Cloudflare 查已有 A/AAAA，填入上方「接入域名」。
              <strong>CF 添加 / 更新解析</strong>：把当前接入域名写到 CF（指向公网 IP）。
              客户端优先用域名。建议<strong>仅 DNS</strong>（灰云）。
            </p>
          </div>

          <p class="help-text">
            <template v-if="isFrontRole(form.role)">
              前置填<strong>端口池</strong>（如 10401–10499）：每条隧道自动占一个端口转发到对应落地；
              商家 DNAT 需放行整段。不会一次打开 99 个监听，只开「有隧道的」端口。
            </template>
            <template v-else>
              落地端口 = mita 监听（如 10001 / 10002）。
            </template>
            改端口后会自动重建并下发；异常时再点「重建配置」强制重推。
            <span v-if="mode === 'edit'" class="mono"> · {{ editingId }}</span>
          </p>
        </template>
        <template v-else>
          <div class="kv">
            <dt>Node ID</dt>
            <dd class="mono">{{ created.node.id }}</dd>
            <dt>Token</dt>
            <dd class="mono" style="word-break:break-all">{{ created.agent_token }}</dd>
            <dt>面板</dt>
            <dd class="mono">{{ created.panel_url }}</dd>
            <dt>端口</dt>
            <dd class="mono">
              <template v-if="created.node.port_max > created.node.port_min">
                {{ created.node.port_min }}–{{ created.node.port_max }}
              </template>
              <template v-else>
                {{ created.node.listen_port || created.node.port_min }}
              </template>
            </dd>
          </div>
          <div class="field" style="margin-top:4px">
            <label>一键安装 Agent（目标 Linux）</label>
            <textarea readonly rows="3" class="code-block" :value="created.install_cmd" />
          </div>
          <div class="row-actions">
            <button class="btn btn-primary btn-sm" @click="copy(created.install_cmd)">复制安装命令</button>
            <button class="btn btn-ghost btn-sm" @click="copy(created.agent_token)">复制 Token</button>
          </div>
        </template>
      </div>
      <div class="modal-ft">
        <div
          v-if="formFlash.msg"
          class="action-feedback"
          :class="formFlash.kind"
          @click="formFlash.clear()"
        >{{ formFlash.msg }}</div>
        <button class="btn btn-ghost" @click="show = false">
          {{ mode === 'created' ? '完成' : '取消' }}
        </button>
        <button v-if="mode === 'create'" class="btn btn-primary" :disabled="saving" @click="create">
          {{ saving ? '创建中…' : '创建' }}
        </button>
        <button v-else-if="mode === 'edit'" class="btn btn-primary" :disabled="saving" @click="saveEdit">
          {{ saving ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>
  </div>

  <div v-if="installShow && installInfo" class="modal-mask" @click.self="installShow = false">
    <div class="modal" style="width:min(600px,100%)">
      <div class="modal-hd">
        <h3>Agent 安装</h3>
        <button class="btn btn-ghost btn-sm" @click="installShow = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="kv">
          <dt>Node ID</dt>
          <dd class="mono">{{ installInfo.node_id }}</dd>
          <dt>角色</dt>
          <dd><span class="badge">{{ installInfo.role }}</span></dd>
          <dt>Token</dt>
          <dd class="mono" style="word-break:break-all">{{ installInfo.agent_token }}</dd>
          <dt>面板</dt>
          <dd class="mono">{{ installInfo.panel_url }}</dd>
        </div>
        <div class="field">
          <label>安装 Agent（整行复制到目标机执行）</label>
          <textarea readonly rows="3" class="code-block" :value="installInfo.install_cmd" />
          <p class="help-text" style="margin-top:8px">
            {{ installInfo.hint || '在目标 Linux 上执行安装命令；装完回来看节点是否在线。' }}
          </p>
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="installShow = false">关闭</button>
        <button class="btn btn-primary" @click="copy(installInfo.install_cmd)">复制安装命令</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.row-warn td {
  background: rgba(245, 158, 11, 0.06);
}
</style>
