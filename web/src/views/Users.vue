<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import QRCode from 'qrcode'
import { api, copyText, formatBytes, formatBps, getToken, statusBadge } from '../api'

const users = ref([])
const routes = ref([])
const rates = ref({}) // id -> {up, down, ts}
const error = ref('')
const toast = ref('')
const filter = ref('')
const statusFilter = ref('all')
const show = ref(false)
const mode = ref('create') // create | edit | created
const editingId = ref(null)
const created = ref(null)
const saving = ref(false)
const moreId = ref(null)

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
const resetShow = ref(false)
const resetInfo = ref(null) // { username, proxy_password }

let listTimer
let rateTimer

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

async function copy(text) {
  try {
    await copyText(text)
    toast.value = '已复制'
  } catch {
    toast.value = '复制失败，请手动选中'
  }
}

function openEdit(u) {
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
    toast.value = '用户已创建'
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
    toast.value = '已保存'
    show.value = false
    await loadUsers()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function resetPw(id) {
  moreId.value = null
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

async function remove(u) {
  moreId.value = null
  const id = typeof u === 'object' && u ? u.id : u
  const name = typeof u === 'object' && u ? u.username || `#${id}` : `#${id}`
  if (!confirm(`确认删除用户「${name}」？\n\n将从落地 mita 下发配置中移除，不可恢复。`)) return
  try {
    await api(`/api/admin/users/${id}`, { method: 'DELETE' })
    toast.value = `已删除 ${name}`
    await loadUsers()
  } catch (e) {
    error.value = e.message
  }
}

async function toggle(u) {
  moreId.value = null
  const res = await api(`/api/admin/users/${u.id}/toggle`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
  toast.value = res.status === 'disabled' ? '已停用' : '已启用'
  await loadUsers()
}

function openRenew(u) {
  moreId.value = null
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
    toast.value = '已续期'
    renewShow.value = false
    await loadUsers()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

function openAddTraffic(u) {
  moreId.value = null
  trafficUser.value = u
  trafficGB.value = 50
  trafficShow.value = true
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
    toast.value = unlimited ? '已改为不限流量' : `已加 ${trafficGB.value} GB`
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
  moreId.value = null
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
    toast.value = '无用户 ID'
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
    toast.value = `已下载 ${name}`
  } catch (e) {
    if (mihomoYAML.value) {
      const blob = new Blob([mihomoYAML.value], { type: 'application/x-yaml' })
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = `mihomo-${subUser.value?.username || 'user'}.yaml`
      document.body.appendChild(a)
      a.click()
      a.remove()
      toast.value = '已下载 YAML'
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
})
onUnmounted(() => {
  clearInterval(listTimer)
  clearInterval(rateTimer)
})
</script>

<template>
  <div v-if="error && !show && !subShow && !renewShow && !trafficShow && !resetShow" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

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
    <div class="toolbar-left">
      <input class="input-filter" v-model="filter" placeholder="搜索用户 / 备注 / 隧道" />
    </div>
    <button class="btn btn-primary btn-sm" @click="openCreate">开户</button>
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
          <template v-else>未分配隧道，可点右上角「开户」并选择隧道</template>
        </div>
      </header>
      <div class="table-wrap user-group-table">
        <table class="data table-users">
          <thead>
            <tr>
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
              <td class="col-user">
                <div class="name-link">{{ u.username }}</div>
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
                  <div class="more-wrap">
                    <button class="btn btn-link btn-sm" @click="moreId = moreId === u.id ? null : u.id">更多</button>
                    <div v-if="moreId === u.id" class="more-menu" @click.stop>
                      <button @click="openAddTraffic(u); moreId = null">加流量</button>
                      <button @click="resetPw(u.id)">重置密码</button>
                    </div>
                  </div>
                </div>
              </td>
            </tr>
            <tr v-if="!g.users.length">
              <td colspan="7" class="user-group-empty">
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
        <div v-if="error && show" class="error" style="margin:0">{{ error }}</div>
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
            <dt>节点链接</dt>
            <dd style="word-break:break-all" class="mono">{{ created.share_url || '（无可用入口）' }}</dd>
          </div>
          <div class="row-actions" style="margin-top:4px">
            <button class="btn btn-ghost btn-sm" @click="copy(created.proxy_password)">复制密码</button>
            <button class="btn btn-ghost btn-sm" :disabled="!created.share_url" @click="copy(created.share_url)">
              复制节点链接
            </button>
            <button
              class="btn btn-primary btn-sm"
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
.more-wrap { position: relative; display: inline-block; }
.more-menu {
  position: absolute;
  right: 0;
  top: 100%;
  z-index: 20;
  background: #fff;
  border: 1px solid var(--border-line);
  border-radius: 6px;
  min-width: 110px;
  box-shadow: var(--shadow-md);
  padding: 4px 0;
}
.more-menu button {
  display: block;
  width: 100%;
  text-align: left;
  border: 0;
  background: transparent;
  padding: 8px 12px;
  cursor: pointer;
  font-size: 13px;
}
.more-menu button:hover { background: var(--bg-hover); }
.more-menu button.danger { color: var(--danger); }
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
