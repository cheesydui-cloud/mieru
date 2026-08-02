<script setup>
import { onMounted, reactive, ref } from 'vue'
import { api } from '../api'
import { setBrandName } from '../brand'

const error = ref('')
const toast = ref('')
const loading = ref(false)
const form = reactive({
  panel_url: '',
  panel_name: '',
  panel_url_set: false,
  version: '',
  admin_user: 'admin',
})
const pw = reactive({
  current_password: '',
  new_password: '',
  new_password2: '',
  username: '',
})

async function load() {
  try {
    const s = await api('/api/admin/settings')
    form.panel_url = s.panel_url || ''
    form.panel_name = s.panel_name || 'Mieru Panel'
    if (s.panel_name) setBrandName(s.panel_name)
    form.panel_url_set = !!s.panel_url_set
    form.version = s.version || ''
    form.admin_user = s.admin_user || 'admin'
    pw.username = form.admin_user
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

async function saveSettings() {
  loading.value = true
  error.value = ''
  try {
    const res = await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({
        panel_url: form.panel_url,
        panel_name: form.panel_name,
      }),
    })
    form.panel_url = res.panel_url
    form.panel_name = res.panel_name
    form.panel_url_set = true
    setBrandName(res.panel_name)
    toast.value = '设置已保存。侧栏名称与浏览器标题/图标已更新。'
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
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
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
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

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>面板</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">Agent 回连地址 · 安装命令基准</div>
      </div>
      <span class="badge mono" v-if="form.version">{{ form.version }}</span>
    </div>
    <div class="panel-bd" style="padding:16px">
      <div class="field" style="margin-bottom:14px">
        <label>面板地址</label>
        <input
          v-model="form.panel_url"
        />
        <p class="help-text" style="margin-top:6px">
          带 http/https。只写 IP:端口 会自动补 http://。
          <span v-if="!form.panel_url_set" style="color:var(--warning)">尚未永久保存</span>
        </p>
      </div>
      <div class="field" style="margin-bottom:16px">
        <label>面板名称</label>
        <input v-model="form.panel_name" placeholder="例如：微动传媒" />
        <p class="help-text" style="margin-top:6px">
          显示在左侧栏、登录页、浏览器标签标题与图标首字。
        </p>
      </div>
      <button class="btn btn-primary" :disabled="loading" @click="saveSettings">保存</button>
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
</template>
