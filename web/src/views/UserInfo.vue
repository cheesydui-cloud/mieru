<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { copyText, formatBytes, formatBps, statusBadge } from '../api'
import { brand, brandMarkLetter, loadBrand } from '../brand'

const route = useRoute()
const info = ref(null)
const error = ref('')
const toast = ref('')
const loading = ref(true)
let timer

function statusLabel(s) {
  const m = { active: '正常', disabled: '停用', expired: '到期', over_quota: '超流量' }
  return m[s] || s || '—'
}

const remainPct = computed(() => {
  const u = info.value
  if (!u) return 100
  if (!u.traffic_limit_bytes) return 100
  const used = u.traffic_used_bytes || 0
  const left = Math.max(0, u.traffic_limit_bytes - used)
  return Math.round((left / u.traffic_limit_bytes) * 100)
})

const ringStyle = computed(() => ({ '--p': `${remainPct.value}%` }))

const panelTitle = computed(() => info.value?.panel_name || brand.name || 'Mieru')

async function load() {
  const tok = route.params.token
  if (!tok) {
    error.value = '链接无效'
    loading.value = false
    return
  }
  try {
    const res = await fetch(`/api/u/${encodeURIComponent(tok)}`, {
      headers: { Accept: 'application/json' },
      cache: 'no-store',
    })
    const text = await res.text()
    let data = null
    try {
      data = text ? JSON.parse(text) : null
    } catch {
      data = null
    }
    if (!res.ok) {
      throw new Error((data && data.error) || text || res.statusText)
    }
    info.value = data
    error.value = ''
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function copy(text) {
  if (!text) return
  try {
    await copyText(text)
    toast.value = '已复制'
    setTimeout(() => {
      if (toast.value === '已复制') toast.value = ''
    }, 2000)
  } catch {
    toast.value = '复制失败，请手动选中'
  }
}

onMounted(async () => {
  try {
    await loadBrand()
  } catch {
    /* ignore */
  }
  document.title = `${panelTitle.value} · 账号信息`
  await load()
  timer = setInterval(load, 15000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="login-page user-info-page">
    <div class="user-info-wrap">
      <div class="user-info-top">
        <div class="brand" style="padding: 0">
          <div
            v-if="brand.faviconData"
            class="brand-mark brand-mark-img"
            :style="{ backgroundImage: `url(${brand.faviconData})` }"
          />
          <div v-else class="brand-mark">{{ brandMarkLetter(panelTitle) }}</div>
          <div class="brand-text">
            <strong>{{ panelTitle }}</strong>
            <span>账号信息（只读）</span>
          </div>
        </div>
      </div>

      <div v-if="toast" class="toast" @click="toast = ''">{{ toast }}</div>
      <div v-if="error" class="error">{{ error }}</div>
      <div v-else-if="loading" class="muted" style="text-align: center; padding: 40px">加载中…</div>

      <template v-else-if="info">
        <div class="card ring-wrap">
          <div class="ring" :style="ringStyle">
            <div class="ring-inner">
              <strong>{{ info.traffic_limit_bytes ? remainPct + '%' : '∞' }}</strong>
              <span>{{ info.traffic_limit_bytes ? '剩余流量' : '不限流量' }}</span>
            </div>
          </div>
          <div style="flex: 1; min-width: 0">
            <div style="display: flex; gap: 8px; align-items: center; margin-bottom: 10px; flex-wrap: wrap">
              <strong style="font-size: 18px">{{ info.username }}</strong>
              <span class="badge" :class="statusBadge(info.status)">
                <span class="dot"></span>{{ statusLabel(info.status) }}
              </span>
            </div>
            <dl class="kv">
              <dt>到期</dt>
              <dd>{{ info.expire_at || '永久' }}</dd>
              <dt>已用 / 配额</dt>
              <dd>
                {{ formatBytes(info.traffic_used_bytes) }}
                /
                {{ info.traffic_limit_bytes ? formatBytes(info.traffic_limit_bytes) : '不限' }}
              </dd>
              <dt>今日</dt>
              <dd>↓ {{ formatBytes(info.today_down) }} · ↑ {{ formatBytes(info.today_up) }}</dd>
              <dt>实时</dt>
              <dd>
                ↓ {{ formatBps(info.rate?.down_bps) }}
                ·
                ↑ {{ formatBps(info.rate?.up_bps) }}
              </dd>
              <dt v-if="info.route_name">隧道</dt>
              <dd v-if="info.route_name">{{ info.route_name }}</dd>
              <dt v-if="info.entry">入口</dt>
              <dd v-if="info.entry" class="mono">{{ info.entry }}</dd>
              <dt v-if="info.note">备注</dt>
              <dd v-if="info.note">{{ info.note }}</dd>
            </dl>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>订阅链接</h2>
            <div class="row-actions">
              <button class="btn btn-ghost btn-sm" :disabled="!info.subscription" @click="copy(info.subscription)">
                复制订阅
              </button>
              <button class="btn btn-primary btn-sm" :disabled="!info.mihomo_url" @click="copy(info.mihomo_url)">
                复制 Mihomo
              </button>
            </div>
          </div>
          <div class="panel-bd" style="padding: 14px 18px">
            <div class="mono" style="word-break: break-all; color: var(--text-secondary); font-size: 12.5px">
              {{ info.subscription || '—' }}
            </div>
            <p class="muted" style="margin: 10px 0 0; font-size: 12px; line-height: 1.5">
              本页仅展示用量与状态，不含代理密码。如需节点扫码或密码，请联系管理员。
            </p>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.user-info-page {
  place-items: start center;
  padding: 40px 16px 64px;
  min-height: 100vh;
}
.user-info-wrap {
  width: min(720px, 100%);
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.user-info-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.brand-mark-img {
  background-size: cover;
  background-position: center;
  color: transparent !important;
}
</style>
