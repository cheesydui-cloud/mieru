<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { api, copyText, statusBadge } from '../api'

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
  role: 'entry',
  region: '',
  tags: '',
  public_ip: '',
  private_ip: '',
  hostname: '',
  alt_hostnames: '',
  port_min: 10001,
  port_max: 20000,
})

function blankForm() {
  Object.assign(form, {
    name: '',
    role: 'entry',
    region: '',
    tags: '',
    public_ip: '',
    private_ip: '',
    hostname: '',
    alt_hostnames: '',
    port_min: 10001,
    port_max: 20000,
  })
}

function fillForm(n) {
  Object.assign(form, {
    name: n.name || '',
    role: n.role || 'entry',
    region: n.region || '',
    tags: n.tags || '',
    public_ip: n.public_ip || '',
    private_ip: n.private_ip || '',
    hostname: n.hostname || '',
    alt_hostnames: n.alt_hostnames || '',
    port_min: n.port_min > 0 ? n.port_min : (n.listen_port > 0 ? n.listen_port : 10001),
    port_max: n.port_max > 0 ? n.port_max : (n.port_min > 0 ? n.port_min : 20000),
  })
}

function portLabel(n) {
  if (n.port_min > 0 || n.port_max > 0) {
    const a = n.port_min || n.listen_port || '?'
    const b = n.port_max || a
    return a === b ? String(a) : `${a}-${b}`
  }
  if (n.listen_port > 0) return String(n.listen_port)
  return '默认'
}

const filteredNodes = computed(() => {
  const q = (filter.value || '').trim().toLowerCase()
  if (!q) return nodes.value || []
  return (nodes.value || []).filter((n) => {
    return (
      (n.name || '').toLowerCase().includes(q) ||
      (n.id || '').toLowerCase().includes(q) ||
      (n.hostname || '').toLowerCase().includes(q) ||
      (n.public_ip || '').toLowerCase().includes(q) ||
      (n.role || '').toLowerCase().includes(q)
    )
  })
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
  return {
    name: form.name,
    role: form.role,
    region: form.region,
    tags: form.tags,
    public_ip: form.public_ip,
    private_ip: form.private_ip,
    hostname: form.hostname,
    alt_hostnames: form.alt_hostnames,
    // only start/end; backend treats start as primary listen
    port_min: min,
    port_max: max,
    listen_port: min,
  }
}

async function create() {
  if (!form.name.trim()) {
    error.value = '请填写名称'
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
    toast.value = `节点已创建：${res.node.id}`
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
    error.value = '请填写名称'
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
    toast.value = '节点已更新'
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
  if (!confirm('确认删除该节点？')) return
  await api(`/api/admin/nodes/${id}`, { method: 'DELETE' })
  toast.value = '已删除'
  await load()
}

async function rebuild() {
  await api('/api/admin/rebuild', { method: 'POST' })
  toast.value = '已重建全部节点配置'
  await load()
}

async function copy(text) {
  try {
    await copyText(text)
    toast.value = '已复制到剪贴板'
  } catch {
    // last resort: select the textarea if present
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
    <div class="page-tab active">节点列表</div>
  </div>

  <div class="panel-toolbar">
    <input class="input-filter" v-model="filter" placeholder="筛选名称…" />
    <div class="row-actions">
      <button class="btn btn-ghost btn-sm" @click="rebuild">重建配置</button>
      <button class="btn btn-primary btn-sm" @click="openCreate">新增节点</button>
    </div>
  </div>
  <div class="muted" style="font-size: 12px; margin: -6px 0 10px; line-height: 1.5">
    家宽 / 住宅出口请到侧栏「落地」统一管理（角色 exit）。本页可管入口、中继、落地全部角色。
  </div>

  <div class="table-wrap">
    <table class="data" v-if="filteredNodes.length">
      <thead>
        <tr>
          <th>名称</th>
          <th>角色</th>
          <th>在线</th>
          <th>接入域名</th>
          <th>公网 IP</th>
          <th>内网 IP</th>
          <th>端口</th>
          <th>区域</th>
          <th>状态</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="n in filteredNodes" :key="n.id">
          <td>
            <div class="name-link">{{ n.name }}</div>
            <div class="muted mono" style="font-size:11px">{{ n.id }}</div>
          </td>
          <td><span class="badge">{{ n.role }}</span></td>
          <td>
            <span class="badge" :class="statusBadge(n.status)">
              <span class="dot"></span>{{ n.status === 'online' ? '在线' : (n.status || '离线') }}
            </span>
          </td>
          <td class="mono">{{ n.hostname || '—' }}</td>
          <td class="mono">{{ n.public_ip || '—' }}</td>
          <td class="mono">{{ n.private_ip || '—' }}</td>
          <td class="mono" style="font-size:12px">{{ portLabel(n) }}</td>
          <td>{{ n.region || '—' }}</td>
          <td><span class="badge">{{ n.status || '—' }}</span></td>
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
    <div v-else class="empty">
      <div style="margin-bottom:12px">{{ nodes.length ? '无匹配节点' : '还没有节点' }}</div>
      <button v-if="!nodes.length" class="btn btn-primary" @click="openCreate">新增节点</button>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="show = false">
    <div class="modal" style="width:min(640px,100%)">
      <div class="modal-hd">
        <h3>
          <template v-if="mode === 'created'">节点已创建</template>
          <template v-else-if="mode === 'edit'">编辑节点</template>
          <template v-else>新建节点</template>
        </h3>
        <button class="btn btn-ghost btn-sm" @click="show = false">关闭</button>
      </div>
      <div class="modal-bd">
        <template v-if="mode !== 'created'">
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
              <label>内网 IP</label>
              <input v-model="form.private_ip" placeholder="IX/机房内网，如 10.x.x.x" />
            </div>
            <div class="field">
              <label>区域</label>
              <input v-model="form.region" placeholder="cn / us / sh-ix" />
            </div>
            <div class="field">
              <label>标签</label>
              <input v-model="form.tags" placeholder="residential,tk" />
            </div>
            <div class="field">
              <label>起始端口</label>
              <input v-model.number="form.port_min" type="number" min="0" max="65535" placeholder="10001" />
            </div>
            <div class="field">
              <label>结束端口</label>
              <input v-model.number="form.port_max" type="number" min="0" max="65535" placeholder="20000" />
            </div>
          </div>
          <div class="muted" style="font-size:12px;line-height:1.55">
            只填端口范围，例如 <code class="mono">10001</code> ～ <code class="mono">20000</code>。
            起始端口同时作为订阅/客户端主端口；范围内端口用于按用户分配转发。
            都填 <code class="mono">0</code> 则用角色默认范围。
            <strong>内网 IP</strong>：上一跳连本节点时优先用（入口→中继、中继→落地在 IX 内网互通时填）。
            <span v-if="mode === 'edit'" class="mono"> · ID：{{ editingId }}</span>
          </div>
        </template>
        <template v-else>
          <div class="kv">
            <dt>Node ID</dt>
            <dd class="mono">{{ created.node.id }}</dd>
            <dt>Token</dt>
            <dd class="mono" style="word-break:break-all">{{ created.agent_token }}</dd>
            <dt>面板地址</dt>
            <dd class="mono">{{ created.panel_url }}</dd>
            <dt>端口范围</dt>
            <dd class="mono">{{ created.node.port_min }}-{{ created.node.port_max }}</dd>
          </div>
          <div class="field" style="margin-top:12px">
            <label>一键安装 Agent（在目标 Linux 上执行）</label>
            <textarea
              readonly
              rows="4"
              class="mono"
              style="width:100%;resize:vertical;background:var(--bg-elevated);border:1px solid var(--border);border-radius:10px;padding:12px"
              :value="created.install_cmd"
            />
          </div>
          <div class="row-actions" style="margin-top:10px">
            <button class="btn btn-primary btn-sm" @click="copy(created.install_cmd)">复制安装命令</button>
            <button class="btn btn-ghost btn-sm" @click="copy(created.agent_token)">复制 Token</button>
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
    <div class="modal" style="width:min(640px,100%)">
      <div class="modal-hd">
        <h3>Agent 安装命令</h3>
        <button class="btn btn-ghost btn-sm" @click="installShow = false">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="kv">
          <dt>Node ID</dt>
          <dd class="mono">{{ installInfo.node_id }}</dd>
          <dt>Role</dt>
          <dd><span class="badge">{{ installInfo.role }}</span></dd>
          <dt>Token</dt>
          <dd class="mono" style="word-break:break-all">{{ installInfo.agent_token }}</dd>
          <dt>面板</dt>
          <dd class="mono">{{ installInfo.panel_url }}</dd>
        </div>
        <div class="field" style="margin-top:12px">
          <label>在目标机器执行</label>
          <textarea
            readonly
            rows="4"
            class="mono"
            style="width:100%;resize:vertical;background:var(--bg-elevated);border:1px solid var(--border);border-radius:10px;padding:12px"
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
