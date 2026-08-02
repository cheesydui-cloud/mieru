<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useFlash } from '../flash'
import { useRoute } from 'vue-router'
import QRCode from 'qrcode'
import { copyText, formatBytes, formatBps, statusBadge } from '../api'
import { brand, brandMarkLetter, loadBrand } from '../brand'

const route = useRoute()
const info = ref(null)
const error = ref('')
const flash = useFlash()
const loading = ref(true)
const subQR = ref('')
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

const shareURL = computed(() => info.value?.share_url || '')
const mihomoYAML = computed(() => info.value?.mihomo_yaml || '')
const entries = computed(() => (Array.isArray(info.value?.entries) ? info.value.entries : []))

/** Query page: host only, never show :port */
function entryHostOnly(raw) {
  const s = String(raw || '').trim()
  if (!s) return ''
  // [ipv6]:port
  if (s.startsWith('[')) {
    const m = s.match(/^\[([^\]]+)\](?::\d+)?$/)
    return m ? m[1] : s
  }
  // host:port (single colon, not bare ipv6)
  if (/^[^:]+:\d+$/.test(s)) return s.replace(/:\d+$/, '')
  return s
}

const entryDisplay = computed(() => entryHostOnly(info.value?.entry))

async function makeQR(text) {
  if (!text) return ''
  return QRCode.toDataURL(text, {
    width: 260,
    margin: 2,
    color: { dark: '#0f172a', light: '#ffffff' },
    errorCorrectionLevel: 'M',
  })
}

async function refreshQR(url) {
  try {
    subQR.value = url ? await makeQR(url) : ''
  } catch {
    subQR.value = ''
  }
}

watch(shareURL, (url) => {
  refreshQR(url)
})

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
    await refreshQR(data?.share_url || '')
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    loading.value = false
  }
}

function leavePage() {
  // Public capability URL — no session. Close tab if possible, else blank.
  try {
    window.close()
  } catch {
    /* ignore */
  }
  setTimeout(() => {
    try {
      window.location.replace('about:blank')
    } catch {
      window.location.href = 'about:blank'
    }
  }, 120)
}

async function copy(text) {
  if (!text) return
  try {
    await copyText(text)
    flash.ok('已复制')
    setTimeout(() => {
      /* flash auto-clears */
    }, 2000)
  } catch {
    flash.err('复制失败，请手动选中')
  }
}

function downloadYAML() {
  const body = mihomoYAML.value
  if (!body) {
    // fallback: open public mihomo url
    if (info.value?.mihomo_url) {
      window.open(info.value.mihomo_url, '_blank')
      return
    }
    flash.err('暂无 YAML')
    return
  }
  const name = `mihomo-${info.value?.username || 'user'}.yaml`
  const blob = new Blob([body], { type: 'application/x-yaml' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(a.href)
  flash.ok(`已下载 ${name}`)
}

onMounted(async () => {
  try {
    await loadBrand()
  } catch {
    /* ignore */
  }
  document.title = `${panelTitle.value} · 账号信息`
  await load()
  // status/rate refresh; share/QR stable so no need every tick
  timer = setInterval(async () => {
    try {
      const tok = route.params.token
      if (!tok) return
      const res = await fetch(`/api/u/${encodeURIComponent(tok)}`, {
        headers: { Accept: 'application/json' },
        cache: 'no-store',
      })
      if (!res.ok) return
      const data = await res.json()
      // preserve QR if share_url unchanged
      const prevShare = info.value?.share_url
      info.value = data
      if ((data?.share_url || '') !== (prevShare || '')) {
        await refreshQR(data?.share_url || '')
      }
    } catch {
      /* ignore poll errors */
    }
  }, 15000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div class="login-page user-info-page">
    <div class="user-info-wrap">
      <div class="user-info-top" style="display:flex;justify-content:space-between;align-items:center;gap:12px">
        <div class="brand" style="padding: 0">
          <div
            v-if="brand.faviconData"
            class="brand-mark brand-mark-img"
            :style="{ backgroundImage: `url(${brand.faviconData})` }"
          />
          <div v-else class="brand-mark">{{ brandMarkLetter(panelTitle) }}</div>
          <div class="brand-text">
            <strong>{{ panelTitle }}</strong>
            <span>账号信息</span>
          </div>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-sm"
          title="关闭本页（查询页为公开链接，无登录态）"
          @click="leavePage"
        >
          退出
        </button>
      </div>
      <div v-if="error" class="action-feedback err" style="margin:0 0 10px" @click="error = ''">{{ error }}</div>
      <div
        v-if="flash.msg"
        class="action-feedback"
        :class="flash.kind"
        style="margin:0 0 10px"
        @click="flash.clear()"
      >{{ flash.msg }}</div>
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
              <dt v-if="entryDisplay">入口</dt>
              <dd v-if="entryDisplay" class="mono">{{ entryDisplay }}</dd>
              <dt v-if="info.note">备注</dt>
              <dd v-if="info.note">{{ info.note }}</dd>
            </dl>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>扫码 / 节点</h2>
            <div class="row-actions">
              <button class="btn btn-ghost btn-sm" :disabled="!shareURL" @click="copy(shareURL)">复制链接</button>
            </div>
          </div>
          <div class="panel-bd share-block">
            <div class="qr-center">
              <div v-if="subQR" class="qr-box">
                <img :src="subQR" alt="节点二维码" width="260" height="260" />
              </div>
              <div v-else class="muted" style="padding: 16px; text-align: center">
                无法生成二维码（未绑定隧道 / 无前置地址）
              </div>
            </div>
            <div class="field" style="margin-top: 14px">
              <label>节点链接（扫码内容 · mierus://）</label>
              <textarea readonly rows="3" class="mono share-ta" :value="shareURL" />
            </div>
            <div v-if="entries.length > 1" class="field">
              <label>全部入口</label>
              <div v-for="(e, i) in entries" :key="i" class="mono entry-row">
                <span>{{ e.name }} · {{ e.host }}</span>
                <button class="btn btn-link btn-sm" type="button" @click="copy(e.url)">复制</button>
              </div>
            </div>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>Mihomo / Clash Meta YAML</h2>
            <div class="row-actions">
              <button class="btn btn-ghost btn-sm" :disabled="!mihomoYAML" @click="copy(mihomoYAML)">复制 YAML</button>
              <button class="btn btn-primary btn-sm" :disabled="!mihomoYAML && !info.mihomo_url" @click="downloadYAML">
                下载 YAML
              </button>
            </div>
          </div>
          <div class="panel-bd" style="padding: 14px 18px">
            <textarea readonly rows="12" class="mono share-ta" :value="mihomoYAML" />
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>订阅链接</h2>
            <div class="row-actions">
              <button class="btn btn-ghost btn-sm" :disabled="!info.subscription" @click="copy(info.subscription)">
                复制订阅
              </button>
              <button class="btn btn-ghost btn-sm" :disabled="!info.mihomo_url" @click="copy(info.mihomo_url)">
                复制 Mihomo URL
              </button>
            </div>
          </div>
          <div class="panel-bd" style="padding: 14px 18px">
            <div class="mono" style="word-break: break-all; color: var(--text-secondary); font-size: 12.5px">
              {{ info.subscription || '—' }}
            </div>
            <p class="muted" style="margin: 10px 0 0; font-size: 12px; line-height: 1.5">
              本链接可直接扫码导入或下载 YAML，请勿公开转发给无关人员。
              若怀疑泄露，请联系管理员在后台「重置订阅」以作废旧链接。
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
.share-block {
  padding: 16px 18px 18px;
}
.qr-center {
  display: flex;
  justify-content: center;
}
.qr-box {
  border: 1px solid var(--border, #e2e8f0);
  border-radius: 12px;
  padding: 12px;
  background: #fff;
}
.share-ta {
  width: 100%;
  resize: vertical;
  min-height: 72px;
  font-size: 12.5px;
  line-height: 1.45;
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid var(--border, #e2e8f0);
  background: var(--bg-muted, #f8fafc);
  color: var(--text, #0f172a);
}
.entry-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  font-size: 12.5px;
  border-bottom: 1px dashed var(--border, #e2e8f0);
}
.entry-row:last-child {
  border-bottom: 0;
}
</style>
