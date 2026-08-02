<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, copyText, statusBadge } from '../api'

const route = useRoute()
const router = useRouter()

const nodes = ref([])
const filter = ref('')
const tab = ref('all') // all | front | exit
const error = ref('')
const toast = ref('')
const show = ref(false)
const mode = ref('create') // create | edit | created
const installShow = ref(false)
const installInfo = ref(null)
const created = ref(null)
const editingId = ref('')
const saving = ref(false)

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

function openCreate() {
  blankForm()
  created.value = null
  editingId.value = ''
  mode.value = 'create'
  show.value = true
}

function openEdit(n) {
  fillForm(n)
  created.value = null
  editingId.value = n.id
  mode.value = 'edit'
  show.value = true
  error.value = ''
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
    toast.value = `已创建：${res.node.name}`
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
    toast.value = '已更新，配置已自动下发（Agent 心跳后生效）'
    show.value = false
    await load()
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
  // Only show actionable / in-progress states.
  // "已升至 vX" is redundant with the Agent column version.
  const st = n.upgrade_status || ''
  if (st === 'error' && n.upgrade_error) return n.upgrade_error
  if (st === 'pending' || n.upgrade_pending) {
    return n.upgrade_target ? `排队 → v${n.upgrade_target}` : '升级排队中'
  }
  if (st === 'running') {
    return n.upgrade_target ? `升级中 → v${n.upgrade_target}` : '升级中…'
  }
  if (n.config_stale) {
    return `配置未生效 面板v${n.config_version}/Agent v${n.agent_config_version || '?'}`
  }
  return ''
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
    toast.value = res.message || `已推送升级 → ${res.version || ''}`
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    const next = { ...upgrading.value }
    delete next[n.id]
    upgrading.value = next
  }
}

async function pushUpgradeAll() {
  if (!confirm('向所有在线节点推送 Agent 升级到面板版本？')) return
  try {
    const res = await api('/api/admin/nodes/upgrade-all', { method: 'POST' })
    toast.value = res.message || '已推送'
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
    toast.value = parts.join(' · ') + '；Agent 将停用'
    await load()
  } catch (e) {
    error.value = e.message
  }
}

async function rebuild() {
  await api('/api/admin/rebuild', { method: 'POST' })
  toast.value = '已重建全部节点配置'
  await load()
}

async function copy(text) {
  try {
    await copyText(text)
    toast.value = '已复制'
  } catch {
    toast.value = '复制失败，请手动选中'
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
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<template>
  <div v-if="error && !show" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

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
    <input class="input-filter" v-model="filter" />
    <div class="row-actions">
      <button class="btn btn-ghost btn-sm" @click="pushUpgradeAll" title="向所有在线节点推送 Agent 升级">全部升级 Agent</button>
      <button
        class="btn btn-ghost btn-sm"
        @click="rebuild"
        title="应急：强制重新生成并 bump 全部节点配置。面板启动/升级、开户改隧道、到期超流已自动重建"
      >
        重建配置
      </button>
      <button class="btn btn-primary btn-sm" @click="openCreate">新增节点</button>
    </div>
  </div>
  <p class="help-text" style="margin-top:-6px">
    <strong>前置</strong> = 商家入口（端口<strong>段</strong>，如 10401–10499，每条隧道占一个）·
    <strong>落地</strong> = 家宽 mita（单端口）。
    点<strong>升级</strong>可远程推送 Agent。
  </p>

  <div class="table-wrap">
    <table class="data" v-if="tabNodes.length">
      <thead>
        <tr>
          <th>名称</th>
          <th>类型</th>
          <th>状态</th>
          <th>公网 / 接入</th>
          <th>端口 / 池</th>
          <th>区域</th>
          <th>Agent</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="n in tabNodes" :key="n.id" :class="{ 'row-warn': n.no_heartbeat || n.config_stale }">
          <td>
            <div class="name-link">{{ n.name }}</div>
            <div class="muted mono" style="font-size:11px">{{ n.id }}</div>
            <div v-if="n.apply_error" class="apply-err" :title="n.apply_error">
              {{ n.apply_error }}
            </div>
            <div v-if="upgradeRowHint(n)" class="apply-err" :title="upgradeRowHint(n)">
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
              class="muted"
              style="font-size:11px;margin-top:3px"
              :style="n.no_heartbeat ? { color: 'var(--danger)', fontWeight: 600 } : {}"
            >
              {{ heartbeatLabel(n) }}
            </div>
            <div
              v-if="n.role === 'exit' || n.role === 'hybrid'"
              class="muted"
              :title="n.metering_hint || ''"
              :style="{
                fontSize: '11px',
                marginTop: '2px',
                color: n.traffic_reporting ? 'var(--success)' : 'var(--warning)',
              }"
            >
              {{ meteringLabel(n) }}
            </div>
          </td>
          <td class="mono" style="font-size:12px">
            <div>{{ n.public_ip || '—' }}</div>
            <div class="muted" v-if="n.hostname">{{ n.hostname }}</div>
            <div class="muted" v-if="n.private_ip">内网 {{ n.private_ip }}</div>
          </td>
          <td class="mono">{{ portLabel(n) }}</td>
          <td>{{ n.region || '—' }}</td>
          <td class="mono" style="font-size:12px">
            <div>
              {{ n.agent_version ? 'v' + n.agent_version : '—' }}
              <span
                v-if="needsUpgrade(n) && n.agent_version"
                class="badge"
                style="margin-left:4px;font-size:10px;background:#fef3c7;color:#92400e"
                title="面板版本更新"
              >可升</span>
            </div>
            <div v-if="n.config_stale" class="muted" style="font-size:11px;color:var(--warning)">
              配置未生效
            </div>
          </td>
          <td>
            <div class="row-actions">
              <button class="btn btn-link btn-sm" @click="openEdit(n)">编辑</button>
              <button
                class="btn btn-link btn-sm"
                :disabled="upgradeBusy(n) || n.status === 'offline'"
                :title="
                  n.upgrade_error ||
                  (n.status === 'offline' ? '节点离线，无法推送' : '远程升级到面板同版本')
                "
                @click="pushUpgrade(n)"
              >
                {{ upgradeLabel(n) }}
              </button>
              <button class="btn btn-link btn-sm" @click="showInstall(n.id)">安装+防火墙</button>
              <button class="btn btn-link-danger btn-sm" @click="remove(n)">删除</button>
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
        <div v-if="error && show" class="error" style="margin:0">{{ error }}</div>
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
              <input v-model="form.hostname" placeholder="可选" />
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
            <textarea
              readonly
              rows="4"
              class="mono"
              style="width:100%;resize:vertical;background:var(--bg-elevated);border:1px solid var(--border-line);border-radius:6px;padding:12px"
              :value="created.install_cmd"
            />
          </div>
          <div class="row-actions">
            <button class="btn btn-primary btn-sm" @click="copy(created.install_cmd)">复制安装命令</button>
            <button class="btn btn-ghost btn-sm" @click="copy(created.agent_token)">复制 Token</button>
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
          <label>安装 + 防火墙（整段复制到目标机执行）</label>
          <textarea
            readonly
            rows="8"
            class="mono"
            style="width:100%;resize:vertical;background:var(--bg-elevated);border:1px solid var(--border-line);border-radius:6px;padding:12px"
            :value="installInfo.install_cmd"
          />
          <p class="help-text" style="margin-top:8px">
            {{ installInfo.hint || '先放行端口，再装 Agent；装完回来看节点是否在线。' }}
          </p>
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="installShow = false">关闭</button>
        <button class="btn btn-primary" @click="copy(installInfo.install_cmd)">复制安装+防火墙</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.apply-err {
  margin-top: 4px;
  max-width: 280px;
  font-size: 11px;
  line-height: 1.35;
  color: #92400e;
  background: #fffbeb;
  border: 1px solid #f59e0b;
  border-radius: 4px;
  padding: 3px 7px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  cursor: help;
}
.row-warn td {
  background: #fffbeb33;
}
</style>
