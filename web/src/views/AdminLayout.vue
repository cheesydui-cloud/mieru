<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { clearSession, getUsername } from '../api'

const route = useRoute()
const router = useRouter()
const user = getUsername()
const version = ref('')

const title = computed(() => {
  const map = {
    dashboard: '总览',
    nodes: '节点',
    users: '用户',
    routes: '线路',
    settings: '设置',
  }
  return map[route.name] || '控制台'
})

function logout() {
  clearSession()
  router.replace('/login')
}

onMounted(async () => {
  try {
    const r = await fetch('/api/version', { cache: 'no-store' })
    if (r.ok) {
      const j = await r.json()
      version.value = j.version || ''
    }
  } catch {
    /* ignore */
  }
})
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">M</div>
        <div class="brand-text">
          <strong>Mieru Panel</strong>
          <span>Control Plane</span>
        </div>
      </div>
      <router-link class="nav-item" :class="{ active: route.name === 'dashboard' }" to="/">总览</router-link>
      <router-link class="nav-item" :class="{ active: route.name === 'nodes' }" to="/nodes">节点</router-link>
      <router-link class="nav-item" :class="{ active: route.name === 'routes' }" to="/routes">线路</router-link>
      <router-link class="nav-item" :class="{ active: route.name === 'users' }" to="/users">用户</router-link>
      <router-link class="nav-item" :class="{ active: route.name === 'settings' }" to="/settings">设置</router-link>
      <div class="sidebar-foot">
        <div>域名优先 · 落地主计量 · Agent 下发</div>
        <div v-if="version" class="sidebar-ver mono">{{ version }}</div>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <h1>{{ title }}</h1>
        <div class="topbar-actions">
          <span v-if="version" class="badge mono" style="margin-right:8px">{{ version }}</span>
          <span>{{ user }}</span>
          <button class="btn btn-ghost btn-sm" @click="logout">退出</button>
        </div>
      </header>
      <main class="content">
        <router-view />
      </main>
    </div>
  </div>
</template>
