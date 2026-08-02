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

const navMain = [
  { name: 'dashboard', to: '/', label: '总览', hint: '拓扑与健康', icon: 'grid' },
  { name: 'users', to: '/users', label: '用户', hint: '开户与配额', icon: 'users' },
  { name: 'routes', to: '/routes', label: '隧道', hint: '前置 → 落地', icon: 'route' },
  { name: 'nodes', to: '/nodes', label: '节点', hint: 'Agent 与端口', icon: 'server' },
]

const navSystem = [
  { name: 'settings', to: '/settings', label: '设置', hint: '品牌 · CF · 安全', icon: 'settings' },
]

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
          <span>管理控制台</span>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-sm nav-close"
          aria-label="收起菜单"
          title="收起菜单"
          @click="closeNav"
        >
          ×
        </button>
      </div>

      <div class="sidebar-scroll">
        <div class="nav-section">
          <div class="nav-section-label">工作台</div>
          <nav class="sidebar-nav">
            <router-link
              v-for="item in navMain"
              :key="item.name"
              class="nav-item"
              :class="{ active: route.name === item.name }"
              :to="item.to"
              @click="closeNav"
            >
              <span class="nav-ico" :data-icon="item.icon" aria-hidden="true" />
              <span class="nav-copy">
                <span class="nav-label">{{ item.label }}</span>
                <span class="nav-hint">{{ item.hint }}</span>
              </span>
            </router-link>
          </nav>
        </div>

        <div class="nav-section">
          <div class="nav-section-label">系统</div>
          <nav class="sidebar-nav">
            <router-link
              v-for="item in navSystem"
              :key="item.name"
              class="nav-item"
              :class="{ active: route.name === item.name }"
              :to="item.to"
              @click="closeNav"
            >
              <span class="nav-ico" :data-icon="item.icon" aria-hidden="true" />
              <span class="nav-copy">
                <span class="nav-label">{{ item.label }}</span>
                <span class="nav-hint">{{ item.hint }}</span>
              </span>
            </router-link>
          </nav>
        </div>
      </div>

      <div class="sidebar-foot">
        <div class="sidebar-foot-card">
          <div class="sidebar-foot-kicker">链路</div>
          <div class="sidebar-foot-title">手机 → 前置 → 家宽</div>
        </div>
        <div class="sidebar-account">
          <div class="sidebar-account-meta">
            <span class="sidebar-account-name" :title="user || 'admin'">{{ user || 'admin' }}</span>
            <span v-if="version" class="sidebar-ver mono">{{ version }}</span>
          </div>
          <button type="button" class="btn btn-ghost btn-sm sidebar-logout" @click="logout" title="退出登录">
            退出登录
          </button>
        </div>
      </div>
    </aside>

    <div class="main">
      <header class="topbar">
        <div class="topbar-left">
          <button type="button" class="btn btn-ghost btn-sm nav-toggle" @click="toggleNav" aria-label="菜单">
            ☰
          </button>
          <div class="topbar-user">
            <span class="topbar-name">{{ user || 'admin' }}</span>
            <span class="topbar-sep">·</span>
            <span class="topbar-page">{{ title }}</span>
          </div>
        </div>
        <div class="topbar-actions">
          <!-- 版本号 / 退出登录已移至左侧栏底部 -->
        </div>
      </header>
      <main class="content">
        <router-view />
      </main>
    </div>
  </div>
</template>
