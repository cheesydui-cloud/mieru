<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, statusBadge } from '../api'

const router = useRouter()
const diag = ref(null)
const routes = ref([])
const error = ref('')
const toast = ref('')
const rebuilding = ref(false)
const rebuildStatus = ref(null)
let timer

async function load() {
  try {
    const [d, rs, rb] = await Promise.all([
      api('/api/admin/diagnose'),
      api('/api/admin/routes'),
      api('/api/admin/rebuild-status').catch(() => null),
    ])
    diag.value = d
    routes.value = Array.isArray(rs) ? rs : []
    rebuildStatus.value = d?.rebuild || rb || null
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

function fmtRebuild(rb) {
  if (!rb || !rb.at) return '尚未记录'
  const age = typeof rb.age_sec === 'number' ? rb.age_sec : null
  let when = rb.at
  try {
    when = new Date(rb.at).toLocaleString()
  } catch {
    /* keep */
  }
  const ageTxt = age != null ? (age < 60 ? `${age}s 前` : `${Math.round(age / 60)} 分前`) : ''
  const st = rb.ok === false ? '失败' : '成功'
  const reason = rb.reason ? ` · ${rb.reason}` : ''
  return `${st} · ${when}${ageTxt ? ' · ' + ageTxt : ''}${reason}`
}

const nodes = computed(() => diag.value?.nodes || [])
const fronts = computed(() =>
  nodes.value.filter((n) => n.role === 'relay' || n.role === 'entry'),
)
const exits = computed(() =>
  nodes.value.filter((n) => n.role === 'exit' || n.role === 'hybrid'),
)
const tunnelEdges = computed(() => diag.value?.tunnel_edges || [])
const stats = computed(() => diag.value?.stats || {})

const onlineCount = computed(() => nodes.value.filter((n) => n.status === 'online').length)
const issueCount = computed(() => {
  let n = (diag.value?.global_issue_items || diag.value?.global_issues || []).length
  for (const node of nodes.value) n += (node.issue_items || node.issues || []).length
  return n
})

const allIssueItems = computed(() => {
  const items = []
  for (const it of diag.value?.global_issue_items || []) {
    items.push(it)
  }
  // fallback for plain strings
  if (!items.length) {
    for (const t of diag.value?.global_issues || []) {
      items.push({ text: t, href: '/', kind: 'global' })
    }
  }
  for (const node of nodes.value) {
    if (node.issue_items?.length) {
      for (const it of node.issue_items) {
        items.push({
          ...it,
          text: `[${node.name}] ${it.text}`,
        })
      }
    } else {
      for (const t of node.issues || []) {
        const href =
          node.role === 'exit' || node.role === 'hybrid'
            ? '/nodes?tab=exit'
            : node.role === 'relay' || node.role === 'entry'
              ? '/nodes?tab=front'
              : '/nodes'
        items.push({ text: `[${node.name}] ${t}`, href, kind: 'node', node_id: node.id })
      }
    }
  }
  return items
})

function nodeTone(n) {
  if ((n.issues || []).length) return n.status === 'online' ? 'warn' : 'err'
  if (n.status === 'online') return 'ok'
  if (n.status === 'degraded') return 'warn'
  return 'err'
}

function statusLabel(s) {
  if (s === 'online') return '在线'
  if (s === 'degraded') return '异常'
  if (s === 'offline') return '离线'
  return s || '未知'
}

function pluginSummary(n) {
  const ps = n.plugins || []
  if (!ps.length) return '—'
  return ps
    .map((p) => {
      const t = p.type || '?'
      if (t === 'tcp_forward') {
        return `tcp_forward :${p.listen_port || '?'} → ${p.target_host || p.exit_id || '?'}:${p.target_port || '?'}`
      }
      if (t === 'mita_server') return `mita :${p.listen_port || p.port_min || '?'}`
      if (t === 'socks_in') return `socks :${p.listen_port || '?'}`
      if (t === 'mieru_client') return `mieru → ${p.server || '?'}:${p.port || '?'}`
      return t
    })
    .join(' · ')
}

function healthLabel(h) {
  const m = { ok: '通', degraded: '部分通', down: '不通', unknown: '未测' }
  return m[h] || h || '未测'
}

function healthClass(h) {
  if (h === 'ok') return 'ok'
  if (h === 'degraded') return 'warn'
  if (h === 'down') return 'err'
  return ''
}

function edgeTone(e) {
  if (e.health === 'ok') return 'ok'
  if (e.health === 'degraded') return 'warn'
  if (e.health === 'down') return 'err'
  return ''
}

function goIssue(it) {
  if (it?.href) router.push(it.href)
}

function goRoute(id) {
  router.push('/routes')
}

async function rebuild() {
  rebuilding.value = true
  try {
    const res = await api('/api/admin/rebuild', { method: 'POST' })
    rebuildStatus.value = res?.rebuild || rebuildStatus.value
    toast.value = '已重建全部节点配置'
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    rebuilding.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 8000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

  <div class="page-tabs">
    <div class="page-tab active">拓扑健康</div>
  </div>

  <div class="grid-stats" v-if="diag">
    <div class="card" :class="onlineCount === nodes.length && nodes.length ? 'card-ok' : ''">
      <h3>节点在线</h3>
      <div class="value">{{ onlineCount }}<span class="slash"> / {{ nodes.length }}</span></div>
    </div>
    <div class="card">
      <h3>前置 / 落地</h3>
      <div class="value">{{ fronts.length }}<span class="slash"> / {{ exits.length }}</span></div>
      <div class="sub">relay·entry / exit·hybrid</div>
    </div>
    <div class="card">
      <h3>启用隧道</h3>
      <div class="value">{{ diag.enabled_routes || 0 }}</div>
    </div>
    <div class="card" :class="issueCount ? 'card-warn' : 'card-ok'">
      <h3>待处理问题</h3>
      <div class="value">{{ issueCount }}</div>
    </div>
  </div>

  <div class="grid-stats" v-if="diag" style="grid-template-columns: repeat(3, 1fr)">
    <div
      class="card clickable"
      :class="stats.agent_behind ? 'card-warn' : 'card-ok'"
      @click="router.push('/nodes')"
    >
      <h3>Agent 版本落后</h3>
      <div class="value">{{ stats.agent_behind || 0 }}</div>
      <div class="sub">面板 {{ stats.panel_version || diag.version || '—' }}</div>
    </div>
    <div
      class="card clickable"
      :class="stats.config_stale ? 'card-warn' : 'card-ok'"
      @click="router.push('/nodes')"
    >
      <h3>配置未生效</h3>
      <div class="value">{{ stats.config_stale || 0 }}</div>
      <div class="sub">desired &gt; agent applied</div>
    </div>
    <div
      class="card clickable"
      :class="stats.traffic_silent ? 'card-warn' : 'card-ok'"
      @click="router.push({ path: '/nodes', query: { tab: 'exit' } })"
    >
      <h3>流量上报沉默落地</h3>
      <div class="value">{{ stats.traffic_silent || 0 }}</div>
      <div class="sub">exit 近期无 /api/agent/traffic</div>
    </div>
  </div>

  <div class="grid-stats" v-if="diag" style="grid-template-columns: repeat(4, 1fr)">
    <div
      class="card clickable"
      :class="stats.expiring_soon ? 'card-warn' : ''"
      @click="router.push('/users')"
    >
      <h3>3 天内到期</h3>
      <div class="value">{{ stats.expiring_soon || 0 }}</div>
      <div class="sub">点此去用户续期</div>
    </div>
    <div
      class="card clickable"
      :class="stats.over_quota ? 'card-warn' : ''"
      @click="router.push('/users')"
    >
      <h3>已超流量</h3>
      <div class="value">{{ stats.over_quota || 0 }}</div>
      <div class="sub">需加流量或停用</div>
    </div>
    <div class="card clickable" @click="router.push('/users')">
      <h3>已到期用户</h3>
      <div class="value">{{ stats.expired_users || 0 }}</div>
      <div class="sub">状态 expired</div>
    </div>
    <div
      class="card"
      :class="rebuildStatus && rebuildStatus.ok === false ? 'card-warn' : rebuildStatus?.at ? 'card-ok' : ''"
    >
      <h3>最近重建</h3>
      <div class="value" style="font-size:14px;line-height:1.35;margin-top:4px">
        {{ fmtRebuild(rebuildStatus) }}
      </div>
      <div class="sub" v-if="rebuildStatus?.error" style="color:var(--danger)">{{ rebuildStatus.error }}</div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>隧道拓扑</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          {{ diag?.topology_hint || '手机 → 前置 → 落地 mita → 家宽' }}
          · 按真实隧道画边，不是堆全部节点
        </div>
      </div>
      <div class="row-actions">
        <button class="btn btn-ghost btn-sm" @click="load">刷新</button>
        <button
          class="btn btn-ghost btn-sm"
          :disabled="rebuilding"
          @click="rebuild"
          title="应急手动重建；平时改配置/升级已自动下发"
        >
          {{ rebuilding ? '重建中…' : '重建配置' }}
        </button>
      </div>
    </div>

    <div v-if="tunnelEdges.length" class="topo-edges">
      <div
        v-for="e in tunnelEdges"
        :key="e.route_id"
        class="topo-edge"
        :class="edgeTone(e)"
        @click="goRoute(e.route_id)"
      >
        <div class="te-name">{{ e.name }}</div>
        <div class="te-path mono">
          <span>{{ e.front_name || '前置' }}</span>
          <span class="muted">
            {{ e.front_host || '?' }}{{ e.front_port ? ':' + e.front_port : '' }}
          </span>
          <span class="te-arrow">→</span>
          <span>{{ e.exit_name || '落地' }}</span>
          <span class="muted">mita {{ e.exit_port || '?' }}</span>
        </div>
        <div class="te-meta">
          <span class="badge" :class="healthClass(e.health)">{{ healthLabel(e.health) }}</span>
          <span class="muted">用户 {{ e.user_count || 0 }}</span>
        </div>
      </div>
    </div>
    <div v-else class="empty">
      还没有启用隧道。先建<strong>前置</strong>和<strong>落地</strong>，再绑隧道。
      <div style="margin-top:12px" class="row-actions">
        <button class="btn btn-primary btn-sm" @click="router.push('/nodes')">去节点</button>
        <button class="btn btn-ghost btn-sm" @click="router.push('/routes')">去隧道</button>
      </div>
    </div>

    <ul v-if="allIssueItems.length" class="issue-list">
      <li
        v-for="(iss, i) in allIssueItems"
        :key="'i' + i"
        class="issue-click"
        @click="goIssue(iss)"
      >
        {{ iss.text }}
        <span v-if="iss.href" class="muted" style="margin-left:6px">→</span>
      </li>
    </ul>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <h2>节点明细</h2>
      <span class="muted" style="font-size:12px">8s 自动刷新</span>
    </div>
    <div class="panel-bd">
      <table class="data" v-if="nodes.length">
        <thead>
          <tr>
            <th>名称</th>
            <th>角色</th>
            <th>状态</th>
            <th>地址 / 端口</th>
            <th>Agent / 配置</th>
            <th>计量</th>
            <th>问题</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="n in nodes" :key="n.id">
            <td>
              <div class="name-link">{{ n.name }}</div>
              <div class="muted mono" style="font-size:11px">{{ n.id }}</div>
            </td>
            <td><span class="badge">{{ n.role }}</span></td>
            <td>
              <span class="badge" :class="statusBadge(n.status)">
                <span class="dot"></span>{{ statusLabel(n.status) }}
              </span>
              <div
                v-if="n.no_heartbeat"
                class="muted"
                style="font-size:11px;color:var(--danger);margin-top:2px"
              >
                未心跳/超时
              </div>
            </td>
            <td class="mono" style="font-size:12px">
              {{ n.dial_host || n.public_ip || '—' }}
              <span v-if="n.public_port">:{{ n.public_port }}</span>
              <span v-if="n.mita_port" class="muted"> · mita {{ n.mita_port }}</span>
            </td>
            <td class="mono" style="font-size:12px">
              <div>{{ n.agent_version ? 'v' + n.agent_version : '—' }}</div>
              <div class="muted" style="font-size:11px">
                cfg v{{ n.config_version
                }}<template v-if="n.agent_config_version"> / 已应用 v{{ n.agent_config_version }}</template>
                <span v-if="n.config_stale" style="color:var(--warning)"> · 未生效</span>
                <span v-if="n.version_behind" style="color:var(--warning)"> · 版本落后</span>
              </div>
            </td>
            <td style="font-size:12px">
              <template v-if="n.role === 'exit' || n.role === 'hybrid'">
                <span :style="{ color: n.traffic_reporting ? 'var(--success)' : 'var(--warning)' }">
                  {{ n.metering_hint || (n.traffic_reporting ? '计量正常' : '未上报') }}
                </span>
              </template>
              <span v-else class="muted">—</span>
            </td>
            <td>
              <template v-if="(n.issues || []).length">
                <div
                  v-for="(iss, i) in n.issues"
                  :key="i"
                  class="issue-click muted"
                  style="font-size:11px;color:var(--danger);line-height:1.4"
                  @click="
                    router.push(
                      n.role === 'exit' || n.role === 'hybrid'
                        ? { path: '/nodes', query: { tab: 'exit' } }
                        : { path: '/nodes', query: { tab: 'front' } },
                    )
                  "
                >
                  {{ iss }}
                </div>
              </template>
              <span v-else class="badge ok">正常</span>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty">暂无节点</div>
    </div>
  </div>

  <div class="panel" v-if="routes.length">
    <div class="panel-hd">
      <h2>隧道</h2>
      <button class="btn btn-ghost btn-sm" @click="router.push('/routes')">管理隧道</button>
    </div>
    <div class="panel-bd">
      <table class="data">
        <thead>
          <tr>
            <th>名称</th>
            <th>路径</th>
            <th>健康</th>
            <th>用户</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in routes" :key="r.id" class="route-row" @click="router.push('/routes')">
            <td class="name-link">{{ r.name }}</td>
            <td class="mono" style="font-size:12px">
              {{ r.path_summary || (r.front_host && r.front_port ? r.front_host + ':' + r.front_port : '—') }}
            </td>
            <td>
              <span class="badge" :class="healthClass(r.last_probe_health || r.health)">
                {{ healthLabel(r.last_probe_health || r.health) }}
              </span>
            </td>
            <td class="num">{{ r.user_count || 0 }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
