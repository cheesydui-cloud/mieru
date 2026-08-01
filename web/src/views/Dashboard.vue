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
let timer

async function load() {
  try {
    const [d, rs] = await Promise.all([
      api('/api/admin/diagnose'),
      api('/api/admin/routes'),
    ])
    diag.value = d
    routes.value = Array.isArray(rs) ? rs : []
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

const nodes = computed(() => diag.value?.nodes || [])
const fronts = computed(() =>
  nodes.value.filter((n) => n.role === 'relay' || n.role === 'entry'),
)
const exits = computed(() =>
  nodes.value.filter((n) => n.role === 'exit' || n.role === 'hybrid'),
)

const onlineCount = computed(() => nodes.value.filter((n) => n.status === 'online').length)
const issueCount = computed(() => {
  let n = (diag.value?.global_issues || []).length
  for (const node of nodes.value) n += (node.issues || []).length
  return n
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

async function rebuild() {
  rebuilding.value = true
  try {
    await api('/api/admin/rebuild', { method: 'POST' })
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
      <h3>启用线路</h3>
      <div class="value">{{ diag.enabled_routes || 0 }}</div>
    </div>
    <div class="card" :class="issueCount ? 'card-warn' : 'card-ok'">
      <h3>待处理问题</h3>
      <div class="value">{{ issueCount }}</div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>链路示意</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          {{ diag?.topology_hint || '手机 → 前置 → 落地 mita → 家宽' }}
        </div>
      </div>
      <div class="row-actions">
        <button class="btn btn-ghost btn-sm" @click="load">刷新</button>
        <button class="btn btn-primary btn-sm" :disabled="rebuilding" @click="rebuild">
          {{ rebuilding ? '重建中…' : '重建配置' }}
        </button>
      </div>
    </div>
    <div class="topo-chain" v-if="fronts.length || exits.length">
      <template v-if="fronts.length">
        <div
          v-for="n in fronts"
          :key="'f' + n.id"
          class="topo-node"
          :class="nodeTone(n)"
        >
          <div class="tn-role">前置 · {{ n.role }}</div>
          <div class="tn-name">{{ n.name }}</div>
          <div class="tn-meta">{{ n.public_ip || n.dial_host || '—' }}:{{ n.public_port || '?' }}</div>
          <div class="tn-meta" style="margin-top:4px">{{ statusLabel(n.status) }}</div>
        </div>
      </template>
      <div v-else class="topo-node err">
        <div class="tn-role">前置</div>
        <div class="tn-name">未配置</div>
        <div class="tn-meta">去「节点」建 relay</div>
      </div>
      <div class="topo-arrow">→</div>
      <template v-if="exits.length">
        <div
          v-for="n in exits"
          :key="'e' + n.id"
          class="topo-node"
          :class="nodeTone(n)"
        >
          <div class="tn-role">落地 · {{ n.role }}</div>
          <div class="tn-name">{{ n.name }}</div>
          <div class="tn-meta">{{ n.dial_host || n.public_ip || '—' }}:{{ n.mita_port || n.public_port || '?' }}</div>
          <div class="tn-meta" style="margin-top:4px">{{ statusLabel(n.status) }} · 用户 {{ n.user_count }}</div>
        </div>
      </template>
      <div v-else class="topo-node err">
        <div class="tn-role">落地</div>
        <div class="tn-name">未配置</div>
        <div class="tn-meta">去「节点」建 exit</div>
      </div>
      <div class="topo-arrow">→</div>
      <div class="topo-node ok">
        <div class="tn-role">出口</div>
        <div class="tn-name">家宽 / 互联网</div>
        <div class="tn-meta">TK 等业务流量</div>
      </div>
    </div>
    <div v-else class="empty">
      还没有节点。先建<strong>前置 (relay)</strong>和<strong>落地 (exit)</strong>，再绑线路。
      <div style="margin-top:12px">
        <button class="btn btn-primary btn-sm" @click="router.push('/nodes')">去节点</button>
      </div>
    </div>
    <ul v-if="(diag?.global_issues || []).length" class="issue-list">
      <li v-for="(iss, i) in diag.global_issues" :key="'g' + i">{{ iss }}</li>
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
            <th>插件</th>
            <th>配置</th>
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
            </td>
            <td class="mono" style="font-size:12px">
              {{ n.dial_host || n.public_ip || '—' }}
              <span v-if="n.public_port">:{{ n.public_port }}</span>
              <span v-if="n.mita_port" class="muted"> · mita {{ n.mita_port }}</span>
            </td>
            <td style="font-size:12px;max-width:280px">{{ pluginSummary(n) }}</td>
            <td class="num">v{{ n.config_version }}</td>
            <td>
              <template v-if="(n.issues || []).length">
                <div
                  v-for="(iss, i) in n.issues"
                  :key="i"
                  class="muted"
                  style="font-size:11px;color:var(--danger);line-height:1.4"
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
      <h2>线路</h2>
      <button class="btn btn-ghost btn-sm" @click="router.push('/routes')">管理线路</button>
    </div>
    <div class="panel-bd">
      <table class="data">
        <thead>
          <tr>
            <th>名称</th>
            <th>健康</th>
            <th>策略</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in routes" :key="r.id">
            <td class="name-link">{{ r.name }}</td>
            <td>
              <span class="badge" :class="healthClass(r.health)">{{ healthLabel(r.health) }}</span>
            </td>
            <td><span class="badge">{{ r.strategy }}</span></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
