<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '../api'

const routes = ref([])
const nodes = ref([])
const error = ref('')
const show = ref(false)
const form = reactive({
  name: '',
  strategy: 'sticky',
  entry_id: '',
  relay_id: '',
  exit_id: '',
})

const entries = computed(() => nodes.value.filter((n) => n.role === 'entry' || n.role === 'hybrid'))
const relays = computed(() => nodes.value.filter((n) => n.role === 'relay' || n.role === 'hybrid'))
const exits = computed(() => nodes.value.filter((n) => n.role === 'exit' || n.role === 'hybrid'))

async function load() {
  try {
    const [rs, ns] = await Promise.all([
      api('/api/admin/routes'),
      api('/api/admin/nodes'),
    ])
    routes.value = rs
    nodes.value = ns
    error.value = ''
  } catch (e) {
    error.value = e.message
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
  const n = nodes.value.find((x) => x.id === hop.node_id)
  return n ? `${n.name} (${n.role})` : hop.node_id
}

function openCreate() {
  form.name = ''
  form.strategy = 'sticky'
  form.entry_id = entries.value[0]?.id || ''
  form.relay_id = relays.value[0]?.id || ''
  form.exit_id = exits.value[0]?.id || ''
  show.value = true
}

async function create() {
  const hops = []
  if (form.entry_id) hops.push({ node_id: form.entry_id, order: 0, capability_type: 'socks_in' })
  if (form.relay_id) hops.push({ node_id: form.relay_id, order: 1, capability_type: 'mieru_client' })
  if (form.exit_id) hops.push({ node_id: form.exit_id, order: 2, capability_type: 'mita_server' })
  try {
    await api('/api/admin/routes', {
      method: 'POST',
      body: JSON.stringify({
        name: form.name,
        strategy: form.strategy,
        enabled: true,
        hops_json: JSON.stringify(hops),
        weight: 100,
      }),
    })
    show.value = false
    await load()
  } catch (e) {
    error.value = e.message
  }
}

async function remove(id) {
  if (!confirm('删除线路？')) return
  await api(`/api/admin/routes/${id}`, { method: 'DELETE' })
  await load()
}

onMounted(load)
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>

  <div class="panel">
    <div class="panel-hd">
      <h2>线路编排</h2>
      <button class="btn btn-primary btn-sm" @click="openCreate">新建线路</button>
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
              </div>
            </td>
            <td>{{ r.health }}</td>
            <td>
              <button class="btn btn-danger btn-sm" @click="remove(r.id)">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty">通用 hops 模型：Entry → Relay(mieru) → Exit(mita)</div>
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
          <input v-model="form.name" placeholder="cn-entry → ix → us-exit" />
        </div>
        <div class="form-grid">
          <div class="field">
            <label>策略</label>
            <select v-model="form.strategy">
              <option value="sticky">sticky</option>
              <option value="wrr">wrr</option>
              <option value="failover">failover</option>
            </select>
          </div>
          <div class="field">
            <label>Entry</label>
            <select v-model="form.entry_id">
              <option value="">—</option>
              <option v-for="n in entries" :key="n.id" :value="n.id">{{ n.name }}</option>
            </select>
          </div>
          <div class="field">
            <label>Relay</label>
            <select v-model="form.relay_id">
              <option value="">—</option>
              <option v-for="n in relays" :key="n.id" :value="n.id">{{ n.name }}</option>
            </select>
          </div>
          <div class="field">
            <label>Exit</label>
            <select v-model="form.exit_id">
              <option value="">—</option>
              <option v-for="n in exits" :key="n.id" :value="n.id">{{ n.name }}</option>
            </select>
          </div>
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="show = false">取消</button>
        <button class="btn btn-primary" @click="create">保存</button>
      </div>
    </div>
  </div>
</template>
