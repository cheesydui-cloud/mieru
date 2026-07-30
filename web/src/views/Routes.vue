<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '../api'

const routes = ref([])
const nodes = ref([])
const error = ref('')
const toast = ref('')
const show = ref(false)
const saving = ref(false)
const form = reactive({
  name: '',
  strategy: 'sticky',
  entry_id: '',
  relay_id: '',
  exit_id: '',
})

const entries = computed(() => (nodes.value || []).filter((n) => n.role === 'entry' || n.role === 'hybrid'))
const relays = computed(() => (nodes.value || []).filter((n) => n.role === 'relay' || n.role === 'hybrid'))
const exits = computed(() => (nodes.value || []).filter((n) => n.role === 'exit' || n.role === 'hybrid'))
const hasNodes = computed(() => (nodes.value || []).length > 0)
const canSave = computed(() => form.name.trim() && (form.entry_id || form.relay_id || form.exit_id))

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
  const n = (nodes.value || []).find((x) => x.id === hop.node_id)
  return n ? `${n.name} (${n.role})` : hop.node_id
}

function openCreate() {
  form.name = ''
  form.strategy = 'sticky'
  form.entry_id = entries.value[0]?.id || ''
  form.relay_id = relays.value[0]?.id || ''
  form.exit_id = exits.value[0]?.id || ''
  error.value = ''
  show.value = true
}

async function create() {
  if (!form.name.trim()) {
    error.value = '请填写线路名称'
    return
  }
  const hops = []
  if (form.entry_id) hops.push({ node_id: form.entry_id, order: 0, capability_type: 'socks_in' })
  if (form.relay_id) hops.push({ node_id: form.relay_id, order: 1, capability_type: 'mieru_client' })
  if (form.exit_id) hops.push({ node_id: form.exit_id, order: 2, capability_type: 'mita_server' })
  if (!hops.length) {
    error.value = '至少选择一个节点（建议 Entry → Relay → Exit）'
    return
  }
  saving.value = true
  try {
    await api('/api/admin/routes', {
      method: 'POST',
      body: JSON.stringify({
        name: form.name.trim(),
        strategy: form.strategy,
        enabled: true,
        hops_json: JSON.stringify(hops),
        weight: 100,
      }),
    })
    show.value = false
    toast.value = '线路已创建'
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
          Entry → Relay(mieru) → Exit(mita)
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
                  <span class="hop">{{ hopLabel(h) }}</span>
                </template>
                <span v-if="!parseHops(r.hops_json).length" class="muted">无 hops</span>
              </div>
            </td>
            <td>{{ r.health || '—' }}</td>
            <td>
              <button class="btn btn-danger btn-sm" @click="remove(r.id)">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty" style="padding: 48px 24px; text-align: center">
        <div style="font-size: 16px; margin-bottom: 8px">还没有线路</div>
        <div class="muted" style="margin-bottom: 20px; max-width: 420px; margin-left: auto; margin-right: auto">
          先在「节点」里创建 Entry / Relay / Exit，再把它们串成一条线路。
        </div>
        <button class="btn btn-primary" @click="openCreate">新建线路</button>
        <div v-if="!hasNodes" class="muted" style="margin-top: 12px; font-size: 12px">
          当前还没有节点，下拉里会是空的 —— 可先去「节点」页添加。
        </div>
      </div>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal">
      <div class="modal-hd">
        <h3>新建线路</h3>
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
            <label>Entry（入口）</label>
            <select v-model="form.entry_id">
              <option value="">— 不选 —</option>
              <option v-for="n in entries" :key="n.id" :value="n.id">{{ n.name }} ({{ n.id }})</option>
            </select>
            <div v-if="!entries.length" class="muted" style="font-size:12px;margin-top:4px">无 entry 节点，请先在「节点」创建</div>
          </div>
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
        <div class="muted" style="font-size:12px;line-height:1.6">
          推荐拓扑：Entry → Relay → Exit。可只选部分节点；保存后会生成 hops 配置下发给 Agent。
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="show = false">取消</button>
        <button class="btn btn-primary" :disabled="saving || !canSave" @click="create">
          {{ saving ? '保存中…' : '保存线路' }}
        </button>
      </div>
    </div>
  </div>
</template>
