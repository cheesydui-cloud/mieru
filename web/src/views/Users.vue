<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { api, formatBytes, formatBps, statusBadge } from '../api'

const users = ref([])
const routes = ref([])
const error = ref('')
const toast = ref('')
const show = ref(false)
const created = ref(null)
const form = reactive({
  username: '',
  expire_at: '',
  traffic_limit_gb: 100,
  route_id: null,
  note: '',
})
let timer

async function load() {
  try {
    const [us, rs] = await Promise.all([
      api('/api/admin/users'),
      api('/api/admin/routes'),
    ])
    users.value = us
    routes.value = rs
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

function openCreate() {
  Object.assign(form, {
    username: '',
    expire_at: '',
    traffic_limit_gb: 100,
    route_id: routes.value[0]?.id || null,
    note: '',
  })
  created.value = null
  show.value = true
}

async function create() {
  try {
    const body = {
      username: form.username,
      expire_at: form.expire_at || undefined,
      traffic_limit_bytes: Math.round(Number(form.traffic_limit_gb || 0) * 1024 * 1024 * 1024),
      route_id: form.route_id ? Number(form.route_id) : null,
      note: form.note,
    }
    created.value = await api('/api/admin/users', {
      method: 'POST',
      body: JSON.stringify(body),
    })
    toast.value = '用户已创建'
    await load()
  } catch (e) {
    error.value = e.message
  }
}

async function resetPw(id) {
  const res = await api(`/api/admin/users/${id}/reset-password`, { method: 'POST' })
  toast.value = `新密码：${res.proxy_password}`
}

async function resetSub(id) {
  const res = await api(`/api/admin/users/${id}/reset-sub`, { method: 'POST' })
  toast.value = `新订阅：${res.subscription}`
}

async function remove(id) {
  if (!confirm('确认删除用户？')) return
  await api(`/api/admin/users/${id}`, { method: 'DELETE' })
  await load()
}

async function copy(text) {
  await navigator.clipboard.writeText(text)
  toast.value = '已复制'
}

function subURL(tokenPath) {
  if (!tokenPath) return ''
  if (tokenPath.startsWith('http')) return tokenPath
  return `${location.origin}${tokenPath}`
}

onMounted(() => {
  load()
  timer = setInterval(load, 5000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

  <div class="panel">
    <div class="panel-hd">
      <h2>用户</h2>
      <button class="btn btn-primary btn-sm" @click="openCreate">开户</button>
    </div>
    <div class="panel-bd">
      <table class="data" v-if="users.length">
        <thead>
          <tr>
            <th>用户</th>
            <th>状态</th>
            <th>到期</th>
            <th>流量</th>
            <th>实时</th>
            <th>线路</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td>
              <div>{{ u.username }}</div>
              <div class="muted mono" style="font-size:12px">#{{ u.id }}</div>
            </td>
            <td>
              <span class="badge" :class="statusBadge(u.status)">
                <span class="dot"></span>{{ u.status }}
              </span>
            </td>
            <td class="mono">{{ u.expire_at ? u.expire_at.slice(0, 10) : '永久' }}</td>
            <td class="num">
              {{ formatBytes(u.traffic_used_bytes) }}
              <span class="muted">/</span>
              {{ u.traffic_limit_bytes ? formatBytes(u.traffic_limit_bytes) : '∞' }}
            </td>
            <td class="num">
              ↓ {{ formatBps(u.down_bps) }}
              <span class="muted">·</span>
              ↑ {{ formatBps(u.up_bps) }}
            </td>
            <td class="mono">{{ u.route_id || '—' }}</td>
            <td>
              <div class="row-actions">
                <button class="btn btn-ghost btn-sm" @click="resetPw(u.id)">重置密码</button>
                <button class="btn btn-ghost btn-sm" @click="resetSub(u.id)">重置订阅</button>
                <button class="btn btn-danger btn-sm" @click="remove(u.id)">删除</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty">暂无用户</div>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal">
      <div class="modal-hd">
        <h3>开户</h3>
        <button class="btn btn-ghost btn-sm" @click="show = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="form-grid">
          <div class="field">
            <label>用户名</label>
            <input v-model="form.username" placeholder="alice" />
          </div>
          <div class="field">
            <label>到期日</label>
            <input v-model="form.expire_at" type="date" />
          </div>
          <div class="field">
            <label>流量上限 (GB，0=不限)</label>
            <input v-model.number="form.traffic_limit_gb" type="number" min="0" />
          </div>
          <div class="field">
            <label>线路</label>
            <select v-model="form.route_id">
              <option :value="null">未绑定</option>
              <option v-for="r in routes" :key="r.id" :value="r.id">{{ r.name }} (#{{ r.id }})</option>
            </select>
          </div>
        </div>
        <div class="field">
          <label>备注</label>
          <input v-model="form.note" />
        </div>
        <div v-if="created" class="card" style="padding:14px">
          <div class="kv">
            <dt>代理密码</dt>
            <dd>{{ created.proxy_password }}</dd>
            <dt>订阅</dt>
            <dd style="word-break:break-all">{{ subURL(created.subscription) }}</dd>
          </div>
          <div class="row-actions" style="margin-top:10px">
            <button class="btn btn-ghost btn-sm" @click="copy(created.proxy_password)">复制密码</button>
            <button class="btn btn-ghost btn-sm" @click="copy(subURL(created.subscription))">复制订阅</button>
          </div>
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="show = false">关闭</button>
        <button class="btn btn-primary" @click="create">创建</button>
      </div>
    </div>
  </div>
</template>
