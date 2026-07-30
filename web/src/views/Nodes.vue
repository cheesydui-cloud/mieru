<script setup>
import { onMounted, reactive, ref } from 'vue'
import { api, statusBadge } from '../api'

const nodes = ref([])
const error = ref('')
const toast = ref('')
const show = ref(false)
const createdToken = ref('')
const form = reactive({
  name: '',
  role: 'entry',
  region: '',
  tags: '',
  public_ip: '',
  hostname: '',
  alt_hostnames: '',
})

async function load() {
  try {
    nodes.value = await api('/api/admin/nodes')
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

function openCreate() {
  Object.assign(form, {
    name: '',
    role: 'entry',
    region: '',
    tags: '',
    public_ip: '',
    hostname: '',
    alt_hostnames: '',
  })
  createdToken.value = ''
  show.value = true
}

async function create() {
  try {
    const res = await api('/api/admin/nodes', {
      method: 'POST',
      body: JSON.stringify(form),
    })
    createdToken.value = res.agent_token
    toast.value = `节点已创建：${res.node.id}`
    await load()
  } catch (e) {
    error.value = e.message
  }
}

async function remove(id) {
  if (!confirm('确认删除该节点？')) return
  await api(`/api/admin/nodes/${id}`, { method: 'DELETE' })
  await load()
}

async function rebuild() {
  await api('/api/admin/rebuild', { method: 'POST' })
  toast.value = '已重建全部节点配置'
  await load()
}

async function copy(text) {
  await navigator.clipboard.writeText(text)
  toast.value = '已复制'
}

onMounted(load)
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

  <div class="panel">
    <div class="panel-hd">
      <h2>节点列表</h2>
      <div class="row-actions">
        <button class="btn btn-ghost btn-sm" @click="rebuild">重建配置</button>
        <button class="btn btn-primary btn-sm" @click="openCreate">新建节点</button>
      </div>
    </div>
    <div class="panel-bd">
      <table class="data" v-if="nodes.length">
        <thead>
          <tr>
            <th>名称</th>
            <th>角色</th>
            <th>接入域名</th>
            <th>公网 IP</th>
            <th>区域 / 标签</th>
            <th>状态</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="n in nodes" :key="n.id">
            <td>
              <div>{{ n.name }}</div>
              <div class="muted mono" style="font-size:12px">{{ n.id }}</div>
            </td>
            <td><span class="badge">{{ n.role }}</span></td>
            <td class="mono">{{ n.hostname || '—' }}</td>
            <td class="mono">{{ n.public_ip || '—' }}</td>
            <td>
              <div>{{ n.region || '—' }}</div>
              <div class="muted" style="font-size:12px">{{ n.tags || '' }}</div>
            </td>
            <td>
              <span class="badge" :class="statusBadge(n.status)">
                <span class="dot"></span>{{ n.status }}
              </span>
            </td>
            <td>
              <button class="btn btn-danger btn-sm" @click="remove(n.id)">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty">还没有节点。建议先建 Exit，再 Relay，再 Entry（可填域名）。</div>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal">
      <div class="modal-hd">
        <h3>新建节点</h3>
        <button class="btn btn-ghost btn-sm" @click="show = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="form-grid">
          <div class="field">
            <label>名称</label>
            <input v-model="form.name" placeholder="us-exit-01" />
          </div>
          <div class="field">
            <label>角色</label>
            <select v-model="form.role">
              <option value="entry">entry（入口）</option>
              <option value="relay">relay（中继）</option>
              <option value="exit">exit（落地）</option>
              <option value="hybrid">hybrid</option>
            </select>
          </div>
          <div class="field">
            <label>接入域名</label>
            <input v-model="form.hostname" placeholder="e1.example.com" />
          </div>
          <div class="field">
            <label>公网 IP</label>
            <input v-model="form.public_ip" placeholder="x.x.x.x" />
          </div>
          <div class="field">
            <label>区域</label>
            <input v-model="form.region" placeholder="cn / us / sh-ix" />
          </div>
          <div class="field">
            <label>标签</label>
            <input v-model="form.tags" placeholder="residential,tk" />
          </div>
        </div>
        <div v-if="createdToken" class="card" style="padding:14px">
          <div class="muted" style="margin-bottom:8px">请立即保存 Agent Token（仅此时完整展示）</div>
          <div class="mono" style="word-break:break-all">{{ createdToken }}</div>
          <div class="row-actions" style="margin-top:10px">
            <button class="btn btn-ghost btn-sm" @click="copy(createdToken)">复制 Token</button>
          </div>
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="show = false">取消</button>
        <button class="btn btn-primary" @click="create">创建</button>
      </div>
    </div>
  </div>
</template>
