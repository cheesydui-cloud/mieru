<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, setSession } from '../api'

const router = useRouter()
const route = useRoute()
const username = ref('admin')
const password = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  error.value = ''
  loading.value = true
  try {
    const data = await api('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    setSession(data)
    const next = typeof route.query.next === 'string' ? route.query.next : ''
    if (next && next.startsWith('/') && !next.startsWith('//')) {
      router.replace(next)
    } else {
      router.replace(data.role === 'admin' ? '/' : '/portal')
    }
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="brand" style="padding: 0 0 16px; border: none; min-height: auto">
        <div class="brand-mark">M</div>
        <div class="brand-text">
          <strong>Mieru 控制台</strong>
        </div>
      </div>
      <h1>登录</h1>
      <p>管理节点、用户、隧道与落地计量</p>
      <form class="stack" @submit.prevent="submit">
        <div class="field">
          <label>用户名</label>
          <input v-model="username" autocomplete="username" />
        </div>
        <div class="field">
          <label>密码</label>
          <input v-model="password" type="password" autocomplete="current-password" />
        </div>
        <button class="btn btn-primary" style="width: 100%; height: 40px" :disabled="loading">
          {{ loading ? '登录中…' : '进入控制台' }}
        </button>
        <div v-if="error" class="error">{{ error }}</div>
      </form>
    </div>
  </div>
</template>
