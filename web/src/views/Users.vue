<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import QRCode from 'qrcode'
import { api, copyText, formatBytes, formatBps, statusBadge } from '../api'

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

// subscription modal
const subShow = ref(false)
const subUser = ref(null)
const subLink = ref('')
const subQR = ref('')
const subLoading = ref(false)

let timer

async function load() {
  try {
    const [us, rs] = await Promise.all([
      api('/api/admin/users'),
      api('/api/admin/routes'),
    ])
    users.value = Array.isArray(us) ? us : []
    routes.value = Array.isArray(rs) ? rs : []
    error.value = ''
  } catch (e) {
    error.value = e.message
    users.value = []
    routes.value = []
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
  if (!confirm('重置订阅后旧链接立即失效，确认？')) return
  const res = await api(`/api/admin/users/${id}/reset-sub`, { method: 'POST' })
  toast.value = `新订阅已生成`
  await load()
  // if modal open for this user, refresh
  if (subShow.value && subUser.value?.id === id) {
    await openSub({ ...subUser.value, subscription: res.subscription, sub_token: res.sub_token })
  }
}

async function remove(id) {
  if (!confirm('确认删除用户？')) return
  await api(`/api/admin/users/${id}`, { method: 'DELETE' })
  await load()
}

async function copy(text) {
  try {
    await copyText(text)
    toast.value = '已复制到剪贴板'
  } catch {
    toast.value = '自动复制失败：请手动选中复制'
  }
}

function subURL(tokenPath) {
  if (!tokenPath) return ''
  if (tokenPath.startsWith('http')) return tokenPath
  return `${location.origin}${tokenPath}`
}

function userSubURL(u) {
  if (!u) return ''
  if (u.subscription) return subURL(u.subscription)
  if (u.sub_token) return `${location.origin}/sub/${u.sub_token}`
  return ''
}

async function openSub(u) {
  subUser.value = u
  subLink.value = userSubURL(u)
  subQR.value = ''
  subShow.value = true
  subLoading.value = true
  try {
    if (!subLink.value && u?.id) {
      const detail = await api(`/api/admin/users/${u.id}`)
      if (detail?.subscription) {
        subLink.value = subURL(detail.subscription)
        subUser.value = { ...u, ...(detail.user || {}), subscription: detail.subscription }
      }
    }
    if (subLink.value) {
      subQR.value = await QRCode.toDataURL(subLink.value, {
        width: 240,
        margin: 2,
        color: { dark: '#0f172a', light: '#ffffff' },
      })
    }
  } catch (e) {
    error.value = e.message || '生成二维码失败'
  } finally {
    subLoading.value = false
  }
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

  <div class="page-tabs">
    <div class="page-tab active">用户列表</div>
  </div>

  <div class="panel-toolbar">
    <span class="muted" style="font-size:13px">开户、绑定线路、订阅与代理密码</span>
    <button class="btn btn-primary btn-sm" @click="openCreate">开户</button>
  </div>

  <div class="table-wrap">
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
            <div class="name-link">{{ u.username }}</div>
            <div class="muted mono" style="font-size:11px">#{{ u.id }}</div>
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
              <button class="btn btn-primary btn-sm" @click="openSub(u)">订阅地址</button>
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
            <button
              class="btn btn-primary btn-sm"
              @click="openSub({ id: created.user?.id, username: created.user?.username || form.username, subscription: created.subscription, sub_token: created.sub_token })"
            >
              查看二维码
            </button>
          </div>
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="show = false">关闭</button>
        <button class="btn btn-primary" @click="create">创建</button>
      </div>
    </div>
  </div>

  <!-- subscription + QR -->
  <div v-if="subShow" class="modal-mask" @click.self="subShow = false">
    <div class="modal" style="width:min(480px,100%)">
      <div class="modal-hd">
        <h3>订阅地址 · {{ subUser?.username || '' }}</h3>
        <button class="btn btn-ghost btn-sm" @click="subShow = false">关闭</button>
      </div>
      <div class="modal-bd" style="text-align:center">
        <div v-if="subLoading" class="muted" style="padding:24px">生成二维码…</div>
        <template v-else>
          <div
            v-if="subQR"
            style="display:inline-block;padding:12px;background:#fff;border-radius:12px;border:1px solid var(--border);margin-bottom:14px"
          >
            <img :src="subQR" alt="订阅二维码" width="240" height="240" style="display:block" />
          </div>
          <div v-else class="muted" style="padding:16px">无法生成二维码（缺少订阅链接）</div>
          <div class="field" style="text-align:left">
            <label>订阅链接（Clash / 兼容客户端）</label>
            <textarea
              readonly
              rows="3"
              class="mono"
              style="width:100%;resize:vertical;background:var(--bg-elevated);border:1px solid var(--border);border-radius:10px;padding:12px;font-size:12px"
              :value="subLink"
            />
          </div>
          <div class="muted" style="font-size:12px;line-height:1.55;margin-top:8px;text-align:left">
            扫码或复制链接导入客户端。重置订阅会使本链接立即失效。
          </div>
        </template>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="subShow = false">关闭</button>
        <button class="btn btn-primary" :disabled="!subLink" @click="copy(subLink)">复制链接</button>
      </div>
    </div>
  </div>
</template>
