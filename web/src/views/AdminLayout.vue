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
    exits: '落地',
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

async function loadVersion() {
  try {
    // bust any intermediate proxy cache
    const r = await fetch('/api/version?t=' + Date.now(), {
      cache: 'no-store',
      headers: { 'Cache-Control': 'no-cache' },
    })
    if (r.ok) {
      const j = await r.json()
      version.value = j.version || ''
    }
  } catch {
    /* ignore */
  }
}

onMounted(() => {
  loadVersion()
})
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">M</div>
        <div class="brand-text">
          <strong>Mieru 控制台</strong>
          <span>Control Plane</span>
        </div>
      </div>
      <nav class="sidebar-nav">
        <router-link class="nav-item" :class="{ active: route.name === 'dashboard' }" to="/">总览</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'nodes' }" to="/nodes">节点</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'exits' }" to="/exits">落地</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'users' }" to="/users">用户</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'routes' }" to="/routes">线路</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'settings' }" to="/settings">设置</router-link>
      </nav>
      <div class="sidebar-foot">
        <div>多跳编排 · 落地计量</div>
        <div v-if="version" class="sidebar-ver mono">{{ version }}</div>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <div class="topbar-user">{{ user || 'admin' }}</div>
        <div class="topbar-actions">
          <span v-if="version" class="badge mono">{{ version }}</span>
          <button class="btn btn-ghost btn-sm" @click="logout">退出登录</button>
        </div>
      </header>
      <main class="content">
        <router-view />
      </main>
    </div>
  </div>
</template>
