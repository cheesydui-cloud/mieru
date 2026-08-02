<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, clearSession, formatBytes, formatBps, getUsername, statusBadge } from '../api'

const router = useRouter()
const profile = ref(null)
const error = ref('')
const toast = ref('')
let timer

const remainPct = computed(() => {
  const u = profile.value?.user
  if (!u) return 100
  if (!u.traffic_limit_bytes) return 100
  const used = u.traffic_used_bytes || 0
  const left = Math.max(0, u.traffic_limit_bytes - used)
  return Math.round((left / u.traffic_limit_bytes) * 100)
})

const ringStyle = computed(() => ({ '--p': `${remainPct.value}%` }))

async function load() {
  try {
    profile.value = await api('/api/me/profile')
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

function logout() {
  clearSession()
  router.replace('/login')
}

async function copy(text) {
  await navigator.clipboard.writeText(text)
  toast.value = '已复制'
}

function subFull() {
  const p = profile.value?.subscription || ''
  if (!p) return ''
  if (p.startsWith('http')) return p
  return `${location.origin}${p}`
}

onMounted(() => {
  load()
  timer = setInterval(load, 3000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="login-page" style="place-items: start center; padding-top: 48px">
    <div style="width: min(880px, 100%); display: flex; flex-direction: column; gap: 16px">
      <div style="display:flex; justify-content: space-between; align-items:center">
        <div class="brand" style="padding:0">
          <div class="brand-mark">M</div>
          <div class="brand-text">
            <strong>我的接入</strong>
            <span>{{ getUsername() }}</span>
          </div>
        </div>
        <button class="btn btn-primary btn-sm" @click="logout">退出登录</button>
      </div>

      <div v-if="error" class="error">{{ error }}</div>
      <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>

      <template v-if="profile?.user">
        <div class="card ring-wrap">
          <div class="ring" :style="ringStyle">
            <div class="ring-inner">
              <strong>{{ remainPct }}%</strong>
              <span>剩余流量</span>
            </div>
          </div>
          <div>
            <div style="display:flex; gap:8px; align-items:center; margin-bottom:12px">
              <span class="badge" :class="statusBadge(profile.user.status)">
                <span class="dot"></span>{{ profile.user.status }}
              </span>
            </div>
            <dl class="kv">
              <dt>到期</dt>
              <dd>{{ profile.user.expire_at ? profile.user.expire_at.slice(0, 10) : '永久' }}</dd>
              <dt>已用</dt>
              <dd>
                {{ formatBytes(profile.user.traffic_used_bytes) }}
                /
                {{ profile.user.traffic_limit_bytes ? formatBytes(profile.user.traffic_limit_bytes) : '不限' }}
              </dd>
              <dt>今日</dt>
              <dd>↓ {{ formatBytes(profile.today_down) }} · ↑ {{ formatBytes(profile.today_up) }}</dd>
              <dt>实时</dt>
              <dd>
                ↓ {{ formatBps(profile.rate?.down_bps) }}
                ·
                ↑ {{ formatBps(profile.rate?.up_bps) }}
              </dd>
            </dl>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>订阅与接入</h2>
            <button class="btn btn-primary btn-sm" @click="copy(subFull())">复制订阅链接</button>
          </div>
          <div class="panel-bd" style="padding:16px 20px">
            <div class="mono" style="word-break:break-all; color: var(--text-secondary)">{{ subFull() }}</div>
            <div class="muted" style="margin-top:8px; font-size:12px">
              订阅仅包含 Entry 域名；骨干 mieru 对客户端透明。
            </div>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd"><h2>入口节点</h2></div>
          <div class="panel-bd">
            <table class="data" v-if="profile.entries?.length">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>域名</th>
                  <th>区域</th>
                  <th>状态</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="e in profile.entries" :key="e.host">
                  <td>{{ e.name }}</td>
                  <td class="mono">{{ e.host }}</td>
                  <td>{{ e.region || '—' }}</td>
                  <td>
                    <span class="badge" :class="statusBadge(e.status)">
                      <span class="dot"></span>{{ e.status }}
                    </span>
                  </td>
                  <td>
                    <button class="btn btn-ghost btn-sm" @click="copy(e.host)">复制</button>
                  </td>
                </tr>
              </tbody>
            </table>
            <div v-else class="empty">管理员尚未配置入口域名</div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
