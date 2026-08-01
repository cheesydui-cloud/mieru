<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '../api'

const routes = ref([])
const nodes = ref([])
const error = ref('')
const toast = ref('')
const show = ref(false)
const saving = ref(false)
const mode = ref('create')
const editingId = ref(null)
const probing = ref({})
const probeDetail = ref(null)

const form = reactive({
  name: '',
  strategy: 'sticky',
  front_id: '',
  exit_id: '',
})

const fronts = computed(() =>
  (nodes.value || []).filter((n) => n.role === 'relay' || n.role === 'entry' || n.role === 'hybrid'),
)
const exits = computed(() =>
  (nodes.value || []).filter((n) => n.role === 'exit' || n.role === 'hybrid'),
)
const hasNodes = computed(() => (nodes.value || []).length > 0)
const canSave = computed(() => !!(form.name.trim() && (form.front_id || form.exit_id)))

async function load() {
  try {
    const [rs, ns] = await Promise.all([api('/api/admin/routes'), api('/api/admin/nodes')])
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
    const host = hop.host || hop.name || '商家入口'
    return `商家入口 ${host}${port}`
  }
  const n = (nodes.value || []).find((x) => x.id === hop.node_id)
  if (!n) {
    const short = (hop.node_id || '?').slice(0, 12)
    return short
  }
  const kind =
    n.role === 'exit' || n.role === 'hybrid'
      ? '落地'
      : n.role === 'relay' || n.role === 'entry'
        ? '前置'
        : n.role
  return `${n.name}（${kind}）`
}

/** 前置入口端口：手机扫码连这个（API 已按隧道分配） */
function frontPortLabel(r) {
  if (r.front_port > 0) return String(r.front_port)
  return '—'
}

function exitPortLabel(r) {
  if (r.exit_port > 0) return String(r.exit_port)
  return '—'
}

function blankForm() {
  form.name = ''
  form.strategy = 'sticky'
  form.front_id = fronts.value[0]?.id || ''
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
  form.front_id = ''
  form.exit_id = ''
  for (const h of parseHops(r.hops_json)) {
    if (!h.node_id) continue
    const n = (nodes.value || []).find((x) => x.id === h.node_id)
    const role = n?.role || ''
    if (role === 'exit' || role === 'hybrid' || h.capability_type === 'mita_server') {
      form.exit_id = h.node_id
    } else {
      form.front_id = h.node_id
    }
  }
  error.value = ''
  show.value = true
}

function buildHops() {
  const hops = []
  let order = 0
  if (form.front_id) {
    hops.push({
      node_id: form.front_id,
      order: order++,
      capability_type: 'tcp_forward',
    })
  }
  if (form.exit_id) {
    hops.push({
      node_id: form.exit_id,
      order: order++,
      capability_type: 'mita_server',
    })
  }
  return hops
}

async function save() {
  if (!form.name.trim()) {
    error.value = '请填写隧道名称'
    return
  }
  if (!form.front_id && !form.exit_id) {
    error.value = '请选择前置和/或落地'
    return
  }
  if (!form.exit_id) {
    error.value = 'TK 路径需要选择落地（exit）'
    return
  }
  const hops = buildHops()
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
      toast.value = '隧道已更新'
    } else {
      await api('/api/admin/routes', {
        method: 'POST',
        body: JSON.stringify(body),
      })
      toast.value = '隧道已创建'
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
  if (!confirm('删除隧道？')) return
  try {
    await api(`/api/admin/routes/${id}`, { method: 'DELETE' })
    toast.value = '已删除'
    await load()
  } catch (e) {
    error.value = e.message
  }
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

/** Per-hop result badge: skip/external = 不可测 (not a hard fail). */
function hopResultLabel(h) {
  if (!h) return '—'
  if (h.via === 'skip' || h.kind === 'external' || h.kind === 'info') return '不可测'
  return h.ok ? '通' : '不通'
}

function hopResultClass(h) {
  if (!h) return ''
  if (h.via === 'skip' || h.kind === 'external' || h.kind === 'info') return 'warn'
  return h.ok ? 'ok' : 'err'
}

function hopRowLabel(h) {
  if (!h) return '—'
  if (h.label) return h.label
  if (h.from && h.to) return `${h.from} → ${h.to}`
  return h.host || h.name || '—'
}

async function probe(r) {
  probing.value = { ...probing.value, [r.id]: true }
  error.value = ''
  try {
    const res = await api(`/api/admin/routes/${r.id}/probe`, { method: 'POST' })
    probeDetail.value = res
    const ok = (res.hops || []).filter((x) => x.ok).length
    const total = (res.hops || []).length
    toast.value = `探测：${ok}/${total} · ${healthLabel(res.health)}`
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
  <div v-if="error && !show" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

  <div class="page-tabs">
    <div class="page-tab active">隧道</div>
  </div>

  <div class="panel-toolbar">
    <p class="help-text" style="margin:0">
      前置 → 落地 · 每条隧道在前置占一个<strong>入口端口</strong>（扫码连这个）
    </p>
    <button class="btn btn-primary btn-sm" @click="openCreate">新建隧道</button>
  </div>

  <div class="table-wrap">
    <table class="data" v-if="routes.length">
      <thead>
        <tr>
          <th>名称</th>
          <th>路径</th>
          <th>入口端口</th>
          <th>落地端口</th>
          <th>健康</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in routes" :key="r.id">
          <td>
            <div class="name-link">{{ r.name }}</div>
            <div class="muted mono" style="font-size:11px">#{{ r.id }} · {{ r.strategy }}</div>
          </td>
          <td>
            <div class="hops">
              <template v-for="(h, i) in parseHops(r.hops_json)" :key="i">
                <span v-if="i" class="hop-arrow">→</span>
                <span class="hop">{{ hopLabel(h) }}</span>
              </template>
              <span v-if="!parseHops(r.hops_json).length" class="muted">无 hops</span>
            </div>
          </td>
          <td>
            <div class="mono" style="font-weight:600">{{ frontPortLabel(r) }}</div>
            <div v-if="r.front_host" class="muted mono" style="font-size:11px">{{ r.front_host }}</div>
          </td>
          <td>
            <div class="mono">{{ exitPortLabel(r) }}</div>
            <div v-if="r.exit_name" class="muted" style="font-size:11px">{{ r.exit_name }}</div>
          </td>
          <td>
            <span class="badge" :class="healthClass(r.health)">{{ healthLabel(r.health) }}</span>
          </td>
          <td>
            <div class="row-actions">
              <button class="btn btn-link btn-sm" @click="openEdit(r)">编辑</button>
              <button class="btn btn-link btn-sm" :disabled="!!probing[r.id]" @click="probe(r)">
                {{ probing[r.id] ? '探测中…' : '测通断' }}
              </button>
              <button class="btn btn-link-danger btn-sm" @click="remove(r.id)">删除</button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty">
      <div style="margin-bottom:8px;font-weight:600">还没有隧道</div>
      <div class="muted" style="margin-bottom:16px">选一个前置 + 一个落地，手机扫码走前置出口家宽。</div>
      <button class="btn btn-primary" @click="openCreate">新建隧道</button>
      <div v-if="!hasNodes" class="muted" style="margin-top:12px;font-size:12px">请先在「节点」创建前置和落地</div>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal" style="max-width:520px">
      <div class="modal-hd">
        <h3>{{ mode === 'edit' ? '编辑隧道' : '新建隧道' }}</h3>
        <button class="btn btn-ghost btn-sm" @click="show = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div v-if="error && show" class="error" style="margin:0">{{ error }}</div>
        <div class="field">
          <label>名称</label>
          <input v-model="form.name" />
        </div>
        <div class="form-grid">
          <div class="field">
            <label>前置</label>
            <select v-model="form.front_id">
              <option value="">（不选）</option>
              <option v-for="n in fronts" :key="n.id" :value="n.id">
                {{ n.name }} · {{ n.role }} · {{ n.public_ip || n.hostname || n.id }}
              </option>
            </select>
          </div>
          <div class="field">
            <label>落地</label>
            <select v-model="form.exit_id">
              <option value="">（必选）</option>
              <option v-for="n in exits" :key="n.id" :value="n.id">
                {{ n.name }} · {{ n.role }} · {{ n.public_ip || n.hostname || n.id }}
              </option>
            </select>
          </div>
        </div>
        <div class="field">
          <label>策略</label>
          <select v-model="form.strategy">
            <option value="sticky">sticky</option>
            <option value="failover">failover</option>
            <option value="wrr">wrr</option>
          </select>
        </div>
        <p class="help-text">
          保存后请在节点页点「重建配置」。前置会为每条隧道分配入口端口并 tcp_forward → 落地 mita。
        </p>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="show = false">取消</button>
        <button class="btn btn-primary" :disabled="saving || !canSave" @click="save">
          {{ saving ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>
  </div>

  <div v-if="probeDetail" class="modal-mask" @click.self="probeDetail = null">
    <div class="modal" style="max-width:560px">
      <div class="modal-hd">
        <h3>探测结果 · {{ healthLabel(probeDetail.health) }}</h3>
        <button class="btn btn-ghost btn-sm" @click="probeDetail = null">关闭</button>
      </div>
      <div class="modal-bd">
        <p class="help-text" style="margin:0 0 8px">
          {{ probeDetail.note || '关键看「前置→落地」。商家公网入口无 Agent 时显示「不可测」属正常。' }}
        </p>
        <table class="data" v-if="(probeDetail.hops || []).length">
          <thead>
            <tr>
              <th>跳</th>
              <th>结果</th>
              <th>延迟</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(h, i) in probeDetail.hops" :key="i">
              <td style="font-size:12px">
                <div>{{ hopRowLabel(h) }}</div>
                <div class="muted mono" style="font-size:11px" v-if="h.host">
                  {{ h.host }}{{ h.port ? ':' + h.port : '' }}
                </div>
              </td>
              <td>
                <span class="badge" :class="hopResultClass(h)">{{ hopResultLabel(h) }}</span>
              </td>
              <td class="mono">
                {{
                  h.via === 'skip' || h.kind === 'external'
                    ? '—'
                    : h.latency_ms != null
                      ? h.latency_ms + 'ms'
                      : '—'
                }}
              </td>
              <td class="muted" style="font-size:12px;max-width:240px;line-height:1.4">
                {{ h.error || h.detail || '—' }}
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else class="empty">无 hop 结果</div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-primary" @click="probeDetail = null">知道了</button>
      </div>
    </div>
  </div>
</template>
