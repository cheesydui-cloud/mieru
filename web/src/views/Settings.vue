<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api, getToken } from '../api'
import { setBrandMeta } from '../brand'

const error = ref('')
const toast = ref('')
const loading = ref(false)
const audit = ref([])
const auditQ = ref('')
const auditAction = ref('')
const securityHints = ref([])
const jwtDefault = ref(false)
const corsWide = ref(false)
const form = reactive({
  panel_url: '',
  panel_name: '',
  panel_subtitle: '',
  panel_favicon: '',
  panel_url_set: false,
  version: '',
  admin_user: 'admin',
  cf_zone_id: '',
  cf_api_token: '',
  cf_token_set: false,
  cf_configured: false,
  cf_proxied_default: false,
})
const cfTesting = ref(false)
const pw = reactive({
  current_password: '',
  new_password: '',
  new_password2: '',
  username: '',
})

const filteredAudit = computed(() => {
  let list = audit.value || []
  const q = auditQ.value.trim().toLowerCase()
  const act = auditAction.value.trim().toLowerCase()
  if (act) list = list.filter((a) => String(a.action || '').toLowerCase().includes(act))
  if (q) {
    list = list.filter((a) => {
      const hay = `${a.actor || ''} ${a.action || ''} ${a.target || ''} ${a.detail || ''}`.toLowerCase()
      return hay.includes(q)
    })
  }
  return list
})

async function load() {
  try {
    const [s, logs] = await Promise.all([
      api('/api/admin/settings'),
      api('/api/admin/audit?limit=200').catch(() => []),
    ])
    form.panel_url = s.panel_url || ''
    form.panel_name = s.panel_name || 'Mieru Panel'
    form.panel_subtitle = s.panel_subtitle || ''
    form.panel_favicon = s.panel_favicon || ''
    setBrandMeta({
      name: s.panel_name,
      subtitle: s.panel_subtitle,
      faviconData: s.panel_favicon || '',
    })
    form.panel_url_set = !!s.panel_url_set
    form.version = s.version || ''
    form.admin_user = s.admin_user || 'admin'
    form.cf_zone_id = s.cf_zone_id || ''
    form.cf_token_set = !!s.cf_token_set
    form.cf_configured = !!s.cf_configured
    form.cf_proxied_default = !!s.cf_proxied_default
    // never echo real token; placeholder if set
    form.cf_api_token = s.cf_token_set ? '********' : ''
    pw.username = form.admin_user
    securityHints.value = Array.isArray(s.security_hints) ? s.security_hints : []
    jwtDefault.value = !!s.jwt_is_default
    corsWide.value = !!s.cors_wide_open
    audit.value = Array.isArray(logs) ? logs : []
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

async function downloadBackup() {
  loading.value = true
  error.value = ''
  try {
    const tok = getToken()
    const res = await fetch('/api/admin/backup', {
      headers: {
        Accept: 'application/json',
        ...(tok ? { Authorization: `Bearer ${tok}` } : {}),
      },
      cache: 'no-store',
    })
    const text = await res.text()
    if (!res.ok) {
      let msg = text
      try {
        msg = JSON.parse(text).error || text
      } catch {
        /* keep */
      }
      throw new Error(msg || res.statusText)
    }
    const blob = new Blob([text], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `mieru-backup-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.json`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(a.href)
    toast.value = '备份已下载（不含 agent_token / 管理员密码）'
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function onFaviconFile(ev) {
  const f = ev.target.files && ev.target.files[0]
  if (!f) return
  if (f.size > 100 * 1024) {
    error.value = '图标请 ≤100KB'
    return
  }
  const reader = new FileReader()
  reader.onload = () => {
    form.panel_favicon = String(reader.result || '')
  }
  reader.readAsDataURL(f)
}

function clearFavicon() {
  form.panel_favicon = ''
}

async function saveSettings() {
  loading.value = true
  error.value = ''
  try {
    const body = {
      panel_url: form.panel_url,
      panel_name: form.panel_name,
      panel_subtitle: form.panel_subtitle,
      panel_favicon: form.panel_favicon,
      cf_zone_id: form.cf_zone_id,
      cf_proxied_default: !!form.cf_proxied_default,
    }
    // only send token when user typed a new one / clear (not placeholder)
    const tok = (form.cf_api_token || '').trim()
    if (tok === 'clear') {
      body.cf_api_token = 'clear'
    } else if (tok && tok !== '********') {
      body.cf_api_token = tok
    }
    // empty + was set → keep existing (omit field)
    const res = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify(body),
    })
    form.panel_url = res.panel_url
    form.panel_name = res.panel_name
    form.panel_subtitle = res.panel_subtitle || ''
    form.panel_favicon = res.panel_favicon || ''
    form.panel_url_set = true
    form.cf_configured = !!res.cf_configured
    form.cf_token_set = !!res.cf_token_set
    form.cf_zone_id = res.cf_zone_id || form.cf_zone_id
    form.cf_api_token = form.cf_token_set ? '********' : ''
    setBrandMeta({
      name: res.panel_name,
      subtitle: res.panel_subtitle,
      faviconData: res.panel_favicon || '',
    })
    toast.value = '设置已保存。侧栏名称、登录副标题与图标已更新。'
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function testCF() {
  cfTesting.value = true
  error.value = ''
  try {
    // save first if user typed new token
    await saveSettings()
    const res = await api('/api/admin/cloudflare/test', { method: 'POST', body: '{}' })
    toast.value = res.zone_name ? `Cloudflare 正常 · Zone ${res.zone_name}` : 'Cloudflare 连接正常'
  } catch (e) {
    error.value = e.message
  } finally {
    cfTesting.value = false
  }
}

function clearCFToken() {
  form.cf_api_token = 'clear'
  form.cf_token_set = false
}

async function changePassword() {
  error.value = ''
  if (pw.new_password !== pw.new_password2) {
    error.value = '两次新密码不一致'
    return
  }
  if (pw.new_password.length < 6) {
    error.value = '新密码至少 6 位'
    return
  }
  loading.value = true
  try {
    await api('/api/admin/admin-password', {
      method: 'POST',
      body: JSON.stringify({
        username: pw.username || form.admin_user,
        current_password: pw.current_password,
        new_password: pw.new_password,
      }),
    })
    toast.value = '管理员密码已修改，请牢记新密码'
    pw.current_password = ''
    pw.new_password = ''
    pw.new_password2 = ''
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function fmtTime(t) {
  if (!t) return '—'
  try {
    const d = new Date(t)
    if (Number.isNaN(d.getTime())) return String(t).slice(0, 19)
    return d.toLocaleString()
  } catch {
    return String(t)
  }
}

onMounted(load)
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

  <div class="page-tabs">
    <div class="page-tab active">设置</div>
  </div>

  <div
    v-if="securityHints.length || !form.panel_url_set"
    class="panel"
    style="border-color: var(--warning)"
  >
    <div class="panel-hd">
      <div>
        <h2>安全 / 外链提示</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">上线前建议处理</div>
      </div>
    </div>
    <div class="panel-bd" style="padding:14px 16px">
      <ul style="margin:0;padding-left:18px;line-height:1.6;font-size:13px">
        <li v-if="!form.panel_url_set" style="color:var(--warning)">
          未永久保存「面板地址」—— 用户查询页 / 订阅链接可能变成当前访问的 IP:端口，请在下方填写并保存。
        </li>
        <li v-for="(h, i) in securityHints" :key="i" style="color:var(--warning)">{{ h }}</li>
        <li v-if="jwtDefault" class="muted" style="font-size:12px">
          例：在 <code class="mono">/etc/mieru-panel.env</code> 设置
          <code class="mono">PANEL_JWT_SECRET=$(openssl rand -hex 32)</code> 后
          <code class="mono">systemctl restart mieru-panel</code>
        </li>
        <li v-if="corsWide" class="muted" style="font-size:12px">
          跨域 env：<code class="mono">PANEL_CORS</code>（默认 <code class="mono">*</code>，同域 SPA 可忽略）
        </li>
      </ul>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>面板</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          Agent 回连 · 查询页/订阅外链基准 · 品牌 · 安装命令
        </div>
      </div>
      <span class="badge mono" v-if="form.version">{{ form.version }}</span>
    </div>
    <div class="panel-bd" style="padding:16px">
      <div class="field" style="margin-bottom:14px">
        <label>面板地址（对外 URL，必填）</label>
        <input
          v-model="form.panel_url"
          placeholder="https://panel.example.com 或 http://IP:8080"
          :style="!form.panel_url_set ? 'border-color:var(--warning)' : ''"
        />
        <p class="help-text" style="margin-top:6px">
          用户查询页、订阅、扫码分享都基于此地址。带 http/https；只写 IP:端口 会自动补 http://。
          <span v-if="!form.panel_url_set" style="color:var(--warning);font-weight:600">
            尚未永久保存
          </span>
          <span v-else style="color:var(--success)">已保存</span>
        </p>
      </div>
      <div class="field" style="margin-bottom:14px">
        <label>面板名称</label>
        <input v-model="form.panel_name" placeholder="例如：微动传媒" />
        <p class="help-text" style="margin-top:6px">
          显示在左侧栏、登录页、浏览器标签标题与图标首字。
        </p>
      </div>
      <div class="field" style="margin-bottom:14px">
        <label>登录页副标题</label>
        <input v-model="form.panel_subtitle" placeholder="管理节点、用户、隧道与落地计量" />
      </div>
      <div class="field" style="margin-bottom:16px">
        <label>自定义图标（favicon / logo，可选）</label>
        <div class="row-actions" style="align-items:center;gap:12px;margin-bottom:8px">
          <div
            class="brand-mark"
            style="width:40px;height:40px;font-size:16px;overflow:hidden"
          >
            <img
              v-if="form.panel_favicon"
              :src="form.panel_favicon"
              alt=""
              style="width:100%;height:100%;object-fit:cover"
            />
            <span v-else>{{ (form.panel_name || 'M').slice(0, 1) }}</span>
          </div>
          <input type="file" accept="image/png,image/jpeg,image/svg+xml,image/webp" @change="onFaviconFile" />
          <button v-if="form.panel_favicon" type="button" class="btn btn-ghost btn-sm" @click="clearFavicon">
            清除自定义
          </button>
        </div>
        <p class="help-text">PNG/SVG/WebP，建议 ≤100KB。清空后恢复名称首字图标。</p>
      </div>
      <button class="btn btn-primary" :disabled="loading" @click="saveSettings">保存</button>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>Cloudflare</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          新建/编辑节点时一键添加域名 A 记录 · API Token 需 Zone.DNS 编辑权限
        </div>
      </div>
      <span class="badge" :class="form.cf_configured ? 'ok' : 'warn'">
        {{ form.cf_configured ? '已配置' : '未配置' }}
      </span>
    </div>
    <div class="panel-bd" style="padding:16px">
      <div class="form-grid">
        <div class="field">
          <label>Zone ID</label>
          <input v-model="form.cf_zone_id" class="mono" placeholder="Cloudflare 域名 Zone ID" />
        </div>
        <div class="field">
          <label>API Token</label>
          <input
            v-model="form.cf_api_token"
            type="password"
            autocomplete="off"
            :placeholder="form.cf_token_set ? '已保存（留空不改，输入新 Token 覆盖）' : 'Bearer Token'"
          />
        </div>
      </div>
      <label class="muted" style="display:flex;align-items:center;gap:8px;margin:12px 0;font-size:13px">
        <input type="checkbox" v-model="form.cf_proxied_default" />
        默认开启橙云代理（入口自定义端口请勿开，建议仅 DNS）
      </label>
      <p class="help-text" style="margin:0 0 12px">
        Token 创建：Cloudflare → My Profile → API Tokens → Create Token →
        权限 <code class="mono">Zone.DNS Edit</code> + 指定 Zone。
        节点弹窗点「CF 添加/更新解析」即可把公网 IP 写到接入域名。
      </p>
      <div class="row-actions">
        <button class="btn btn-primary" :disabled="loading" @click="saveSettings">保存 CF 配置</button>
        <button class="btn btn-ghost" :disabled="loading || cfTesting" @click="testCF">
          {{ cfTesting ? '测试中…' : '测试连接' }}
        </button>
        <button
          v-if="form.cf_token_set"
          class="btn btn-ghost"
          type="button"
          :disabled="loading"
          @click="clearCFToken(); saveSettings()"
        >
          清除 Token
        </button>
      </div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>备份导出</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          JSON 快照：设置 / 节点（无 token）/ 隧道 / 用户
        </div>
      </div>
      <button class="btn btn-ghost btn-sm" :disabled="loading" @click="downloadBackup">下载备份</button>
    </div>
    <div class="panel-bd" style="padding:14px 16px">
      <p class="help-text" style="margin:0">
        不含 agent_token、管理员密码哈希、用户代理密码。节点侧请继续保留
        <code class="mono">/etc/mieru-agent.env</code>。
      </p>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>管理员</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">修改登录账号</div>
      </div>
    </div>
    <div class="panel-bd" style="padding:16px">
      <div class="form-grid">
        <div class="field">
          <label>用户名</label>
          <input v-model="pw.username" />
        </div>
        <div class="field">
          <label>当前密码</label>
          <input v-model="pw.current_password" type="password" autocomplete="current-password" />
        </div>
        <div class="field">
          <label>新密码</label>
          <input v-model="pw.new_password" type="password" autocomplete="new-password" />
        </div>
        <div class="field">
          <label>确认新密码</label>
          <input v-model="pw.new_password2" type="password" autocomplete="new-password" />
        </div>
      </div>
      <p class="help-text" style="margin:12px 0 14px">
        改密立即生效。勿开 <code class="mono">PANEL_ADMIN_FORCE_SYNC=1</code>，否则重启会用 env 覆盖。
      </p>
      <button class="btn btn-primary" :disabled="loading" @click="changePassword">修改密码</button>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>操作审计</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          最近 200 条 · 支持关键词 / 动作过滤
        </div>
      </div>
      <button class="btn btn-ghost btn-sm" @click="load">刷新</button>
    </div>
    <div class="panel-bd">
      <div class="panel-toolbar" style="padding:12px 16px 0;gap:8px;flex-wrap:wrap">
        <input class="input-filter" v-model="auditQ" placeholder="搜索 操作者/动作/对象/详情" />
        <input class="input-filter" v-model="auditAction" placeholder="动作含… 如 rebuild" style="min-width:140px" />
      </div>
      <table class="data" v-if="filteredAudit.length">
        <thead>
          <tr>
            <th>时间</th>
            <th>操作者</th>
            <th>动作</th>
            <th>对象</th>
            <th>详情</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in filteredAudit" :key="a.id">
            <td class="mono" style="font-size:12px;white-space:nowrap">{{ fmtTime(a.created_at) }}</td>
            <td>{{ a.actor }}</td>
            <td class="mono">{{ a.action }}</td>
            <td class="mono" style="font-size:12px">{{ a.target }}</td>
            <td class="muted" style="font-size:12px">{{ a.detail || '—' }}</td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty">{{ audit.length ? '无匹配记录' : '暂无审计记录' }}</div>
    </div>
  </div>
</template>
