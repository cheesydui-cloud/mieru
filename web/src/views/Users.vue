<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import QRCode from 'qrcode'
import { api, copyText, formatBytes, getToken, statusBadge } from '../api'

const users = ref([])
const routes = ref([])
const error = ref('')
const toast = ref('')
const show = ref(false)
const mode = ref('create') // create | edit | created
const editingId = ref(null)
const created = ref(null)
const saving = ref(false)
const form = reactive({
  username: '',
  expire_at: '',
  traffic_limit_gb: 100,
  route_id: null,
  entry_host: '',
  entry_port: null,
  note: '',
  status: 'active',
})

// share / QR modal
const subShow = ref(false)
const subUser = ref(null)
const shareURL = ref('')
const shareURLs = ref('')
const subQR = ref('')
const subLoading = ref(false)
const entries = ref([])
const mihomoYAML = ref('')
const mihomoURL = ref('')

let timer

function routeName(id) {
  if (id == null || id === '') return '—'
  const r = (routes.value || []).find((x) => x.id === id || String(x.id) === String(id))
  return r ? r.name : `#${id}`
}

function statusLabel(s) {
  const m = { active: '正常', disabled: '停用', expired: '到期', over_quota: '超流量' }
  return m[s] || s || '—'
}

async function load() {
  try {
    const [us, rs] = await Promise.all([api('/api/admin/users'), api('/api/admin/routes')])
    users.value = Array.isArray(us) ? us : []
    routes.value = Array.isArray(rs) ? rs : []
    error.value = ''
  } catch (e) {
    error.value = e.message
    users.value = []
    routes.value = []
  }
}

function blankForm() {
  Object.assign(form, {
    username: '',
    expire_at: '',
    traffic_limit_gb: 100,
    route_id: routes.value[0]?.id || null,
    entry_host: '',
    entry_port: null,
    note: '',
    status: 'active',
  })
}

function openCreate() {
  blankForm()
  created.value = null
  editingId.value = null
  mode.value = 'create'
  show.value = true
  error.value = ''
}

function openEdit(u) {
  Object.assign(form, {
    username: u.username || '',
    expire_at: u.expire_at ? String(u.expire_at).slice(0, 10) : '',
    traffic_limit_gb: u.traffic_limit_bytes
      ? Math.round(u.traffic_limit_bytes / (1024 * 1024 * 1024))
      : 0,
    route_id: u.route_id ?? null,
    entry_host: u.entry_host || '',
    entry_port: u.entry_port || null,
    note: u.note || '',
    status: u.status || 'active',
  })
  created.value = null
  editingId.value = u.id
  mode.value = 'edit'
  show.value = true
  error.value = ''
}

async function create() {
  if (!form.username.trim()) {
    error.value = '请填写用户名'
    return
  }
  saving.value = true
  try {
    const body = {
      username: form.username.trim(),
      expire_at: form.expire_at || undefined,
      traffic_limit_bytes: Math.round(Number(form.traffic_limit_gb || 0) * 1024 * 1024 * 1024),
      route_id: form.route_id ? Number(form.route_id) : null,
      entry_host: (form.entry_host || '').trim() || undefined,
      entry_port: form.entry_port ? Number(form.entry_port) : undefined,
      note: form.note,
    }
    created.value = await api('/api/admin/users', {
      method: 'POST',
      body: JSON.stringify(body),
    })
    mode.value = 'created'
    toast.value = '用户已创建'
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function saveEdit() {
  if (!editingId.value) return
  saving.value = true
  try {
    const body = {
      status: form.status,
      expire_at: form.expire_at || undefined,
      clear_expire: !form.expire_at,
      traffic_limit_bytes: Math.round(Number(form.traffic_limit_gb || 0) * 1024 * 1024 * 1024),
      // 0 = unbind route (backend treats <=0 as clear)
      route_id: form.route_id ? Number(form.route_id) : 0,
      entry_host: (form.entry_host || '').trim(),
      entry_port: form.entry_port ? Number(form.entry_port) : 0,
      note: form.note,
    }
    await api(`/api/admin/users/${editingId.value}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    })
    toast.value = '已保存'
    show.value = false
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function resetPw(id) {
  const res = await api(`/api/admin/users/${id}/reset-password`, { method: 'POST' })
  toast.value = `新密码：${res.proxy_password}`
}

async function remove(id) {
  if (!confirm('确认删除用户？')) return
  await api(`/api/admin/users/${id}`, { method: 'DELETE' })
  toast.value = '已删除'
  await load()
}

async function copy(text) {
  try {
    await copyText(text)
    toast.value = '已复制'
  } catch {
    toast.value = '复制失败，请手动选中'
  }
}

async function makeQR(text) {
  if (!text) return ''
  return QRCode.toDataURL(text, {
    width: 260,
    margin: 2,
    color: { dark: '#0f172a', light: '#ffffff' },
    errorCorrectionLevel: 'M',
  })
}

async function openSub(u) {
  subUser.value = u
  shareURL.value = ''
  shareURLs.value = ''
  subQR.value = ''
  entries.value = []
  mihomoYAML.value = ''
  mihomoURL.value = ''
  subShow.value = true
  subLoading.value = true
  try {
    let detail = null
    if (u?.id) {
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
      mihomoYAML.value = detail.mihomo_yaml || ''
      mihomoURL.value = detail.mihomo_url || ''
      if (detail.user) subUser.value = { ...u, ...detail.user }
    }
    if (!shareURL.value && created.value && created.value.user?.id === u?.id) {
      shareURL.value = created.value.share_url || ''
      shareURLs.value = created.value.share_urls || shareURL.value
      entries.value = created.value.entries || []
      mihomoYAML.value = created.value.mihomo_yaml || ''
    }
    if (!shareURL.value && u?.share_url) shareURL.value = u.share_url
    if (shareURL.value) subQR.value = await makeQR(shareURL.value)
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    subLoading.value = false
  }
}

async function downloadMihomo(u) {
  const id = u?.id || subUser.value?.id
  if (!id) {
    toast.value = '无用户 ID'
    return
  }
  try {
    const token = getToken()
    const res = await fetch(`/api/admin/users/${id}/mihomo.yaml`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
    if (!res.ok) {
      const t = await res.text()
      throw new Error(t || res.statusText)
    }
    const blob = await res.blob()
    const name = `mihomo-${u?.username || subUser.value?.username || id}.yaml`
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(a.href)
    toast.value = `已下载 ${name}`
  } catch (e) {
    // fallback: use in-memory yaml
    if (mihomoYAML.value) {
      const blob = new Blob([mihomoYAML.value], { type: 'application/x-yaml' })
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = `mihomo-${subUser.value?.username || 'user'}.yaml`
      document.body.appendChild(a)
      a.click()
      a.remove()
      toast.value = '已下载 YAML'
      return
    }
    error.value = e.message || '下载失败'
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 8000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div v-if="error && !show && !subShow" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

  <div class="page-tabs">
    <div class="page-tab active">用户</div>
  </div>

  <div class="panel-toolbar">
    <p class="help-text" style="margin:0">开户 → 绑线路 → 扫码 / 下载 Mihomo YAML</p>
    <button class="btn btn-primary btn-sm" @click="openCreate">开户</button>
  </div>

  <div class="table-wrap">
    <table class="data table-users" v-if="users.length">
      <thead>
        <tr>
          <th class="col-user">用户</th>
          <th class="col-status">状态</th>
          <th class="col-date">到期</th>
          <th class="col-traffic">流量</th>
          <th class="col-route">线路</th>
          <th class="col-ops">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="u in users" :key="u.id">
          <td class="col-user">
            <div class="name-link">{{ u.username }}</div>
            <div class="muted mono" style="font-size:11px">#{{ u.id }}</div>
          </td>
          <td class="col-status">
            <span class="badge" :class="statusBadge(u.status)">
              <span class="dot"></span>{{ statusLabel(u.status) }}
            </span>
          </td>
          <td class="col-date mono">{{ u.expire_at ? String(u.expire_at).slice(0, 10) : '永久' }}</td>
          <td class="col-traffic mono">
            {{ formatBytes(u.traffic_used_bytes) }}
            <span class="muted">/</span>
            {{ u.traffic_limit_bytes ? formatBytes(u.traffic_limit_bytes) : '∞' }}
          </td>
          <td class="col-route">{{ routeName(u.route_id) }}</td>
          <td class="col-ops">
            <div class="row-actions">
              <button class="btn btn-link btn-sm" @click="openSub(u)">扫码</button>
              <button class="btn btn-link btn-sm" @click="downloadMihomo(u)">YAML</button>
              <button class="btn btn-link btn-sm" @click="openEdit(u)">编辑</button>
              <button class="btn btn-link btn-sm" @click="resetPw(u.id)">重置密码</button>
              <button class="btn btn-link-danger btn-sm" @click="remove(u.id)">删除</button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty">暂无用户</div>
  </div>

  <!-- create / edit -->
  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal" style="width:min(560px,100%)">
      <div class="modal-hd">
        <h3>
          <template v-if="mode === 'created'">用户已创建</template>
          <template v-else-if="mode === 'edit'">编辑用户</template>
          <template v-else>开户</template>
        </h3>
        <button class="btn btn-ghost btn-sm" @click="show = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div v-if="error && show" class="error" style="margin:0">{{ error }}</div>
        <template v-if="mode !== 'created'">
          <div class="form-grid">
            <div class="field">
              <label>用户名</label>
              <input
                v-model="form.username"
                placeholder="alice"
                :disabled="mode === 'edit'"
              />
            </div>
            <div class="field" v-if="mode === 'edit'">
              <label>状态</label>
              <select v-model="form.status">
                <option value="active">正常</option>
                <option value="disabled">停用</option>
              </select>
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
            <div class="field">
              <label>公网入口 IP（可选）</label>
              <input v-model="form.entry_host" placeholder="商家前置 IP，如 211.x.x.x" />
            </div>
            <div class="field">
              <label>入口端口（可选）</label>
              <input v-model.number="form.entry_port" type="number" min="1" max="65535" placeholder="如 10401" />
            </div>
          </div>
          <div class="field">
            <label>备注</label>
            <input v-model="form.note" />
          </div>
          <p class="help-text" style="margin:0">
            扫码 / YAML 连<strong>前置</strong>；认证与出口在<strong>落地家宽 mita</strong>。
          </p>
        </template>
        <template v-else>
          <div class="kv">
            <dt>代理密码</dt>
            <dd>{{ created.proxy_password }}</dd>
            <dt>节点链接</dt>
            <dd style="word-break:break-all" class="mono">{{ created.share_url || '（无可用入口）' }}</dd>
          </div>
          <div class="row-actions" style="margin-top:4px">
            <button class="btn btn-ghost btn-sm" @click="copy(created.proxy_password)">复制密码</button>
            <button class="btn btn-ghost btn-sm" :disabled="!created.share_url" @click="copy(created.share_url)">
              复制节点链接
            </button>
            <button
              class="btn btn-primary btn-sm"
              @click="
                openSub({
                  id: created.user?.id,
                  username: created.user?.username || form.username,
                  share_url: created.share_url,
                })
              "
            >
              扫码 / YAML
            </button>
          </div>
        </template>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="show = false">
          {{ mode === 'created' ? '完成' : '取消' }}
        </button>
        <button v-if="mode === 'create'" class="btn btn-primary" :disabled="saving" @click="create">
          {{ saving ? '创建中…' : '创建' }}
        </button>
        <button v-else-if="mode === 'edit'" class="btn btn-primary" :disabled="saving" @click="saveEdit">
          {{ saving ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>
  </div>

  <!-- QR + YAML -->
  <div v-if="subShow" class="modal-mask" @click.self="subShow = false">
    <div class="modal" style="width:min(520px,100%)">
      <div class="modal-hd">
        <h3>扫码 / 配置 · {{ subUser?.username || '' }}</h3>
        <button class="btn btn-ghost btn-sm" @click="subShow = false">关闭</button>
      </div>
      <div class="modal-bd share-modal">
        <div v-if="subLoading" class="muted" style="padding:24px;text-align:center">生成中…</div>
        <template v-else>
          <div class="qr-center">
            <div v-if="subQR" class="qr-box">
              <img :src="subQR" alt="节点二维码" width="260" height="260" />
            </div>
            <div v-else class="muted" style="padding:16px;text-align:center">
              无法生成二维码（未绑定线路 / 无前置地址）
            </div>
          </div>

          <div class="field">
            <label>节点链接（扫码内容 · mierus://）</label>
            <textarea
              readonly
              rows="3"
              class="mono share-ta"
              :value="shareURL"
            />
          </div>

          <div v-if="entries.length > 1" class="field">
            <label>全部入口</label>
            <div
              v-for="(e, i) in entries"
              :key="i"
              class="mono entry-row"
            >
              <span>{{ e.name }} · {{ e.host }}:{{ e.port }}</span>
              <button class="btn btn-link btn-sm" @click="copy(e.url)">复制</button>
            </div>
          </div>

          <div class="field">
            <label>Mihomo / Clash Meta YAML</label>
            <textarea
              readonly
              rows="10"
              class="mono share-ta"
              :value="mihomoYAML"
              placeholder="暂无 endpoint，请绑线路并填写入口"
            />
            <p class="help-text" style="margin-top:6px">
              下载后用 Mihomo / Clash Meta「从文件导入」。节点类型 <code class="mono">mieru</code>，连前置 IP，出口为家宽。
            </p>
          </div>
        </template>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="subShow = false">关闭</button>
        <button class="btn btn-ghost" :disabled="!shareURL" @click="copy(shareURL)">复制链接</button>
        <button class="btn btn-ghost" :disabled="!mihomoYAML" @click="copy(mihomoYAML)">复制 YAML</button>
        <button class="btn btn-primary" :disabled="!mihomoYAML && !subUser?.id" @click="downloadMihomo(subUser)">
          下载 YAML
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.table-users {
  table-layout: fixed;
  width: 100%;
}
.table-users th,
.table-users td {
  vertical-align: middle;
}
.col-user { width: 16%; }
.col-status { width: 12%; }
.col-date { width: 14%; }
.col-traffic { width: 18%; text-align: left; }
.col-route { width: 14%; }
.col-ops { width: 26%; }

.share-modal {
  text-align: left;
}
.qr-center {
  display: flex;
  justify-content: center;
  align-items: center;
  margin-bottom: 8px;
}
.qr-box {
  display: inline-flex;
  padding: 14px;
  background: #fff;
  border: 1px solid var(--border-line);
  border-radius: 6px;
}
.qr-box img {
  display: block;
}
.share-ta {
  width: 100%;
  resize: vertical;
  background: var(--bg-elevated);
  border: 1px solid var(--border-line);
  border-radius: 6px;
  padding: 12px;
  font-size: 12px;
  line-height: 1.45;
}
.entry-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  word-break: break-all;
  padding: 6px 0;
  border-bottom: 1px solid var(--border);
}
</style>
