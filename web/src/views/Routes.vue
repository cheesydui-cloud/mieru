<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '../api'

const routes = ref([])
const nodes = ref([])
const error = ref('')
const toast = ref('')
const show = ref(false)
const saving = ref(false)
const mode = ref('create') // create | edit
const editingId = ref(null)
const probing = ref({}) // id -> true
const probeDetail = ref(null) // last probe result modal

const form = reactive({
  name: '',
  strategy: 'sticky',
  // entry: node | external
  entry_mode: 'node',
  entry_id: '',
  entry_host: '',
  entry_port: 10401,
  entry_name: '',
  relay_id: '',
  exit_id: '',
})

const entries = computed(() => (nodes.value || []).filter((n) => n.role === 'entry' || n.role === 'hybrid'))
const relays = computed(() => (nodes.value || []).filter((n) => n.role === 'relay' || n.role === 'hybrid'))
const exits = computed(() => (nodes.value || []).filter((n) => n.role === 'exit' || n.role === 'hybrid'))
const hasNodes = computed(() => (nodes.value || []).length > 0)
const canSave = computed(() => {
  if (!form.name.trim()) return false
  if (form.entry_mode === 'external') {
    if (!form.entry_host.trim()) return false
  } else if (form.entry_id) {
    // ok
  }
  return !!(form.entry_id || form.entry_mode === 'external' || form.relay_id || form.exit_id)
})

async function load() {
  try {
    const [rs, ns] = await Promise.all([
      api('/api/admin/routes'),
      api('/api/admin/nodes'),
    ])
    routes.value = Array.isArray(rs) ? rs : []
    nodes.value = Array.isArray(ns) ? ns : []
    error.value = ''
  } catch (e) {
    error.value = e.message
    routes.value = []
    nodes.value = []
  }
}

function parseHops(json) {
  try {
    return JSON.parse(json || '[]')
  } catch {
    return []
  }
}

function hopLabel(hop) {
  if (hop.external || (!hop.node_id && hop.host)) {
    const port = hop.port > 0 ? `:${hop.port}` : ''
    const name = hop.name || hop.host || '外部入口'
    return `${name} (${hop.host}${port})`
  }
  const n = (nodes.value || []).find((x) => x.id === hop.node_id)
  return n ? `${n.name} (${n.role})` : hop.node_id || '?'
}

function blankForm() {
  form.name = ''
  form.strategy = 'sticky'
  form.entry_mode = 'node'
  form.entry_id = entries.value[0]?.id || ''
  form.entry_host = ''
  form.entry_port = 10401
  form.entry_name = ''
  form.relay_id = relays.value[0]?.id || ''
  form.exit_id = exits.value[0]?.id || ''
}

function openCreate() {
  blankForm()
  mode.value = 'create'
  editingId.value = null
  error.value = ''
  show.value = true
}

function openEdit(r) {
  blankForm()
  mode.value = 'edit'
  editingId.value = r.id
  form.name = r.name || ''
  form.strategy = r.strategy || 'sticky'
  const hops = parseHops(r.hops_json)

  form.entry_mode = 'node'
  form.entry_id = ''
  form.entry_host = ''
  form.entry_port = 10401
  form.entry_name = ''
  form.relay_id = ''
  form.exit_id = ''

  for (const h of hops) {
    if (h.external || (!h.node_id && h.host)) {
      form.entry_mode = 'external'
      form.entry_host = h.host || ''
      form.entry_port = h.port > 0 ? h.port : 10401
      form.entry_name = h.name || ''
      continue
    }
    if (!h.node_id) continue
    const n = (nodes.value || []).find((x) => x.id === h.node_id)
    const role = n?.role || ''
    const cap = h.capability_type || ''

    if (cap === 'socks_in' || role === 'entry') {
      form.entry_mode = 'node'
      form.entry_id = h.node_id
      continue
    }
    if (cap === 'mieru_client' || role === 'relay') {
      form.relay_id = h.node_id
      continue
    }
    if (cap === 'mita_server' || role === 'exit') {
      form.exit_id = h.node_id
      continue
    }
    if (role === 'hybrid') {
      // first hybrid without entry → entry; else exit
      if (!form.entry_id && form.entry_mode === 'node') {
        form.entry_id = h.node_id
      } else {
        form.exit_id = h.node_id
      }
      continue
    }
    // unknown role: fill in order entry → relay → exit
    if (!form.entry_id && form.entry_mode === 'node') form.entry_id = h.node_id
    else if (!form.relay_id) form.relay_id = h.node_id
    else if (!form.exit_id) form.exit_id = h.node_id
  }
  error.value = ''
  show.value = true
}

function buildHops() {
  const hops = []
  let order = 0
  if (form.entry_mode === 'external') {
    const host = form.entry_host.trim()
    if (host) {
      hops.push({
        external: true,
        host,
        port: Number(form.entry_port) || 0,
        name: form.entry_name.trim() || host,
        order: order++,
        capability_type: 'external_entry',
      })
    }
  } else if (form.entry_id) {
    hops.push({ node_id: form.entry_id, order: order++, capability_type: 'socks_in' })
  }
  if (form.relay_id) {
    hops.push({ node_id: form.relay_id, order: order++, capability_type: 'mieru_client' })
  }
  if (form.exit_id) {
    hops.push({ node_id: form.exit_id, order: order++, capability_type: 'mita_server' })
  }
  return hops
}

async function save() {
  if (!form.name.trim()) {
    error.value = '请填写线路名称'
    return
  }
  if (form.entry_mode === 'external' && !form.entry_host.trim()) {
    error.value = '请填写外部入口的 IP 或域名'
    return
  }
  const hops = buildHops()
  if (!hops.length) {
    error.value = '至少选择一个节点或填写外部入口（建议 Entry → Relay → Exit）'
    return
  }
  saving.value = true
  try {
    const body = {
      name: form.name.trim(),
      strategy: form.strategy,
      enabled: true,
      hops_json: JSON.stringify(hops),
      weight: 100,
    }
    if (mode.value === 'edit' && editingId.value != null) {
      await api(`/api/admin/routes/${editingId.value}`, {
        method: 'PUT',
        body: JSON.stringify(body),
      })
      toast.value = '线路已更新'
    } else {
      await api('/api/admin/routes', {
        method: 'POST',
        body: JSON.stringify(body),
      })
      toast.value = '线路已创建'
    }
    show.value = false
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function remove(id) {
  if (!confirm('删除线路？')) return
  try {
    await api(`/api/admin/routes/${id}`, { method: 'DELETE' })
    toast.value = '已删除'
    await load()
  } catch (e) {
    error.value = e.message
  }
}

function healthLabel(h) {
  const m = {
    ok: '通',
    degraded: '部分通',
    down: '不通',
    unknown: '未测',
  }
  return m[h] || h || '未测'
}

function healthClass(h) {
  if (h === 'ok') return 'badge-ok'
  if (h === 'degraded') return 'badge-warn'
  if (h === 'down') return 'badge-bad'
  return 'badge-muted'
}

async function probe(r) {
  probing.value = { ...probing.value, [r.id]: true }
  error.value = ''
  try {
    const res = await api(`/api/admin/routes/${r.id}/probe`, { method: 'POST' })
    probeDetail.value = res
    const ok = (res.hops || []).filter((x) => x.ok).length
    const total = (res.hops || []).length
    toast.value = `线路 #${r.id} 探测：${ok}/${total} 通 · ${healthLabel(res.health)}`
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    probing.value = { ...probing.value, [r.id]: false }
  }
}

onMounted(load)
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>线路编排</h2>
        <div class="muted" style="font-size: 12px; margin-top: 4px">
          Entry → Relay(mieru) → Exit(mita)；入口可选手动 IP/域名
        </div>
      </div>
      <button class="btn btn-primary btn-sm" @click="openCreate">＋ 新建线路</button>
    </div>
    <div class="panel-bd">
      <table class="data" v-if="routes.length">
        <thead>
          <tr>
            <th>名称</th>
            <th>策略</th>
            <th>Hops</th>
            <th>健康</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in routes" :key="r.id">
            <td>
              <div>{{ r.name }}</div>
              <div class="muted mono" style="font-size:12px">#{{ r.id }}</div>
            </td>
            <td><span class="badge">{{ r.strategy }}</span></td>
            <td>
              <div class="hops">
                <template v-for="(h, i) in parseHops(r.hops_json)" :key="i">
                  <span v-if="i" class="hop-arrow">→</span>
                  <span class="hop" :class="{ external: h.external || (!h.node_id && h.host) }">{{ hopLabel(h) }}</span>
                </template>
                <span v-if="!parseHops(r.hops_json).length" class="muted">无 hops</span>
              </div>
            </td>
            <td>
              <span class="badge" :class="healthClass(r.health)">{{ healthLabel(r.health) }}</span>
            </td>
            <td class="actions-cell">
              <div class="row-actions">
                <button class="btn btn-ghost btn-sm" @click="openEdit(r)">编辑</button>
                <button
                  class="btn btn-ghost btn-sm"
                  :disabled="!!probing[r.id]"
                  @click="probe(r)"
                >
                  {{ probing[r.id] ? '探测中…' : '测通断' }}
                </button>
                <button class="btn btn-danger btn-sm" @click="remove(r.id)">删除</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty" style="padding: 48px 24px; text-align: center">
        <div style="font-size: 16px; margin-bottom: 8px">还没有线路</div>
        <div class="muted" style="margin-bottom: 20px; max-width: 420px; margin-left: auto; margin-right: auto">
          先在「节点」里创建 Relay / Exit；入口可选手动填商家 IP，再串成线路。
        </div>
        <button class="btn btn-primary" @click="openCreate">新建线路</button>
        <div v-if="!hasNodes" class="muted" style="margin-top: 12px; font-size: 12px">
          当前还没有节点，下拉里会是空的 —— 可先去「节点」页添加。
        </div>
      </div>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal" style="max-width: 560px">
      <div class="modal-hd">
        <h3>{{ mode === 'edit' ? '编辑线路' : '新建线路' }}</h3>
        <button class="btn btn-ghost btn-sm" @click="show = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="field">
          <label>名称</label>
          <input v-model="form.name" placeholder="例如：国内入口 → 上海IX → 美国落地" />
        </div>
        <div class="form-grid">
          <div class="field">
            <label>策略</label>
            <select v-model="form.strategy">
              <option value="sticky">sticky（粘性）</option>
              <option value="wrr">wrr（加权轮询）</option>
              <option value="failover">failover（故障切换）</option>
            </select>
          </div>
          <div class="field">
            <label>入口来源</label>
            <select v-model="form.entry_mode">
              <option value="node">从节点选择</option>
              <option value="external">手填 IP / 域名</option>
            </select>
          </div>
        </div>

        <!-- Entry: node -->
        <div v-if="form.entry_mode === 'node'" class="field">
          <label>Entry（入口节点）</label>
          <select v-model="form.entry_id">
            <option value="">— 不选 —</option>
            <option v-for="n in entries" :key="n.id" :value="n.id">{{ n.name }} ({{ n.id }})</option>
          </select>
          <div v-if="!entries.length" class="muted" style="font-size:12px;margin-top:4px">
            无 entry/hybrid 节点。商家入口可改选「手填 IP / 域名」。
          </div>
        </div>

        <!-- Entry: external -->
        <div v-else class="form-grid">
          <div class="field" style="grid-column: 1 / -1">
            <label>入口 IP / 域名</label>
            <input v-model="form.entry_host" placeholder="例如 1.2.3.4 或 entry.example.com" />
          </div>
          <div class="field">
            <label>端口</label>
            <input v-model.number="form.entry_port" type="number" min="1" max="65535" placeholder="10401" />
          </div>
          <div class="field">
            <label>显示名称（可选）</label>
            <input v-model="form.entry_name" placeholder="国内入口" />
          </div>
          <div class="muted" style="grid-column: 1 / -1; font-size:12px; line-height:1.6">
            不装 Agent。订阅会下发此地址；流量由商家 DNAT 到下方 Relay。请保证端口与中转监听一致。
          </div>
        </div>

        <div class="form-grid" style="margin-top: 12px">
          <div class="field">
            <label>Relay（中继 / mieru）</label>
            <select v-model="form.relay_id">
              <option value="">— 不选 —</option>
              <option v-for="n in relays" :key="n.id" :value="n.id">{{ n.name }} ({{ n.id }})</option>
            </select>
          </div>
          <div class="field">
            <label>Exit（落地 / mita）</label>
            <select v-model="form.exit_id">
              <option value="">— 不选 —</option>
              <option v-for="n in exits" :key="n.id" :value="n.id">{{ n.name }} ({{ n.id }})</option>
            </select>
          </div>
        </div>
        <div class="muted" style="font-size:12px;line-height:1.6;margin-top:10px">
          推荐：入口（节点或手填）→ Relay → Exit。保存后 hops 下发给 Agent；手填入口不会出现在「节点」列表。
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="show = false">取消</button>
        <button class="btn btn-primary" :disabled="saving || !canSave" @click="save">
          {{ saving ? '保存中…' : (mode === 'edit' ? '保存修改' : '保存线路') }}
        </button>
      </div>
    </div>
  </div>

  <!-- probe result -->
  <div v-if="probeDetail" class="modal-mask" @click.self="probeDetail = null">
    <div class="modal" style="max-width: 560px">
      <div class="modal-hd">
        <h3>通断探测 · 线路 #{{ probeDetail.route_id }}</h3>
        <button class="btn btn-ghost btn-sm" @click="probeDetail = null">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="kv" style="margin-bottom: 12px">
          <dt>总评</dt>
          <dd>
            <span class="badge" :class="healthClass(probeDetail.health)">{{ healthLabel(probeDetail.health) }}</span>
          </dd>
          <dt>时间</dt>
          <dd class="mono muted">{{ probeDetail.checked_at }}</dd>
        </div>
        <table class="data" v-if="probeDetail.hops?.length">
          <thead>
            <tr>
              <th>跳</th>
              <th>地址</th>
              <th>结果</th>
              <th>延迟</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(h, i) in probeDetail.hops" :key="i">
              <td>
                <div>{{ h.label || '—' }}</div>
                <div class="muted" style="font-size:11px">{{ h.kind }}{{ h.agent_status ? ' · agent ' + h.agent_status : '' }}</div>
              </td>
              <td class="mono">{{ h.host }}:{{ h.port }}</td>
              <td>
                <span class="badge" :class="h.ok ? 'badge-ok' : 'badge-bad'">{{ h.ok ? '通' : '不通' }}</span>
                <div v-if="h.error" class="muted" style="font-size:11px;max-width:180px;word-break:break-all">{{ h.error }}</div>
              </td>
              <td class="mono">{{ h.ok ? h.latency_ms + ' ms' : '—' }}</td>
            </tr>
          </tbody>
        </table>
        <div class="muted" style="font-size:12px;margin-top:12px;line-height:1.6">
          从<strong>面板主机</strong>对每个 hop 的 IP:端口做 TCP 连通检测（不验证 SOCKS 账号）。
          商家入口若仅允许客户端 IP 访问，面板测不通也属正常，以用户端为准。
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-primary" @click="probeDetail = null">知道了</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.hop.external {
  border-style: dashed;
  opacity: 0.95;
}
.actions-cell {
  min-width: 200px;
  white-space: nowrap;
}
.badge-ok {
  background: rgba(52, 211, 153, 0.15);
  color: #34d399;
  border: 1px solid rgba(52, 211, 153, 0.35);
}
.badge-warn {
  background: rgba(251, 191, 36, 0.12);
  color: #fbbf24;
  border: 1px solid rgba(251, 191, 36, 0.35);
}
.badge-bad {
  background: rgba(248, 113, 113, 0.12);
  color: #f87171;
  border: 1px solid rgba(248, 113, 113, 0.35);
}
.badge-muted {
  background: rgba(148, 163, 184, 0.12);
  color: #94a3b8;
  border: 1px solid rgba(148, 163, 184, 0.25);
}
</style>
