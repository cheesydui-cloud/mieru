<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useFlash } from '../flash'
import QRCode from 'qrcode'
import { api, copyText, formatBytes, formatBps, getToken, statusBadge } from '../api'

const users = ref([])
const routes = ref([])
const rates = ref({}) // id -> {up, down, ts}
const error = ref('')
const flash = useFlash()
const formFlash = useFlash()
const filter = ref('')
const statusFilter = ref('all')
const show = ref(false)
const mode = ref('create') // create | edit | created
const editingId = ref(null)
const created = ref(null)
const saving = ref(false)
const moreId = ref(null)
const moreMenuStyle = ref({})
const moreUser = computed(() => (users.value || []).find((u) => u.id === moreId.value) || null)

const form = reactive({
  username: '',
  expire_at: '',
  traffic_limit_gb: 0, // 0=不限；套餐按钮可一键填
  route_id: null,
  entry_host: '',
  entry_port: null,
  note: '',
  status: 'active',
})

const packages = [
  { name: '体验', days: 1, gb: 5 },
  { name: '月卡', days: 30, gb: 100 },
  { name: '不限', days: 30, gb: 0 },
]

const subShow = ref(false)
const subUser = ref(null)
const shareURL = ref('')
const shareURLs = ref('')
const subQR = ref('')
const subLoading = ref(false)
const entries = ref([])
const mihomoYAML = ref('')

const renewShow = ref(false)
const renewUser = ref(null)
const renewDays = ref(30)
const renewDate = ref('')
const trafficShow = ref(false)
const trafficUser = ref(null)
const trafficGB = ref(50)
const multShow = ref(false)
const multUser = ref(null)
const multValue = ref(1)
const speedShow = ref(false)
const speedUser = ref(null)
const speedMbps = ref(0) // 0 = unlimited
const resetShow = ref(false)
const resetInfo = ref(null) // { username, proxy_password }
const selected = ref({}) // id -> true
const batchBusy = ref(false)

let listTimer
let rateTimer

const selectedIds = computed(() =>
  Object.keys(selected.value)
    .filter((k) => selected.value[k])
    .map((k) => Number(k))
    .filter((n) => Number.isFinite(n) && n > 0),
)
const selectedCount = computed(() => selectedIds.value.length)

function isSelected(id) {
  return !!selected.value[id]
}

function toggleSelect(id) {
  selected.value = { ...selected.value, [id]: !selected.value[id] }
}

function clearSelection() {
  selected.value = {}
}

function selectVisible() {
  const next = { ...selected.value }
  for (const g of groupedUsers.value || []) {
    for (const u of g.users || []) {
      next[u.id] = true
    }
  }
  selected.value = next
}

async function batchAction(action, extra = {}) {
  const ids = selectedIds.value
  if (!ids.length) {
    flash.err('请先勾选用户')
    return
  }
  const labels = {
    enable: '批量启用',
    disable: '批量停用',
    delete: '批量删除',
    renew: '批量续期',
    add_traffic: '批量加流量',
  }
  const label = labels[action] || action
  if (action === 'delete') {
    if (!confirm(`确认删除选中的 ${ids.length} 个用户？不可恢复。`)) return
  } else if (!confirm(`确认对 ${ids.length} 个用户执行「${label}」？`)) {
    return
  }
  batchBusy.value = true
  try {
    const res = await api('/api/admin/users/batch', {
      method: 'POST',
      body: JSON.stringify({ ids, action, ...extra }),
    })
    flash.ok(`${label}完成：成功 ${res.success || 0}` + (res.failed ? `，失败 ${res.failed}` : ''))
    clearSelection()
    await loadUsers()
  } catch (e) {
    error.value = e.message
  } finally {
    batchBusy.value = false
  }
}

function routeName(u) {
  if (u.route_name) return u.route_name
  const id = u.route_id
  if (id == null || id === '') return '—'
  const r = (routes.value || []).find((x) => x.id === id || String(x.id) === String(id))
  return r ? r.name : `#${id}`
}

function statusLabel(s) {
  const m = { active: '正常', disabled: '停用', expired: '到期', over_quota: '超流量' }
  return m[s] || s || '—'
}

function isExpiringSoon(u) {
  if (!u.expire_at) return false
  const t = new Date(u.expire_at).getTime()
  if (Number.isNaN(t)) return false
  const days = (t - Date.now()) / 86400000
  return days >= 0 && days <= 3
}

function barStyle(u) {
  return { width: trafficPct(u) + '%' }
}

function trafficPct(u) {
  if (!u.traffic_limit_bytes) return 0
  return Math.min(100, Math.round(((u.traffic_used_bytes || 0) / u.traffic_limit_bytes) * 100))
}

function entryOf(u) {
  return u.entry_display || (u.entry_host ? `${u.entry_host}${u.entry_port ? ':' + u.entry_port : ''}` : '—')
}

function rateOf(u) {
  const live = rates.value[u.id]
  if (live) return live
  return { up: u.up_bps || 0, down: u.down_bps || 0 }
}

const statusCounts = computed(() => {
  const list = users.value || []
  let active = 0
  let expiring = 0
  let over = 0
  let disabled = 0
  let expired = 0
  for (const u of list) {
    if (u.status === 'active') active++
    if (u.status === 'disabled') disabled++
    if (u.status === 'expired') expired++
    if (u.status === 'over_quota') over++
    if (isExpiringSoon(u)) expiring++
  }
  return { all: list.length, active, expiring, over, disabled, expired }
})

const filtered = computed(() => {
  let list = users.value || []
  if (statusFilter.value === 'expiring') {
    list = list.filter((u) => isExpiringSoon(u))
  } else if (statusFilter.value !== 'all') {
    list = list.filter((u) => u.status === statusFilter.value)
  }
  const q = filter.value.trim().toLowerCase()
  if (q) {
    list = list.filter(
      (u) =>
        (u.username || '').toLowerCase().includes(q) ||
        (u.note || '').toLowerCase().includes(q) ||
        String(u.id).includes(q) ||
        routeName(u).toLowerCase().includes(q) ||
        entryOf(u).toLowerCase().includes(q),
    )
  }
  return list
})

function routeEntry(r) {
  if (!r) return ''
  return (
    r.entry_endpoint ||
    (r.front_host && r.front_port ? `${r.front_host}:${r.front_port}` : r.front_host || '') ||
    ''
  )
}

function userRouteKey(u) {
  const rid = u?.route_id
  if (rid == null || rid === '' || rid === 0) return 0
  const n = Number(rid)
  return Number.isFinite(n) ? n : 0
}

/** 所有已创建隧道都成组（含 0 人）；过滤后的用户落入对应组；未绑定置底 */
const groupedUsers = computed(() => {
  const list = filtered.value || []
  const byRoute = new Map()
  for (const u of list) {
    const key = userRouteKey(u)
    if (!byRoute.has(key)) byRoute.set(key, [])
    byRoute.get(key).push(u)
  }

  const groups = []
  for (const r of routes.value || []) {
    const id = Number(r.id)
    if (!Number.isFinite(id) || id <= 0) continue
    const usersIn = byRoute.get(id) || []
    usersIn.sort((a, b) => String(a.username || '').localeCompare(String(b.username || ''), 'zh'))
    groups.push({
      route_id: id,
      name: r.name || `隧道 #${id}`,
      entry: routeEntry(r),
      path: r.path_summary || '',
      users: usersIn,
      canOpen: true,
    })
  }
  groups.sort((a, b) => String(a.name).localeCompare(String(b.name), 'zh'))

  // 用户绑了已删除隧道 / 未绑定
  const known = new Set(groups.map((g) => g.route_id))
  const orphanKeys = [...byRoute.keys()].filter((k) => k !== 0 && !known.has(k))
  for (const key of orphanKeys.sort((a, b) => a - b)) {
    const usersIn = byRoute.get(key) || []
    usersIn.sort((a, b) => String(a.username || '').localeCompare(String(b.username || ''), 'zh'))
    const sample = usersIn[0]
    groups.push({
      route_id: key,
      name: routeName(sample || { route_id: key }),
      entry: sample ? entryOf(sample) : '',
      path: '',
      users: usersIn,
      canOpen: false,
    })
  }

  const unbound = byRoute.get(0) || []
  if (unbound.length) {
    unbound.sort((a, b) => String(a.username || '').localeCompare(String(b.username || ''), 'zh'))
    groups.push({
      route_id: 0,
      name: '未绑定隧道',
      entry: '',
      path: '',
      users: unbound,
      canOpen: false,
    })
  }
  return groups
})

async function loadUsers() {
  try {
    const [us, rs] = await Promise.all([api('/api/admin/users'), api('/api/admin/routes')])
    users.value = Array.isArray(us) ? us : []
    routes.value = Array.isArray(rs) ? rs : []
    const next = { ...rates.value }
    for (const u of users.value) {
      next[u.id] = { up: u.up_bps || 0, down: u.down_bps || 0, ts: u.rate_ts || 0 }
    }
    rates.value = next
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

async function loadRates() {
  try {
    const list = await api('/api/admin/metrics/rates')
    if (!Array.isArray(list)) return
    const next = { ...rates.value }
    const now = Math.floor(Date.now() / 1000)
    for (const id of Object.keys(next)) {
      const r = next[id]
      // 15s: agent posts every 1s but panel clock / brief network lag should not zero the UI
      if (r && r.ts && now - r.ts > 15) {
        next[id] = { up: 0, down: 0, ts: r.ts }
      }
    }
    for (const s of list) {
      const ts = s.ts || 0
      let up = s.up_bps || 0
      let down = s.down_bps || 0
      if (ts && now - ts > 15) {
        up = 0
        down = 0
      }
      next[s.user_id] = { up, down, ts }
    }
    rates.value = next
  } catch {
    /* ignore */
  }
}

function blankForm() {
  // 新建全部留白，不预选隧道/流量/到期；需要时点套餐按钮
  Object.assign(form, {
    username: '',
    expire_at: '',
    traffic_limit_gb: 0,
    route_id: null,
    entry_host: '',
    entry_port: null,
    note: '',
    status: 'active',
  })
}

function applyPackage(p) {
  form.traffic_limit_gb = p.gb
  if (p.days > 0) {
    const d = new Date()
    d.setDate(d.getDate() + p.days)
    form.expire_at = d.toISOString().slice(0, 10)
  } else {
    form.expire_at = ''
  }
}

function openCreate(routeId) {
  closeMore()
  blankForm()
  if (routeId != null && routeId !== '' && Number(routeId) > 0) {
    form.route_id = Number(routeId)
  }
  created.value = null
  editingId.value = null
  mode.value = 'create'
  show.value = true
  error.value = ''
}

function closeMore() {
  moreId.value = null
  moreMenuStyle.value = {}
}

function toggleMore(u, e) {
  e?.stopPropagation?.()
  if (moreId.value === u.id) {
    closeMore()
    return
  }
  moreId.value = u.id
  const el = e?.currentTarget
  if (!el?.getBoundingClientRect) {
    moreMenuStyle.value = { position: 'fixed', right: '16px', top: '80px', zIndex: 1200 }
    return
  }
  const rect = el.getBoundingClientRect()
  const menuH = 96
  const menuW = 128
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
  if (moreId.value == null) return
  const t = e.target
  if (t?.closest?.('.more-menu-float') || t?.closest?.('.more-trigger')) return
  closeMore()
}

function onWinReposition() {
  if (moreId.value != null) closeMore()
}

async function copy(text) {
  try {
    await copyText(text)
    flash.ok('已复制')
  } catch {
    flash.err('复制失败，请手动选中')
  }
}

function userInfoURL(u) {
  if (!u) return ''
  if (u.info_url) return u.info_url
  const tok = u.sub_token || ''
  if (!tok) return ''
  // fallback: same-origin path (works even if panel_url not set)
  return `${window.location.origin}/u/${tok}`
}

async function copyUserInfo(u) {
  const url = userInfoURL(u)
  if (!url) {
    flash.err('该用户无查询链接（缺少 token）')
    return
  }
  await copy(url)
  flash.ok('已复制查询页链接')
}

function openEdit(u) {
  closeMore()
  Object.assign(form, {
    username: u.username || '',
    expire_at: u.expire_at ? String(u.expire_at).slice(0, 10) : '',
    traffic_limit_gb: u.traffic_limit_bytes
      ? Math.round(u.traffic_limit_bytes / (1024 * 1024 * 1024))
      : 0,
    route_id: u.route_id ?? null,
    entry_host: u.entry_host || '',
    entry_port: u.entry_port || null,
    note: u.note || '',
    status: u.status === 'disabled' ? 'disabled' : 'active',
  })
  created.value = null
  editingId.value = u.id
  mode.value = 'edit'
  show.value = true
  error.value = ''
  moreId.value = null
}

async function create() {
  if (!form.username.trim()) {
    error.value = '请填写用户名'
    return
  }
  saving.value = true
  try {
    const body = {
      username: form.username.trim(),
      expire_at: form.expire_at || undefined,
      traffic_limit_bytes: Math.round(Number(form.traffic_limit_gb || 0) * 1024 * 1024 * 1024),
      route_id: form.route_id ? Number(form.route_id) : null,
      entry_host: (form.entry_host || '').trim() || undefined,
      entry_port: form.entry_port ? Number(form.entry_port) : undefined,
      note: form.note,
    }
    created.value = await api('/api/admin/users', {
      method: 'POST',
      body: JSON.stringify(body),
    })
    mode.value = 'created'
    formFlash.ok('用户已创建')
    await loadUsers()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function saveEdit() {
  if (!editingId.value) return
  saving.value = true
  try {
    const body = {
      status: form.status,
      expire_at: form.expire_at || undefined,
      clear_expire: !form.expire_at,
      traffic_limit_bytes: Math.round(Number(form.traffic_limit_gb || 0) * 1024 * 1024 * 1024),
      route_id: form.route_id ? Number(form.route_id) : 0,
      entry_host: (form.entry_host || '').trim(),
      entry_port: form.entry_port ? Number(form.entry_port) : 0,
      note: form.note,
    }
    await api(`/api/admin/users/${editingId.value}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    })
    formFlash.ok('已保存')
    await loadUsers()
    setTimeout(() => { show.value = false }, 900)
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function resetPw(id) {
  closeMore()
  try {
    const res = await api(`/api/admin/users/${id}/reset-password`, { method: 'POST' })
    resetInfo.value = {
      username: res.username || '',
      proxy_password: res.proxy_password || '',
    }
    resetShow.value = true
  } catch (e) {
    error.value = e.message
  }
}

async function resetSub(u) {
  closeMore()
  const name = u?.username || `#${u?.id}`
  if (
    !confirm(
      `确认重置「${name}」的订阅 / 查询页 token？\n\n旧查询页链接与订阅 URL 将立即失效，需重新复制分享。`,
    )
  ) {
    return
  }
  try {
    const res = await api(`/api/admin/users/${u.id}/reset-sub`, { method: 'POST' })
    formFlash.ok('已重置订阅 token')
    await loadUsers()
    if (res?.sub_token || res?.info_url || res?.subscription) {
      const url = res.info_url || (res.sub_token ? `${window.location.origin}/u/${res.sub_token}` : '')
      if (url) {
        try {
          await copyText(url)
          formFlash.ok('已重置并复制新查询页链接')
        } catch {
          /* keep toast */
        }
      }
    }
  } catch (e) {
    error.value = e.message
  }
}

async function remove(u) {
  closeMore()
  const id = typeof u === 'object' && u ? u.id : u
  const name = typeof u === 'object' && u ? u.username || `#${id}` : `#${id}`
  if (!confirm(`确认删除用户「${name}」？\n\n将从落地 mita 下发配置中移除，不可恢复。`)) return
  try {
    await api(`/api/admin/users/${id}`, { method: 'DELETE' })
    flash.ok(`已删除 ${name}`)
    await loadUsers()
  } catch (e) {
    error.value = e.message
  }
}

async function toggle(u) {
  closeMore()
  const res = await api(`/api/admin/users/${u.id}/toggle`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
  flash.ok(res.status === 'disabled' ? '已停用' : '已启用')
  await loadUsers()
}

function openRenew(u) {
  closeMore()
  renewUser.value = u
  renewDays.value = 30
  renewDate.value = ''
  renewShow.value = true
}

async function doRenew() {
  if (!renewUser.value) return
  saving.value = true
  try {
    const body = renewDate.value
      ? { expire_at: renewDate.value }
      : { days: Number(renewDays.value) || 30 }
    await api(`/api/admin/users/${renewUser.value.id}/renew`, {
      method: 'POST',
      body: JSON.stringify(body),
    })
    flash.ok('已续期')
    renewShow.value = false
    await loadUsers()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

function openAddTraffic(u) {
  closeMore()
  trafficUser.value = u
  trafficGB.value = 50
  trafficShow.value = true
}

function openMultiplier(u) {
  closeMore()
  multUser.value = u
  const m = Number(u?.display_multiplier)
  multValue.value = Number.isFinite(m) && m > 0 ? m : 1
  multShow.value = true
}

async function doSetMultiplier() {
  if (!multUser.value) return
  let m = Number(multValue.value)
  if (!Number.isFinite(m) || m <= 0) m = 1
  if (m < 0.1 || m > 100) {
    error.value = '倍率需在 0.1～100 之间'
    return
  }
  saving.value = true
  try {
    await api(`/api/admin/users/${multUser.value.id}/display-multiplier`, {
      method: 'POST',
      body: JSON.stringify({ multiplier: m }),
    })
    flash.ok(m === 1 ? '已恢复真实显示（×1）' : `查询页已设为 ×${m}`)
    multShow.value = false
    await loadUsers()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

function formatMbps(bps) {
  const n = Number(bps) || 0
  if (n <= 0) return '不限'
  const mbps = (n * 8) / 1e6
  if (mbps >= 100) return `${Math.round(mbps)} Mbps`
  if (mbps >= 10) return `${mbps.toFixed(1)} Mbps`
  if (mbps >= 1) return `${mbps.toFixed(2)} Mbps`
  return `${(mbps * 1000).toFixed(0)} Kbps`
}

function openSpeedLimit(u) {
  closeMore()
  speedUser.value = u
  const bps = Number(u?.speed_limit_bps) || 0
  if (bps > 0) {
    // show 1 decimal when needed
    const m = (bps * 8) / 1e6
    speedMbps.value = Math.round(m * 100) / 100
  } else {
    speedMbps.value = 0
  }
  speedShow.value = true
}

async function doSetSpeedLimit(clear = false) {
  if (!speedUser.value) return
  let mbps = clear ? 0 : Number(speedMbps.value)
  if (!Number.isFinite(mbps) || mbps < 0) mbps = 0
  if (mbps > 10000) {
    error.value = '限速最大 10000 Mbps'
    return
  }
  saving.value = true
  try {
    await api(`/api/admin/users/${speedUser.value.id}/speed-limit`, {
      method: 'POST',
      body: JSON.stringify({ mbps }),
    })
    flash.ok(mbps > 0 ? `已限速 ${formatMbps(mbps * 1e6 / 8)}` : '已取消限速')
    speedShow.value = false
    await loadUsers()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function doAddTraffic(unlimited = false) {
  if (!trafficUser.value) return
  saving.value = true
  try {
    const body = unlimited
      ? { unlimited: true }
      : { add_gb: Number(trafficGB.value) || 0 }
    await api(`/api/admin/users/${trafficUser.value.id}/add-traffic`, {
      method: 'POST',
      body: JSON.stringify(body),
    })
    flash.ok(unlimited ? '已改为不限流量' : `已加 ${trafficGB.value} GB`)
    trafficShow.value = false
    await loadUsers()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function makeQR(text) {
  if (!text) return ''
  return QRCode.toDataURL(text, {
    width: 260,
    margin: 2,
    color: { dark: '#0f172a', light: '#ffffff' },
    errorCorrectionLevel: 'M',
  })
}

async function openSub(u) {
  closeMore()
  subUser.value = u
  shareURL.value = ''
  shareURLs.value = ''
  subQR.value = ''
  entries.value = []
  mihomoYAML.value = ''
  subShow.value = true
  subLoading.value = true
  try {
    let detail = null
    if (u?.id) {
      try {
        detail = await api(`/api/admin/users/${u.id}/share`)
      } catch {
        detail = await api(`/api/admin/users/${u.id}`)
      }
    }
    if (detail) {
      shareURL.value = detail.share_url || ''
      shareURLs.value = detail.share_urls || detail.share_url || ''
      entries.value = Array.isArray(detail.entries) ? detail.entries : []
      mihomoYAML.value = detail.mihomo_yaml || ''
      if (detail.user) subUser.value = { ...u, ...detail.user }
    }
    if (!shareURL.value && created.value && created.value.user?.id === u?.id) {
      shareURL.value = created.value.share_url || ''
      shareURLs.value = created.value.share_urls || shareURL.value
      entries.value = created.value.entries || []
      mihomoYAML.value = created.value.mihomo_yaml || ''
    }
    if (!shareURL.value && u?.share_url) shareURL.value = u.share_url
    if (shareURL.value) subQR.value = await makeQR(shareURL.value)
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    subLoading.value = false
  }
}

async function downloadMihomo(u) {
  const id = u?.id || subUser.value?.id
  if (!id) {
    flash.ok('无用户 ID')
    return
  }
  try {
    const token = getToken()
    const res = await fetch(`/api/admin/users/${id}/mihomo.yaml`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (!res.ok) {
      const t = await res.text()
      throw new Error(t || res.statusText)
    }
    const blob = await res.blob()
    const name = `mihomo-${u?.username || subUser.value?.username || id}.yaml`
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(a.href)
    flash.ok(`已下载 ${name}`)
  } catch (e) {
    if (mihomoYAML.value) {
      const blob = new Blob([mihomoYAML.value], { type: 'application/x-yaml' })
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = `mihomo-${subUser.value?.username || 'user'}.yaml`
      document.body.appendChild(a)
      a.click()
      a.remove()
      flash.ok('已下载 YAML')
      return
    }
    error.value = e.message || '下载失败'
  }
}

onMounted(() => {
  loadUsers()
  loadRates()
  listTimer = setInterval(loadUsers, 10000)
  rateTimer = setInterval(loadRates, 1000)
  document.addEventListener('pointerdown', onDocPointerDown, true)
  window.addEventListener('resize', onWinReposition)
  window.addEventListener('scroll', onWinReposition, true)
})
onUnmounted(() => {
  clearInterval(listTimer)
  clearInterval(rateTimer)
  document.removeEventListener('pointerdown', onDocPointerDown, true)
  window.removeEventListener('resize', onWinReposition)
  window.removeEventListener('scroll', onWinReposition, true)
})
</script>

<template>
  <div v-if="error && !show && !subShow && !renewShow && !trafficShow && !multShow && !speedShow && !resetShow" class="action-feedback err page-action-feedback" @click="error = ''">{{ error }}</div>
  <div
    v-if="flash.msg && !show && !subShow && !renewShow && !trafficShow && !multShow && !speedShow && !resetShow"
    class="action-feedback page-action-feedback"
    :class="flash.kind"
    @click="flash.clear()"
  >{{ flash.msg }}</div>

  <Teleport to="body">
    <div
      v-if="moreId != null && moreUser"
      class="more-menu more-menu-float"
      :style="moreMenuStyle"
      @click.stop
    >
      <button type="button" @click="copyUserInfo(moreUser); closeMore()">复制查询页</button>
      <button type="button" @click="openMultiplier(moreUser); closeMore()">
        倍率设置{{ moreUser.display_multiplier && moreUser.display_multiplier !== 1 ? ` · ×${moreUser.display_multiplier}` : '' }}
      </button>
      <button type="button" @click="openAddTraffic(moreUser); closeMore()">加流量</button>
      <button type="button" @click="openSpeedLimit(moreUser); closeMore()">
        限速{{ moreUser.speed_limit_bps > 0 ? ` · ${formatMbps(moreUser.speed_limit_bps)}` : '' }}
      </button>
      <button type="button" @click="resetPw(moreUser.id); closeMore()">重置密码</button>
      <button type="button" @click="resetSub(moreUser)">重置订阅</button>
    </div>
  </Teleport>

  <div class="page-tabs">
    <div class="page-tab active">用户</div>
  </div>

  <div class="stat-chips">
    <button
      type="button"
      class="stat-chip"
      :class="{ active: statusFilter === 'all' }"
      @click="statusFilter = 'all'"
    >
      全部 <strong>{{ statusCounts.all }}</strong>
    </button>
    <button
      type="button"
      class="stat-chip ok"
      :class="{ active: statusFilter === 'active' }"
      @click="statusFilter = 'active'"
    >
      正常 <strong>{{ statusCounts.active }}</strong>
    </button>
    <button
      type="button"
      class="stat-chip warn"
      :class="{ active: statusFilter === 'expiring' }"
      @click="statusFilter = 'expiring'"
    >
      3天内到期 <strong>{{ statusCounts.expiring }}</strong>
    </button>
    <button
      type="button"
      class="stat-chip err"
      :class="{ active: statusFilter === 'over_quota' }"
      @click="statusFilter = 'over_quota'"
    >
      超流 <strong>{{ statusCounts.over }}</strong>
    </button>
    <button
      type="button"
      class="stat-chip"
      :class="{ active: statusFilter === 'disabled' }"
      @click="statusFilter = 'disabled'"
    >
      停用 <strong>{{ statusCounts.disabled }}</strong>
    </button>
    <button
      type="button"
      class="stat-chip"
      :class="{ active: statusFilter === 'expired' }"
      @click="statusFilter = 'expired'"
    >
      到期 <strong>{{ statusCounts.expired }}</strong>
    </button>
  </div>

  <div class="panel-toolbar users-toolbar">
    <div class="toolbar-left" style="flex-wrap:wrap;gap:8px">
      <input class="input-filter" v-model="filter" placeholder="搜索用户 / 备注 / 隧道" />
      <template v-if="selectedCount">
        <span class="badge">已选 {{ selectedCount }}</span>
        <button class="btn btn-ghost btn-sm" :disabled="batchBusy" @click="batchAction('enable')">启用</button>
        <button class="btn btn-ghost btn-sm" :disabled="batchBusy" @click="batchAction('disable')">停用</button>
        <button class="btn btn-ghost btn-sm" :disabled="batchBusy" @click="batchAction('renew', { days: 30 })">
          +30天
        </button>
        <button class="btn btn-ghost btn-sm" :disabled="batchBusy" @click="batchAction('add_traffic', { add_gb: 50 })">
          +50G
        </button>
        <button class="btn btn-ghost btn-sm" :disabled="batchBusy" @click="batchAction('delete')">删除</button>
        <button class="btn btn-link btn-sm" @click="clearSelection">清空</button>
      </template>
      <button v-else class="btn btn-ghost btn-sm" @click="selectVisible">全选当前</button>
    </div>
  </div>

  <div v-if="groupedUsers.length" class="user-groups">
    <section v-for="g in groupedUsers" :key="'rg-' + g.route_id" class="user-group">
      <header class="user-group-hd">
        <div class="user-group-title">
          <span class="user-group-name">{{ g.name }}</span>
          <span class="badge">{{ g.users.length }} 人</span>
          <button
            v-if="g.canOpen"
            type="button"
            class="btn btn-primary btn-sm user-group-open"
            @click="openCreate(g.route_id)"
          >
            开户
          </button>
        </div>
        <div class="user-group-meta muted mono">
          <template v-if="g.route_id">
            <span v-if="g.entry">入口 {{ g.entry }}</span>
            <span v-if="g.path" class="user-group-path">{{ g.path }}</span>
            <span class="muted">#{{ g.route_id }}</span>
          </template>
          <template v-else>未分配隧道，可点隧道组标题旁「开户」或下方空状态开户</template>
        </div>
      </header>
      <div class="table-wrap user-group-table">
        <table class="data table-users">
          <thead>
            <tr>
              <th class="col-check" style="width:36px"></th>
              <th class="col-user">用户</th>
              <th class="col-status">状态</th>
              <th class="col-date">到期</th>
              <th class="col-traffic">流量</th>
              <th class="col-speed">实时</th>
              <th class="col-entry">入口</th>
              <th class="col-ops">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in g.users" :key="u.id">
              <td class="col-check" @click.stop>
                <input type="checkbox" :checked="isSelected(u.id)" @change="toggleSelect(u.id)" />
              </td>
              <td class="col-user">
                <div class="name-link">
                  {{ u.username }}
                  <span
                    v-if="u.display_multiplier && Number(u.display_multiplier) !== 1"
                    class="badge mono"
                    style="margin-left:6px;font-size:10px"
                    :title="'查询页显示倍率 ×' + u.display_multiplier"
                  >×{{ u.display_multiplier }}</span>
                  <span
                    v-if="u.speed_limit_bps > 0"
                    class="badge mono"
                    style="margin-left:6px;font-size:10px"
                    :title="'限速 ' + formatMbps(u.speed_limit_bps)"
                  >↓{{ formatMbps(u.speed_limit_bps) }}</span>
                </div>
                <div v-if="u.note" class="muted note-line">{{ u.note }}</div>
                <div class="muted mono" style="font-size:11px">#{{ u.id }}</div>
              </td>
              <td class="col-status">
                <span class="badge" :class="statusBadge(u.status)">
                  <span class="dot"></span>{{ statusLabel(u.status) }}
                </span>
              </td>
              <td class="col-date mono" :class="{ 'warn-text': isExpiringSoon(u) }">
                {{ u.expire_at ? String(u.expire_at).slice(0, 10) : "永久" }}
              </td>
              <td class="col-traffic">
                <div class="mono traffic-line">
                  {{ formatBytes(u.traffic_used_bytes) }}
                  <span class="muted">/</span>
                  {{ u.traffic_limit_bytes ? formatBytes(u.traffic_limit_bytes) : "∞" }}
                  <span
                    v-if="u.display_multiplier && Number(u.display_multiplier) !== 1"
                    class="muted"
                    style="font-size:11px;margin-left:4px"
                    :title="'查询页按 ×' + u.display_multiplier + ' 显示已用'"
                  >
                    · 页显 {{ formatBytes(Math.round((u.traffic_used_bytes || 0) * Number(u.display_multiplier))) }}
                  </span>
                </div>
                <div v-if="u.traffic_limit_bytes" class="bar">
                  <div class="bar-fill" :style="barStyle(u)"></div>
                </div>
              </td>
              <td class="col-speed mono">
                <div class="speed-line">
                  <span class="speed-down">↓ {{ formatBps(rateOf(u).down) }}</span>
                  <span class="speed-up">↑ {{ formatBps(rateOf(u).up) }}</span>
                </div>
              </td>
              <td class="col-entry mono">{{ entryOf(u) }}</td>
              <td class="col-ops">
                <div class="row-actions user-ops">
                  <button class="btn btn-link btn-sm" @click="openSub(u)">扫码</button>
                  <button class="btn btn-link btn-sm" @click="openEdit(u)">编辑</button>
                  <button class="btn btn-link btn-sm" @click="openRenew(u)">续期</button>
                  <button class="btn btn-link btn-sm" @click="toggle(u)">
                    {{ u.status === 'disabled' ? '启用' : '停用' }}
                  </button>
                  <button class="btn btn-link-danger btn-sm" @click="remove(u)">删除</button>
                  <button
                    type="button"
                    class="btn btn-link btn-sm more-trigger"
                    :class="{ active: moreId === u.id }"
                    @click="toggleMore(u, $event)"
                  >
                    更多
                  </button>
                </div>
              </td>
            </tr>
            <tr v-if="!g.users.length">
              <td colspan="8" class="user-group-empty">
                暂无用户
                <button
                  v-if="g.canOpen"
                  type="button"
                  class="btn btn-link btn-sm"
                  @click="openCreate(g.route_id)"
                >
                  在此隧道开户
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
  <div v-else class="empty">
    <template v-if="users.length">无匹配用户</template>
    <template v-else-if="!(routes || []).length">请先在「隧道」创建隧道，再回来开户</template>
    <template v-else>暂无用户</template>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal" style="width:min(560px,100%)">
      <div class="modal-hd">
        <h3>
          <template v-if="mode === 'created'">用户已创建</template>
          <template v-else-if="mode === 'edit'">编辑用户</template>
          <template v-else-if="form.route_id">
            开户 · {{ routeName({ route_id: form.route_id }) }}
          </template>
          <template v-else>开户</template>
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
          <div v-if="mode === 'create'" class="pkg-row">
            <button
              v-for="p in packages"
              :key="p.name"
              type="button"
              class="btn btn-ghost btn-sm"
              @click="applyPackage(p)"
            >
              {{ p.name }} · {{ p.days }}天 · {{ p.gb ? p.gb + 'G' : '不限' }}
            </button>
          </div>
          <div class="form-grid">
            <div class="field">
              <label>用户名</label>
              <input v-model="form.username" :disabled="mode === 'edit'" />
            </div>
            <div class="field" v-if="mode === 'edit'">
              <label>状态</label>
              <select v-model="form.status">
                <option value="active">正常</option>
                <option value="disabled">停用</option>
              </select>
            </div>
            <div class="field">
              <label>到期日（可空=永不过期）</label>
              <input v-model="form.expire_at" type="date" />
            </div>
            <div class="field">
              <label>流量上限 (GB，0=不限)</label>
              <input
                v-model.number="form.traffic_limit_gb"
                type="number"
                min="0"
                placeholder="0=不限"
              />
            </div>
            <div class="field">
              <label>隧道</label>
              <select v-model="form.route_id">
                <option :value="null">未绑定</option>
                <option v-for="r in routes" :key="r.id" :value="r.id">{{ r.name }} (#{{ r.id }})</option>
              </select>
            </div>
            <div class="field">
              <label>公网入口 IP（可空=用隧道前置）</label>
              <input v-model="form.entry_host" placeholder="可选" />
            </div>
            <div class="field">
              <label>入口端口（可空=用隧道端口）</label>
              <input
                v-model.number="form.entry_port"
                type="number"
                min="1"
                max="65535"
                placeholder="可选"
              />
            </div>
          </div>
          <div class="field">
            <label>备注</label>
            <input v-model="form.note" />
          </div>
        </template>
        <template v-else>
          <div class="kv">
            <dt>代理密码</dt>
            <dd>{{ created.proxy_password }}</dd>
            <dt>查询页</dt>
            <dd style="word-break:break-all" class="mono">
              {{ created.info_url || userInfoURL(created.user) || '—' }}
            </dd>
            <dt>节点链接</dt>
            <dd style="word-break:break-all" class="mono">{{ created.share_url || '（无可用入口）' }}</dd>
          </div>
          <div class="row-actions" style="margin-top:4px;flex-wrap:wrap">
            <button class="btn btn-ghost btn-sm" @click="copy(created.proxy_password)">复制密码</button>
            <button
              class="btn btn-primary btn-sm"
              :disabled="!(created.info_url || userInfoURL(created.user))"
              @click="copy(created.info_url || userInfoURL(created.user))"
            >
              复制查询页
            </button>
            <button class="btn btn-ghost btn-sm" :disabled="!created.share_url" @click="copy(created.share_url)">
              复制节点链接
            </button>
            <button
              class="btn btn-ghost btn-sm"
              @click="
                openSub({
                  id: created.user?.id,
                  username: created.user?.username || form.username,
                  share_url: created.share_url,
                })
              "
            >
              扫码 / YAML
            </button>
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

  <div v-if="resetShow && resetInfo" class="modal-mask" @click.self="resetShow = false">
    <div class="modal" style="width:min(420px,100%)">
      <div class="modal-hd">
        <h3>新代理密码{{ resetInfo.username ? ' · ' + resetInfo.username : '' }}</h3>
        <button class="btn btn-ghost btn-sm" @click="resetShow = false">关闭</button>
      </div>
      <div class="modal-bd">
        <p class="help-text" style="margin:0 0 10px">请立即复制保存。关闭后无法再次查看（需再重置）。</p>
        <div class="field">
          <label>代理密码</label>
          <input class="mono" readonly :value="resetInfo.proxy_password" />
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="resetShow = false">关闭</button>
        <button class="btn btn-primary" @click="copy(resetInfo.proxy_password)">复制密码</button>
      </div>
    </div>
  </div>

  <div v-if="renewShow" class="modal-mask" @click.self="renewShow = false">
    <div class="modal" style="width:min(400px,100%)">
      <div class="modal-hd">
        <h3>续期 · {{ renewUser?.username }}</h3>
        <button class="btn btn-ghost btn-sm" @click="renewShow = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="pkg-row">
          <button class="btn btn-ghost btn-sm" @click="renewDays = 7">+7 天</button>
          <button class="btn btn-ghost btn-sm" @click="renewDays = 30">+30 天</button>
          <button class="btn btn-ghost btn-sm" @click="renewDays = 90">+90 天</button>
        </div>
        <div class="field">
          <label>延长天数</label>
          <input v-model.number="renewDays" type="number" min="1" />
        </div>
        <div class="field">
          <label>或指定到期日（优先）</label>
          <input v-model="renewDate" type="date" />
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="renewShow = false">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="doRenew">确认续期</button>
      </div>
    </div>
  </div>

  <div v-if="trafficShow" class="modal-mask" @click.self="trafficShow = false">
    <div class="modal" style="width:min(400px,100%)">
      <div class="modal-hd">
        <h3>加流量 · {{ trafficUser?.username }}</h3>
        <button class="btn btn-ghost btn-sm" @click="trafficShow = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="pkg-row">
          <button class="btn btn-ghost btn-sm" @click="trafficGB = 10">+10G</button>
          <button class="btn btn-ghost btn-sm" @click="trafficGB = 50">+50G</button>
          <button class="btn btn-ghost btn-sm" @click="trafficGB = 100">+100G</button>
        </div>
        <div class="field">
          <label>增加 (GB)</label>
          <input v-model.number="trafficGB" type="number" min="1" />
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="trafficShow = false">取消</button>
        <button class="btn btn-ghost" :disabled="saving" @click="doAddTraffic(true)">改为不限</button>
        <button class="btn btn-primary" :disabled="saving" @click="doAddTraffic(false)">确认加流量</button>
      </div>
    </div>
  </div>

  <div v-if="multShow" class="modal-mask" @click.self="multShow = false">
    <div class="modal" style="width:min(400px,100%)">
      <div class="modal-hd">
        <h3>倍率设置 · {{ multUser?.username }}</h3>
        <button class="btn btn-ghost btn-sm" @click="multShow = false">关闭</button>
      </div>
      <div class="modal-bd">
        <p class="muted" style="margin: 0 0 12px; font-size: 12.5px; line-height: 1.5">
          仅影响用户<strong>查询页</strong>的「已用 / 今日 / 实时」显示；配额与后台列表仍为真实值。
        </p>
        <div class="pkg-row">
          <button class="btn btn-ghost btn-sm" type="button" @click="multValue = 1">×1</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="multValue = 1.5">×1.5</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="multValue = 2">×2</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="multValue = 3">×3</button>
        </div>
        <div class="field">
          <label>显示倍率（0.1～100）</label>
          <input v-model.number="multValue" type="number" min="0.1" max="100" step="0.1" />
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="multShow = false">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="doSetMultiplier">保存</button>
      </div>
    </div>
  </div>

  <div v-if="speedShow" class="modal-mask" @click.self="speedShow = false">
    <div class="modal" style="width:min(420px,100%)">
      <div class="modal-hd">
        <h3>限速 · {{ speedUser?.username }}</h3>
        <button class="btn btn-ghost btn-sm" @click="speedShow = false">关闭</button>
      </div>
      <div class="modal-bd">
        <p class="muted" style="margin: 0 0 12px; font-size: 12.5px; line-height: 1.5">
          设置该用户最大网速（Mbps）。填 <strong>0</strong> 表示不限速。
          保存后会重建落地配置并下发。
        </p>
        <div class="pkg-row">
          <button class="btn btn-ghost btn-sm" type="button" @click="speedMbps = 0">不限</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="speedMbps = 5">5M</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="speedMbps = 10">10M</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="speedMbps = 20">20M</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="speedMbps = 50">50M</button>
          <button class="btn btn-ghost btn-sm" type="button" @click="speedMbps = 100">100M</button>
        </div>
        <div class="field">
          <label>限速 (Mbps，0=不限)</label>
          <input v-model.number="speedMbps" type="number" min="0" max="10000" step="0.1" />
        </div>
        <p class="help-text" style="margin:0">
          当前：{{ speedUser?.speed_limit_bps > 0 ? formatMbps(speedUser.speed_limit_bps) : '不限' }}
        </p>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="speedShow = false">取消</button>
        <button class="btn btn-ghost" :disabled="saving" @click="doSetSpeedLimit(true)">取消限速</button>
        <button class="btn btn-primary" :disabled="saving" @click="doSetSpeedLimit(false)">保存</button>
      </div>
    </div>
  </div>

  <div v-if="subShow" class="modal-mask" @click.self="subShow = false">
    <div class="modal" style="width:min(520px,100%)">
      <div class="modal-hd">
        <h3>扫码 / 配置 · {{ subUser?.username || '' }}</h3>
        <button class="btn btn-ghost btn-sm" @click="subShow = false">关闭</button>
      </div>
      <div class="modal-bd share-modal">
        <div v-if="subLoading" class="muted" style="padding:24px;text-align:center">生成中…</div>
        <template v-else>
          <div class="qr-center">
            <div v-if="subQR" class="qr-box">
              <img :src="subQR" alt="节点二维码" width="260" height="260" />
            </div>
            <div v-else class="muted" style="padding:16px;text-align:center">
              无法生成二维码（未绑定隧道 / 无前置地址）
            </div>
          </div>
          <div class="field">
            <label>节点链接（扫码内容 · mierus://）</label>
            <textarea readonly rows="3" class="mono share-ta" :value="shareURL" />
          </div>
          <div v-if="entries.length > 1" class="field">
            <label>全部入口</label>
            <div v-for="(e, i) in entries" :key="i" class="mono entry-row">
              <span>{{ e.name }} · {{ e.host }}:{{ e.port }}</span>
              <button class="btn btn-link btn-sm" @click="copy(e.url)">复制</button>
            </div>
          </div>
          <div class="field">
            <label>Mihomo / Clash Meta YAML</label>
            <textarea readonly rows="10" class="mono share-ta" :value="mihomoYAML" />
          </div>
        </template>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="subShow = false">关闭</button>
        <button class="btn btn-ghost" :disabled="!shareURL" @click="copy(shareURL)">复制链接</button>
        <button class="btn btn-ghost" :disabled="!mihomoYAML" @click="copy(mihomoYAML)">复制 YAML</button>
        <button class="btn btn-primary" :disabled="!mihomoYAML && !subUser?.id" @click="downloadMihomo(subUser)">
          下载 YAML
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.users-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.toolbar-left {
  display: flex;
  gap: 8px;
  align-items: center;
  flex: 1;
  min-width: 200px;
}
.input-filter {
  flex: 1;
  max-width: 280px;
  height: 34px;
  border: 1px solid var(--border-line);
  border-radius: 6px;
  padding: 0 10px;
  background: #fff;
}
.status-filter {
  height: 34px;
  border: 1px solid var(--border-line);
  border-radius: 6px;
  padding: 0 8px;
  background: #fff;
}
.table-users { table-layout: fixed; width: 100%; }
.table-users th, .table-users td { vertical-align: middle; }
.col-user { width: 12%; }
.col-status { width: 8%; }
.col-date { width: 10%; }
.col-traffic { width: 14%; }
.col-speed { width: 14%; }
.col-route { width: 12%; }
.col-entry { width: 14%; }
.col-ops { width: 22%; min-width: 220px; }
.user-ops {
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 2px 4px;
  max-width: 100%;
}
.note-line {
  font-size: 12px;
  margin-top: 1px;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.warn-text { color: var(--warning); font-weight: 600; }
.traffic-line { font-size: 12px; }
.bar {
  margin-top: 4px;
  height: 4px;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 2px;
  overflow: hidden;
}
.bar-fill { height: 100%; background: var(--accent); min-width: 0; }
.speed-line {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12px;
  line-height: 1.3;
}
.speed-down { color: var(--success); }
.speed-up { color: var(--link); }
.more-trigger.active { font-weight: 700; }
.pkg-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 12px;
}
.share-modal { text-align: left; }
.qr-center {
  display: flex;
  justify-content: center;
  align-items: center;
  margin-bottom: 8px;
}
.qr-box {
  display: inline-flex;
  padding: 14px;
  background: #fff;
  border: 1px solid var(--border-line);
  border-radius: 6px;
}
.qr-box img { display: block; }
.share-ta {
  width: 100%;
  resize: vertical;
  background: var(--bg-elevated);
  border: 1px solid var(--border-line);
  border-radius: 6px;
  padding: 12px;
  font-size: 12px;
  line-height: 1.45;
}
.entry-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  word-break: break-all;
  padding: 6px 0;
  border-bottom: 1px solid var(--border);
}
</style>
