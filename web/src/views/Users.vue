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

// share / QR modal — encodes socks5:// node link (not subscription URL)
const subShow = ref(false)
const subUser = ref(null)
const shareURL = ref('') // primary socks5:// for QR
const shareURLs = ref('') // all entries newline-separated
const subLink = ref('') // Clash YAML subscription (secondary)
const subQR = ref('')
const subLoading = ref(false)
const entries = ref([])

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

async function makeQR(text) {
  if (!text) return ''
  return QRCode.toDataURL(text, {
    width: 280,
    margin: 2,
    color: { dark: '#0f172a', light: '#ffffff' },
    errorCorrectionLevel: 'M',
  })
}

async function openSub(u) {
  subUser.value = u
  shareURL.value = ''
  shareURLs.value = ''
  subLink.value = ''
  subQR.value = ''
  entries.value = []
  subShow.value = true
  subLoading.value = true
  try {
    let detail = null
    if (u?.id) {
      // dedicated share endpoint returns socks5:// with password
      try {
        detail = await api(`/api/admin/users/${u.id}/share`)
      } catch {
        detail = await api(`/api/admin/users/${u.id}`)
      }
    }
    if (detail) {
      shareURL.value = detail.share_url || ''
      shareURLs.value = detail.share_urls || detail.share_url || ''
      entries.value = Array.isArray(detail.entries) ? detail.entries : []
      subLink.value = subURL(detail.subscription || '')
      if (detail.user) {
        subUser.value = { ...u, ...detail.user }
      }
      // create-user response may already carry share_url
    }
    if (!shareURL.value && created.value && created.value.user?.id === u?.id) {
      shareURL.value = created.value.share_url || ''
      shareURLs.value = created.value.share_urls || shareURL.value
      entries.value = created.value.entries || []
    }
    if (!shareURL.value && u?.share_url) {
      shareURL.value = u.share_url
    }
    // QR encodes the node link, NOT the http subscription URL
    if (shareURL.value) {
      subQR.value = await makeQR(shareURL.value)
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
    <span class="muted" style="font-size:13px">开户、绑定线路、扫码导入节点</span>
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
              <button class="btn btn-primary btn-sm" @click="openSub(u)">扫码使用</button>
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
            <dt>节点链接</dt>
            <dd style="word-break:break-all" class="mono">{{ created.share_url || '（无入口节点）' }}</dd>
          </div>
          <div class="row-actions" style="margin-top:10px">
            <button class="btn btn-ghost btn-sm" @click="copy(created.proxy_password)">复制密码</button>
            <button
              class="btn btn-ghost btn-sm"
              :disabled="!created.share_url"
              @click="copy(created.share_url)"
            >
              复制节点链接
            </button>
            <button
              class="btn btn-primary btn-sm"
              @click="openSub({ id: created.user?.id, username: created.user?.username || form.username, share_url: created.share_url })"
            >
              扫码使用
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

  <!-- QR: socks5:// node link (scan to import), not subscription URL -->
  <div v-if="subShow" class="modal-mask" @click.self="subShow = false">
    <div class="modal" style="width:min(520px,100%)">
      <div class="modal-hd">
        <h3>扫码使用 · {{ subUser?.username || '' }}</h3>
        <button class="btn btn-ghost btn-sm" @click="subShow = false">关闭</button>
      </div>
      <div class="modal-bd" style="text-align:center">
        <div v-if="subLoading" class="muted" style="padding:24px">生成二维码…</div>
        <template v-else>
          <div
            v-if="subQR"
            style="display:inline-block;padding:14px;background:#fff;border-radius:12px;border:1px solid var(--border);margin-bottom:14px"
          >
            <img :src="subQR" alt="节点二维码" width="280" height="280" style="display:block" />
          </div>
          <div v-else class="muted" style="padding:16px">
            无法生成二维码（用户未绑定可用入口，或入口缺少公网地址/端口）
          </div>

          <div class="field" style="text-align:left">
            <label>节点链接（扫码内容 · socks5://）</label>
            <textarea
              readonly
              rows="3"
              class="mono"
              style="width:100%;resize:vertical;background:var(--bg-elevated);border:1px solid var(--border);border-radius:10px;padding:12px;font-size:12px"
              :value="shareURL"
            />
          </div>

          <div v-if="entries.length > 1" class="field" style="text-align:left;margin-top:10px">
            <label>全部入口</label>
            <div
              v-for="(e, i) in entries"
              :key="i"
              class="mono"
              style="font-size:12px;word-break:break-all;padding:6px 0;border-bottom:1px solid var(--border)"
            >
              {{ e.name }} · {{ e.host }}:{{ e.port }}
              <button class="btn btn-ghost btn-sm" style="margin-left:8px" @click="copy(e.url)">复制</button>
            </div>
          </div>

          <div class="field" style="text-align:left;margin-top:12px">
            <label class="muted">Clash 订阅（可选，YAML 下载地址）</label>
            <textarea
              readonly
              rows="2"
              class="mono"
              style="width:100%;resize:vertical;background:var(--bg-elevated);border:1px solid var(--border);border-radius:10px;padding:12px;font-size:11px;opacity:0.85"
              :value="subLink"
            />
          </div>

          <div class="muted" style="font-size:12px;line-height:1.55;margin-top:10px;text-align:left">
            用 Shadowrocket / Quantumult X / Surge / 小火箭等扫描上方二维码，直接导入 SOCKS5 节点。
            不要扫 Clash 订阅地址。骨干 mieru 对客户端透明。
          </div>
        </template>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="subShow = false">关闭</button>
        <button class="btn btn-ghost" :disabled="!subLink" @click="copy(subLink)">复制订阅</button>
        <button class="btn btn-primary" :disabled="!shareURL" @click="copy(shareURL)">复制节点链接</button>
      </div>
    </div>
  </div>
</template>
