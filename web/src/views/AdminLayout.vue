<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { clearSession, getUsername } from '../api'
import { brand, brandMarkLetter, loadBrand } from '../brand'

const route = useRoute()
const router = useRouter()
const user = getUsername()
const version = ref('')
const navOpen = ref(false)

const title = computed(() => {
  const map = {
    dashboard: '总览',
    users: '用户',
    routes: '隧道',
    nodes: '节点',
    settings: '设置',
  }
  return map[route.name] || '控制台'
})

const mark = computed(() => brandMarkLetter(brand.name))

function logout() {
  clearSession()
  router.replace('/login')
}

function closeNav() {
  navOpen.value = false
}

function toggleNav() {
  navOpen.value = !navOpen.value
}

async function loadVersion() {
  try {
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

watch(
  () => route.fullPath,
  () => {
    navOpen.value = false
  },
)

function onKey(e) {
  if (e.key === 'Escape') navOpen.value = false
}

onMounted(() => {
  loadVersion()
  loadBrand()
  window.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div class="app-shell" :class="{ 'nav-open': navOpen }">
    <div v-if="navOpen" class="nav-backdrop" @click="closeNav" />
    <aside class="sidebar" :class="{ open: navOpen }">
      <div class="brand">
        <div class="brand-mark">
          <img
            v-if="brand.faviconData"
            :src="brand.faviconData"
            alt=""
            style="width:100%;height:100%;object-fit:cover;border-radius:inherit"
          />
          <template v-else>{{ mark }}</template>
        </div>
        <div class="brand-text">
          <strong :title="brand.name">{{ brand.name || 'Mieru' }}</strong>
          <span>控制台</span>
        </div>
        <button type="button" class="btn btn-ghost btn-sm nav-close" @click="closeNav">关闭</button>
      </div>
      <nav class="sidebar-nav" @click="closeNav">
        <router-link class="nav-item" :class="{ active: route.name === 'dashboard' }" to="/">总览</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'users' }" to="/users">用户</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'routes' }" to="/routes">隧道</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'nodes' }" to="/nodes">节点</router-link>
        <router-link class="nav-item" :class="{ active: route.name === 'settings' }" to="/settings">设置</router-link>
      </nav>
      <div class="sidebar-foot">
        <div>前置 → 家宽落地</div>
        <div v-if="version" class="sidebar-ver mono">{{ version }}</div>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <div class="topbar-left">
          <button type="button" class="btn btn-ghost btn-sm nav-toggle" @click="toggleNav" aria-label="菜单">
            ☰
          </button>
          <div class="topbar-user">{{ user || 'admin' }} · {{ title }}</div>
        </div>
        <div class="topbar-actions">
          <span v-if="version" class="badge mono">{{ version }}</span>
          <button class="btn btn-ghost btn-sm" @click="logout" title="退出登录">退出登录</button>
        </div>
      </header>
      <main class="content">
        <router-view />
      </main>
    </div>
  </div>
</template>
