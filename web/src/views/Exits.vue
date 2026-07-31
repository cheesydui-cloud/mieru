<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { api, copyText, statusBadge } from '../api'

/** 家宽落地：只管理 exit / hybrid 节点 */
const nodes = ref([])
const filter = ref('')
const error = ref('')
const toast = ref('')
const show = ref(false)
const mode = ref('create') // create | edit | created
const installShow = ref(false)
const installInfo = ref(null)
const created = ref(null)
const editingId = ref('')
const saving = ref(false)

const form = reactive({
  name: '',
  role: 'exit',
  region: '',
  tags: 'residential',
  public_ip: '',
  private_ip: '',
  hostname: '',
  alt_hostnames: '',
  port_min: 8964,
  port_max: 8964,
})

function blankForm() {
  Object.assign(form, {
    name: '',
    role: 'exit',
    region: '',
    tags: 'residential',
    public_ip: '',
    private_ip: '',
    hostname: '',
    alt_hostnames: '',
    port_min: 8964,
    port_max: 8964,
  })
}

function fillForm(n) {
  Object.assign(form, {
    name: n.name || '',
    role: n.role === 'hybrid' ? 'hybrid' : 'exit',
    region: n.region || '',
    tags: n.tags || '',
    public_ip: n.public_ip || '',
    private_ip: n.private_ip || '',
    hostname: n.hostname || '',
    alt_hostnames: n.alt_hostnames || '',
    port_min: n.port_min > 0 ? n.port_min : n.listen_port > 0 ? n.listen_port : 8964,
    port_max: n.port_max > 0 ? n.port_max : n.port_min > 0 ? n.port_min : 8964,
  })
}

function portLabel(n) {
  if (n.port_min > 0 || n.port_max > 0) {
    const a = n.port_min || n.listen_port || '?'
    const b = n.port_max || a
    return a === b ? String(a) : `${a}-${b}`
  }
  if (n.listen_port > 0) return String(n.listen_port)
  return '8964'
}

function roleLabel(role) {
  if (role === 'hybrid') return 'hybrid（落地+入口）'
  return 'exit（纯落地）'
}

const exits = computed(() => {
  return (nodes.value || []).filter((n) => n.role === 'exit' || n.role === 'hybrid')
})

const filtered = computed(() => {
  const q = (filter.value || '').trim().toLowerCase()
  const list = exits.value
  if (!q) return list
  return list.filter((n) => {
    return (
      (n.name || '').toLowerCase().includes(q) ||
      (n.id || '').toLowerCase().includes(q) ||
      (n.hostname || '').toLowerCase().includes(q) ||
      (n.public_ip || '').toLowerCase().includes(q) ||
      (n.region || '').toLowerCase().includes(q) ||
      (n.tags || '').toLowerCase().includes(q)
    )
  })
})

const stats = computed(() => {
  const list = exits.value
  const online = list.filter((n) => n.status === 'online').length
  return { total: list.length, online, offline: list.length - online }
})

async function load() {
  try {
    const ns = await api('/api/admin/nodes')
    nodes.value = Array.isArray(ns) ? ns : []
    error.value = ''
  } catch (e) {
    error.value = e.message
    nodes.value = []
  }
}

function openCreate() {
  blankForm()
  created.value = null
  editingId.value = ''
  mode.value = 'create'
  show.value = true
}

function openEdit(n) {
  fillForm(n)
  created.value = null
  editingId.value = n.id
  mode.value = 'edit'
  show.value = true
  error.value = ''
}

function payload() {
  const min = Number(form.port_min) || 0
  const max = Number(form.port_max) || 0
  let tags = (form.tags || '').trim()
  // 家宽落地默认打 residential，方便筛选
  if (!tags) tags = 'residential'
  return {
    name: form.name,
    role: form.role === 'hybrid' ? 'hybrid' : 'exit',
    region: form.region,
    tags,
    public_ip: form.public_ip,
    private_ip: form.private_ip,
    hostname: form.hostname,
    alt_hostnames: form.alt_hostnames,
    port_min: min,
    port_max: max,
    listen_port: min,
  }
}

async function create() {
  if (!form.name.trim()) {
    error.value = '请填写落地名称'
    return
  }
  if ((form.port_min > 0) !== (form.port_max > 0)) {
    error.value = '起始端口和结束端口需同时填写'
    return
  }
  if (form.port_min > 0 && form.port_max > 0 && form.port_min > form.port_max) {
    error.value = '起始端口不能大于结束端口'
    return
  }
  saving.value = true
  try {
    const res = await api('/api/admin/nodes', {
      method: 'POST',
      body: JSON.stringify(payload()),
    })
    created.value = res
    mode.value = 'created'
    toast.value = `落地已创建：${res.node.id}`
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function saveEdit() {
  if (!editingId.value) return
  if (!form.name.trim()) {
    error.value = '请填写落地名称'
    return
  }
  if ((form.port_min > 0) !== (form.port_max > 0)) {
    error.value = '起始端口和结束端口需同时填写'
    return
  }
  if (form.port_min > 0 && form.port_max > 0 && form.port_min > form.port_max) {
    error.value = '起始端口不能大于结束端口'
    return
  }
  saving.value = true
  try {
    await api(`/api/admin/nodes/${editingId.value}`, {
      method: 'PUT',
      body: JSON.stringify(payload()),
    })
    toast.value = '落地已更新'
    show.value = false
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

async function showInstall(id) {
  try {
    installInfo.value = await api(`/api/admin/nodes/${id}/install`)
    installShow.value = true
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

async function remove(id) {
  if (!confirm('确认删除该落地？绑定到线路的 hops 需自行调整。')) return
  await api(`/api/admin/nodes/${id}`, { method: 'DELETE' })
  toast.value = '已删除'
  await load()
}

async function rebuild() {
  await api('/api/admin/rebuild', { method: 'POST' })
  toast.value = '已重建全部节点配置（含落地 mita）'
  await load()
}

async function copy(text) {
  try {
    await copyText(text)
    toast.value = '已复制到剪贴板'
  } catch {
    toast.value = '自动复制失败：请在文本框内 Ctrl/Cmd+C 手动复制'
  }
}

let refreshTimer
onMounted(() => {
  load()
  refreshTimer = setInterval(load, 5000)
})
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

  <div class="page-tabs">
    <div class="page-tab active">家宽落地</div>
  </div>

  <div class="grid-stats" style="margin-bottom: 4px">
    <div class="card">
      <h3>落地总数</h3>
      <div class="value">{{ stats.total }}</div>
    </div>
    <div class="card">
      <h3>在线</h3>
      <div class="value" style="color: var(--success)">{{ stats.online }}</div>
    </div>
    <div class="card">
      <h3>离线</h3>
      <div class="value">{{ stats.offline }}</div>
    </div>
    <div class="card">
      <h3>说明</h3>
      <div class="sub" style="margin-top: 0; line-height: 1.45">
        家宽机器装 Agent，角色 exit，跑 mita 出网计量
      </div>
    </div>
  </div>

  <div class="panel-toolbar">
    <input class="input-filter" v-model="filter" placeholder="筛选名称 / IP / 区域 / 标签…" />
    <div class="row-actions">
      <button class="btn btn-ghost btn-sm" @click="rebuild">重建配置</button>
      <button class="btn btn-primary btn-sm" @click="openCreate">新增落地</button>
    </div>
  </div>

  <div class="table-wrap">
    <table class="data" v-if="filtered.length">
      <thead>
        <tr>
          <th>名称</th>
          <th>类型</th>
          <th>在线</th>
          <th>公网 IP</th>
          <th>内网 IP</th>
          <th>域名</th>
          <th>mita 端口</th>
          <th>区域</th>
          <th>标签</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="n in filtered" :key="n.id">
          <td>
            <div class="name-link">{{ n.name }}</div>
            <div class="muted mono" style="font-size: 11px">{{ n.id }}</div>
          </td>
          <td>
            <span class="badge">{{ n.role === 'hybrid' ? 'hybrid' : 'exit' }}</span>
          </td>
          <td>
            <span class="badge" :class="statusBadge(n.status)">
              <span class="dot"></span>{{ n.status === 'online' ? '在线' : n.status || '离线' }}
            </span>
          </td>
          <td class="mono">{{ n.public_ip || '—' }}</td>
          <td class="mono">{{ n.private_ip || '—' }}</td>
          <td class="mono">{{ n.hostname || '—' }}</td>
          <td class="mono" style="font-size: 12px">{{ portLabel(n) }}</td>
          <td>{{ n.region || '—' }}</td>
          <td>
            <span class="muted" style="font-size: 12px">{{ n.tags || '—' }}</span>
          </td>
          <td>
            <div class="row-actions">
              <button class="btn btn-ghost btn-sm" @click="openEdit(n)">编辑</button>
              <button class="btn btn-ghost btn-sm" @click="showInstall(n.id)">安装命令</button>
              <button class="btn btn-danger btn-sm" @click="remove(n.id)">删除</button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty" style="padding: 48px 24px; text-align: center">
      <div style="font-size: 16px; margin-bottom: 8px">
        {{ exits.length ? '无匹配落地' : '还没有家宽落地' }}
      </div>
      <div
        v-if="!exits.length"
        class="muted"
        style="margin-bottom: 20px; max-width: 480px; margin-left: auto; margin-right: auto; line-height: 1.55"
      >
        落地 = 家宽 / 住宅出口机器。创建后复制安装命令到该机器执行，Agent 会拉起
        <code class="mono">mita</code>，中继通过 mieru 连到这里出网。
      </div>
      <button v-if="!exits.length" class="btn btn-primary" @click="openCreate">新增落地</button>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal" style="width: min(640px, 100%)">
      <div class="modal-hd">
        <h3>
          <template v-if="mode === 'created'">落地已创建</template>
          <template v-else-if="mode === 'edit'">编辑落地</template>
          <template v-else>新增家宽落地</template>
        </h3>
        <button class="btn btn-ghost btn-sm" @click="show = false">关闭</button>
      </div>
      <div class="modal-bd">
        <template v-if="mode !== 'created'">
          <div class="form-grid">
            <div class="field">
              <label>名称</label>
              <input v-model="form.name" placeholder="家宽-上海-01" />
            </div>
            <div class="field">
              <label>类型</label>
              <select v-model="form.role">
                <option value="exit">exit — 纯落地（mita 出网）</option>
                <option value="hybrid">hybrid — 落地 + 本机 SOCKS 入口</option>
              </select>
            </div>
            <div class="field">
              <label>公网 IP</label>
              <input v-model="form.public_ip" placeholder="家宽公网 IP" />
            </div>
            <div class="field">
              <label>内网 IP</label>
              <input v-model="form.private_ip" placeholder="IX 内网，中继可达" />
            </div>
            <div class="field">
              <label>接入域名（可选）</label>
              <input v-model="form.hostname" placeholder="exit1.example.com" />
            </div>
            <div class="field">
              <label>区域</label>
              <input v-model="form.region" placeholder="sh / bj / us-residential" />
            </div>
            <div class="field">
              <label>标签</label>
              <input v-model="form.tags" placeholder="residential,家宽,电信" />
            </div>
            <div class="field">
              <label>mita 起始端口</label>
              <input v-model.number="form.port_min" type="number" min="0" max="65535" placeholder="8964" />
            </div>
            <div class="field">
              <label>mita 结束端口</label>
              <input v-model.number="form.port_max" type="number" min="0" max="65535" placeholder="8964" />
            </div>
          </div>
          <div class="muted" style="font-size: 12px; line-height: 1.55">
            家宽落地默认单端口 <code class="mono">8964</code>（与中继 mieru 对齐）。
            若运营商只放行特定端口，改成实际可连的端口，并在家宽路由器/防火墙放行。
            中继连落地走 <strong>mieru → mita</strong>，不是 SOCKS。
            <span v-if="mode === 'edit'" class="mono"> · ID：{{ editingId }}</span>
          </div>
        </template>
        <template v-else>
          <div class="kv">
            <dt>Node ID</dt>
            <dd class="mono">{{ created.node.id }}</dd>
            <dt>类型</dt>
            <dd>{{ roleLabel(created.node.role) }}</dd>
            <dt>Token</dt>
            <dd class="mono" style="word-break: break-all">{{ created.agent_token }}</dd>
            <dt>面板地址</dt>
            <dd class="mono">{{ created.panel_url }}</dd>
            <dt>端口</dt>
            <dd class="mono">{{ created.node.port_min }}-{{ created.node.port_max }}</dd>
          </div>
          <div class="field" style="margin-top: 12px">
            <label>在家宽机器上执行（Linux）</label>
            <textarea
              readonly
              rows="4"
              class="mono"
              style="
                width: 100%;
                resize: vertical;
                background: var(--bg-elevated);
                border: 1px solid var(--border);
                border-radius: 10px;
                padding: 12px;
              "
              :value="created.install_cmd"
            />
          </div>
          <div class="row-actions" style="margin-top: 10px">
            <button class="btn btn-primary btn-sm" @click="copy(created.install_cmd)">复制安装命令</button>
            <button class="btn btn-ghost btn-sm" @click="copy(created.agent_token)">复制 Token</button>
          </div>
          <div class="muted" style="font-size: 12px; margin-top: 12px; line-height: 1.5">
            安装后约 15s 内心跳上线；日志：
            <code class="mono">journalctl -u mieru-agent -f</code>
            ，应看到 mita RUNNING。
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
          {{ saving ? '保存中…' : '保存修改' }}
        </button>
      </div>
    </div>
  </div>

  <div v-if="installShow && installInfo" class="modal-mask" @click.self="installShow = false">
    <div class="modal" style="width: min(640px, 100%)">
      <div class="modal-hd">
        <h3>落地 Agent 安装命令</h3>
        <button class="btn btn-ghost btn-sm" @click="installShow = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="kv">
          <dt>Node ID</dt>
          <dd class="mono">{{ installInfo.node_id }}</dd>
          <dt>Role</dt>
          <dd><span class="badge">{{ installInfo.role }}</span></dd>
          <dt>Token</dt>
          <dd class="mono" style="word-break: break-all">{{ installInfo.agent_token }}</dd>
          <dt>面板</dt>
          <dd class="mono">{{ installInfo.panel_url }}</dd>
        </div>
        <div class="field" style="margin-top: 12px">
          <label>在家宽机器执行</label>
          <textarea
            readonly
            rows="4"
            class="mono"
            style="
              width: 100%;
              resize: vertical;
              background: var(--bg-elevated);
              border: 1px solid var(--border);
              border-radius: 10px;
              padding: 12px;
            "
            :value="installInfo.install_cmd"
          />
        </div>
      </div>
      <div class="modal-ft">
        <button class="btn btn-ghost" @click="installShow = false">关闭</button>
        <button class="btn btn-primary" @click="copy(installInfo.install_cmd)">复制命令</button>
      </div>
    </div>
  </div>
</template>
