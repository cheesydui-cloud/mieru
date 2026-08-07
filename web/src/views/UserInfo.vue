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

// Announcements (public query page)
const announcements = ref([])
const popupAnn = ref(null)
const showPopup = ref(false)
const showList = ref(false)
const popupLeft = ref(60)
let popupTimer = null
let popupTick = null

// Client files (public query page downloads)
const clientFiles = ref([])

function clearPopupTimers() {
  if (popupTimer) {
    clearTimeout(popupTimer)
    popupTimer = null
  }
  if (popupTick) {
    clearInterval(popupTick)
    popupTick = null
  }
}

function closePopup() {
  clearPopupTimers()
  showPopup.value = false
}

function openPopup(a, autoCloseSec = 0) {
  if (!a) return
  popupAnn.value = a
  showPopup.value = true
  clearPopupTimers()
  if (autoCloseSec > 0) {
    popupLeft.value = autoCloseSec
    popupTick = setInterval(() => {
      popupLeft.value = Math.max(0, popupLeft.value - 1)
    }, 1000)
    popupTimer = setTimeout(() => {
      closePopup()
    }, autoCloseSec * 1000)
  } else {
    popupLeft.value = 0
  }
}

function openList() {
  showList.value = true
}

function closeList() {
  showList.value = false
}

function openFromList(a) {
  openPopup(a, 0)
}

function fmtAnnTime(t) {
  if (!t) return ''
  try {
    const d = new Date(t)
    if (Number.isNaN(d.getTime())) return String(t).slice(0, 16)
    return d.toLocaleString()
  } catch {
    return String(t).slice(0, 16)
  }
}

async function loadAnnouncements({ autoPopup = false } = {}) {
  try {
    const res = await fetch('/api/announcements', {
      headers: { Accept: 'application/json' },
      cache: 'no-store',
    })
    if (!res.ok) return
    const data = await res.json()
    announcements.value = Array.isArray(data?.items) ? data.items : []
    if (autoPopup && data?.popup) {
      // Every open of query page: auto show popup for 60s
      openPopup(data.popup, 60)
    }
  } catch {
    /* ignore */
  }
}

async function loadClientFiles() {
  try {
    const res = await fetch('/api/files', {
      headers: { Accept: 'application/json' },
      cache: 'no-store',
    })
    if (!res.ok) return
    const data = await res.json()
    clientFiles.value = Array.isArray(data?.items) ? data.items : []
  } catch {
    /* ignore */
  }
}

function downloadClientFile(f) {
  if (!f) return
  const url = f.download_url || `/api/files/${f.id}/download`
  const a = document.createElement('a')
  a.href = url
  a.download = f.filename || f.title || 'download'
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
}

const UI_I18N = {
  zh: {
    accountInfo: '账号信息',
    announcements: '公告',
    exit: '退出',
    loading: '加载中…',
    remainTraffic: '剩余流量',
    unlimitedTraffic: '不限流量',
    expire: '到期',
    permanent: '永久',
    usedQuota: '已用 / 配额',
    unlimited: '不限',
    today: '今日',
    realtime: '实时',
    tunnel: '隧道',
    entry: '入口',
    note: '备注',
    scanNode: '扫码 / 节点',
    copyLink: '复制链接',
    qrFail: '无法生成二维码（未绑定隧道 / 无前置地址）',
    nodeLink: '节点链接（扫码内容 · mierus://）',
    allEntries: '全部入口',
    copy: '复制',
    mihomoYaml: 'Mihomo / Clash Meta YAML（分流）',
    copyYaml: '复制 YAML',
    downloadYaml: '下载 YAML',
    clashSub: 'Clash 分流订阅（国内直连）',
    copyClash: '复制 Clash 分流链接',
    copyMihomo: '复制 Mihomo URL',
    clashHelp:
      'Clash Verge / Windows / Mac 分流用：国内站直连、国外走节点。链接须以 .../mihomo.yaml 结尾。INS/TK 防泄漏请用下方「全局防泄漏」。',
    globalSub: '全局防泄漏（小火箭 / INS·TK）',
    copyGlobal: '复制全局链接',
    copyGlobalYaml: '复制全局 YAML',
    downloadGlobal: '下载全局 YAML',
    globalHelp:
      '专为小火箭全局、INS/TK 防泄漏：无国内直连分流；DNS 仅 1.1.1.1/8.8.8.8 且随代理；关闭 IPv6。Clash 也可订 .../global.yaml 并选全局模式。小火箭若只支持节点列表，请用最下方普通订阅 + 客户端「DNS 经代理」。',
    plainSub: '普通订阅（mierus:// 节点列表）',
    copySub: '复制订阅',
    plainHelp:
      '仅 mierus:// 节点，无 DNS/规则。给小火箭原生订阅/扫码。Clash 请用上方 YAML 链接。请勿公开转发；泄露可让管理员「重置订阅」。',
    close: '关闭',
    autoClose: 's 后自动关闭',
    gotIt: '我知道了',
    annList: '公告列表',
    noAnn: '暂无公告',
    popup: '弹窗',
    files: '文件下载',
    noFiles: '暂无文件',
    download: '下载',
    invalidLink: '链接无效',
    loadFail: '加载失败',
    copied: '已复制',
    copyFail: '复制失败，请手动选中',
    noYaml: '暂无 YAML',
    downloaded: '已下载',
    titleSuffix: '账号信息',
    status: { active: '正常', disabled: '停用', expired: '到期', over_quota: '超流量' },
  },
  en: {
    accountInfo: 'Account',
    announcements: 'Notices',
    exit: 'Close',
    loading: 'Loading…',
    remainTraffic: 'Remaining',
    unlimitedTraffic: 'Unlimited',
    expire: 'Expires',
    permanent: 'Never',
    usedQuota: 'Used / Quota',
    unlimited: 'Unlimited',
    today: 'Today',
    realtime: 'Live',
    tunnel: 'Tunnel',
    entry: 'Entry',
    note: 'Note',
    scanNode: 'QR / Node',
    copyLink: 'Copy link',
    qrFail: 'QR unavailable (no tunnel / entry host)',
    nodeLink: 'Node link (QR content · mierus://)',
    allEntries: 'All entries',
    copy: 'Copy',
    mihomoYaml: 'Mihomo / Clash Meta YAML (split)',
    copyYaml: 'Copy YAML',
    downloadYaml: 'Download YAML',
    clashSub: 'Clash split subscription (CN direct)',
    copyClash: 'Copy Clash split URL',
    copyMihomo: 'Copy Mihomo URL',
    clashHelp:
      'For Clash Verge split tunnel (CN direct). URL must end with /mihomo.yaml. For INS/TK anti-leak use Global below.',
    globalSub: 'Global anti-leak (Shadowrocket / INS·TK)',
    copyGlobal: 'Copy global URL',
    copyGlobalYaml: 'Copy global YAML',
    downloadGlobal: 'Download global YAML',
    globalHelp:
      'Full tunnel: no CN DIRECT rules; DNS is 1.1.1.1/8.8.8.8 with respect-rules; IPv6 off. Clash: subscribe .../global.yaml and use Global mode. Shadowrocket node-only clients: use plain mierus:// below + DNS via proxy.',
    plainSub: 'Plain subscription (mierus:// nodes)',
    copySub: 'Copy subscription',
    plainHelp:
      'mierus:// nodes only (no DNS/rules). For Shadowrocket native sub / QR. Clash should use YAML links above. Do not share publicly.',
    close: 'Close',
    autoClose: 's auto-close',
    gotIt: 'Got it',
    annList: 'Notices',
    noAnn: 'No notices',
    popup: 'Popup',
    files: 'Downloads',
    noFiles: 'No files',
    download: 'Download',
    invalidLink: 'Invalid link',
    loadFail: 'Failed to load',
    copied: 'Copied',
    copyFail: 'Copy failed — select manually',
    noYaml: 'No YAML',
    downloaded: 'Downloaded',
    titleSuffix: 'Account',
    status: { active: 'Active', disabled: 'Disabled', expired: 'Expired', over_quota: 'Over quota' },
  },
}

const locale = computed(() => (info.value?.user_info_locale === 'en' ? 'en' : 'zh'))
const t = computed(() => UI_I18N[locale.value] || UI_I18N.zh)

function statusLabel(s) {
  const m = t.value.status || {}
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
const globalYAML = computed(() => info.value?.global_yaml || '')
const entries = computed(() => (Array.isArray(info.value?.entries) ? info.value.entries : []))
/** Clash Verge / Mihomo remote profile URL (must end with /mihomo.yaml) */
const clashVergeURL = computed(
  () => info.value?.clash_verge_url || info.value?.mihomo_url || '',
)
/** Full-tunnel anti-leak profile (.../global.yaml) */
const globalURL = computed(() => info.value?.global_url || '')

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
    error.value = UI_I18N.zh.invalidLink
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
    error.value = e.message || UI_I18N.zh.loadFail
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
    flash.ok(t.value.copied)
    setTimeout(() => {
      /* flash auto-clears */
    }, 2000)
  } catch {
    flash.err(t.value.copyFail)
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
    flash.err(t.value.noYaml)
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
  flash.ok(`${t.value.downloaded} ${name}`)
}

function downloadGlobalYAML() {
  const body = globalYAML.value
  if (!body) {
    if (globalURL.value) {
      window.open(globalURL.value, '_blank')
      return
    }
    flash.err(t.value.noYaml)
    return
  }
  const name = `global-${info.value?.username || 'user'}.yaml`
  const blob = new Blob([body], { type: 'application/x-yaml' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(a.href)
  flash.ok(`${t.value.downloaded} ${name}`)
}

onMounted(async () => {
  try {
    await loadBrand()
  } catch {
    /* ignore */
  }
  document.title = `${panelTitle.value} · ${t.value.titleSuffix}`
  document.documentElement.lang = locale.value === 'en' ? 'en' : 'zh-CN'
  await Promise.all([load(), loadAnnouncements({ autoPopup: true }), loadClientFiles()])
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
onUnmounted(() => {
  clearInterval(timer)
  clearPopupTimers()
})
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
            <strong class="brand-title" :title="panelTitle">{{ panelTitle }}</strong>
            <i class="brand-rule" aria-hidden="true" />
          </div>
        </div>
        <div class="row-actions" style="align-items:center;gap:8px">
          <button
            type="button"
            class="btn btn-ghost btn-sm ann-btn"
            :title="t.announcements"
            @click="openList"
          >
            {{ t.announcements }}
            <span v-if="announcements.length" class="ann-btn-dot" aria-hidden="true" />
          </button>
          <button
            type="button"
            class="btn btn-ghost btn-sm"
            :title="t.exit"
            @click="leavePage"
          >
            {{ t.exit }}
          </button>
        </div>
      </div>
      <div v-if="error" class="action-feedback err" style="margin:0 0 10px" @click="error = ''">{{ error }}</div>
      <div
        v-if="flash.msg"
        class="action-feedback"
        :class="flash.kind"
        style="margin:0 0 10px"
        @click="flash.clear()"
      >{{ flash.msg }}</div>
      <div v-else-if="loading" class="muted" style="text-align: center; padding: 40px">{{ t.loading }}</div>

      <template v-else-if="info">
        <div class="card ring-wrap">
          <div class="ring" :style="ringStyle">
            <div class="ring-inner">
              <strong>{{ info.traffic_limit_bytes ? remainPct + '%' : '∞' }}</strong>
              <span>{{ info.traffic_limit_bytes ? t.remainTraffic : t.unlimitedTraffic }}</span>
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
              <dt>{{ t.expire }}</dt>
              <dd>{{ info.expire_at || t.permanent }}</dd>
              <dt>{{ t.usedQuota }}</dt>
              <dd>
                {{ formatBytes(info.traffic_used_bytes) }}
                /
                {{ info.traffic_limit_bytes ? formatBytes(info.traffic_limit_bytes) : t.unlimited }}
              </dd>
              <dt>{{ t.today }}</dt>
              <dd>↓ {{ formatBytes(info.today_down) }} · ↑ {{ formatBytes(info.today_up) }}</dd>
              <dt>{{ t.realtime }}</dt>
              <dd>
                ↓ {{ formatBps(info.rate?.down_bps) }}
                ·
                ↑ {{ formatBps(info.rate?.up_bps) }}
              </dd>
              <dt v-if="info.route_name">{{ t.tunnel }}</dt>
              <dd v-if="info.route_name">{{ info.route_name }}</dd>
              <dt v-if="entryDisplay">{{ t.entry }}</dt>
              <dd v-if="entryDisplay" class="mono">{{ entryDisplay }}</dd>
              <dt v-if="info.note">{{ t.note }}</dt>
              <dd v-if="info.note">{{ info.note }}</dd>
            </dl>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>{{ t.scanNode }}</h2>
            <div class="row-actions">
              <button class="btn btn-ghost btn-sm" :disabled="!shareURL" @click="copy(shareURL)">{{ t.copyLink }}</button>
            </div>
          </div>
          <div class="panel-bd share-block">
            <div class="qr-center">
              <div v-if="subQR" class="qr-box">
                <img :src="subQR" :alt="t.scanNode" width="260" height="260" />
              </div>
              <div v-else class="muted" style="padding: 16px; text-align: center">
                {{ t.qrFail }}
              </div>
            </div>
            <div class="field" style="margin-top: 14px">
              <label>{{ t.nodeLink }}</label>
              <textarea readonly rows="3" class="mono share-ta" :value="shareURL" />
            </div>
            <div v-if="entries.length > 1" class="field">
              <label>{{ t.allEntries }}</label>
              <div v-for="(e, i) in entries" :key="i" class="mono entry-row">
                <span>{{ e.name }} · {{ e.host }}</span>
                <button class="btn btn-link btn-sm" type="button" @click="copy(e.url)">{{ t.copy }}</button>
              </div>
            </div>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>{{ t.mihomoYaml }}</h2>
            <div class="row-actions">
              <button class="btn btn-ghost btn-sm" :disabled="!mihomoYAML" @click="copy(mihomoYAML)">{{ t.copyYaml }}</button>
              <button class="btn btn-primary btn-sm" :disabled="!mihomoYAML && !info.mihomo_url" @click="downloadYAML">
                {{ t.downloadYaml }}
              </button>
            </div>
          </div>
          <div class="panel-bd" style="padding: 14px 18px">
            <textarea readonly rows="12" class="mono share-ta" :value="mihomoYAML" />
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>{{ t.clashSub }}</h2>
            <div class="row-actions">
              <button
                class="btn btn-primary btn-sm"
                :disabled="!clashVergeURL"
                @click="copy(clashVergeURL)"
              >
                {{ t.copyClash }}
              </button>
              <button class="btn btn-ghost btn-sm" :disabled="!clashVergeURL" @click="copy(clashVergeURL)">
                {{ t.copyMihomo }}
              </button>
            </div>
          </div>
          <div class="panel-bd" style="padding: 14px 18px">
            <div class="mono" style="word-break: break-all; color: var(--text-secondary); font-size: 12.5px">
              {{ clashVergeURL || '—' }}
            </div>
            <p class="muted" style="margin: 10px 0 0; font-size: 12px; line-height: 1.5">
              {{ t.clashHelp }}
            </p>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>{{ t.globalSub }}</h2>
            <div class="row-actions">
              <button class="btn btn-primary btn-sm" :disabled="!globalURL" @click="copy(globalURL)">
                {{ t.copyGlobal }}
              </button>
              <button class="btn btn-ghost btn-sm" :disabled="!globalYAML" @click="copy(globalYAML)">
                {{ t.copyGlobalYaml }}
              </button>
              <button
                class="btn btn-ghost btn-sm"
                :disabled="!globalYAML && !globalURL"
                @click="downloadGlobalYAML"
              >
                {{ t.downloadGlobal }}
              </button>
            </div>
          </div>
          <div class="panel-bd" style="padding: 14px 18px">
            <div class="mono" style="word-break: break-all; color: var(--text-secondary); font-size: 12.5px">
              {{ globalURL || '—' }}
            </div>
            <textarea
              v-if="globalYAML"
              readonly
              rows="8"
              class="mono share-ta"
              style="margin-top: 10px"
              :value="globalYAML"
            />
            <p class="muted" style="margin: 10px 0 0; font-size: 12px; line-height: 1.5">
              {{ t.globalHelp }}
            </p>
          </div>
        </div>

        <div class="panel">
          <div class="panel-hd">
            <h2>{{ t.plainSub }}</h2>
            <div class="row-actions">
              <button class="btn btn-ghost btn-sm" :disabled="!info.subscription" @click="copy(info.subscription)">
                {{ t.copySub }}
              </button>
            </div>
          </div>
          <div class="panel-bd" style="padding: 14px 18px">
            <div class="mono" style="word-break: break-all; color: var(--text-secondary); font-size: 12.5px">
              {{ info.subscription || '—' }}
            </div>
            <p class="muted" style="margin: 10px 0 0; font-size: 12px; line-height: 1.5">
              {{ t.plainHelp }}
            </p>
          </div>
        </div>

        <div v-if="clientFiles.length" class="panel">
          <div class="panel-hd">
            <h2>{{ t.files }}</h2>
          </div>
          <div class="panel-bd" style="padding: 0">
            <div
              v-for="f in clientFiles"
              :key="f.id"
              class="file-row"
            >
              <div class="file-meta">
                <div class="file-title">{{ f.title || f.filename }}</div>
                <div class="file-sub muted mono">
                  {{ f.filename }}
                  <span v-if="f.size"> · {{ formatBytes(f.size) }}</span>
                </div>
              </div>
              <button type="button" class="btn btn-primary btn-sm" @click="downloadClientFile(f)">
                {{ t.download }}
              </button>
            </div>
          </div>
        </div>
      </template>
    </div>

    <!-- Auto popup announcement (60s) -->
    <div v-if="showPopup && popupAnn" class="ann-popup-mask" @click.self="closePopup">
      <div class="ann-popup" role="dialog" aria-modal="true">
        <div class="ann-popup-hd">
          <h3>{{ popupAnn.title }}</h3>
          <button type="button" class="btn btn-ghost btn-sm" @click="closePopup">{{ t.close }}</button>
        </div>
        <div class="ann-popup-bd">{{ popupAnn.body }}</div>
        <div class="ann-popup-ft">
          <span v-if="popupLeft > 0" class="ann-popup-timer">{{ popupLeft }}{{ t.autoClose }}</span>
          <span v-else class="ann-popup-timer" />
          <button type="button" class="btn btn-primary btn-sm" @click="closePopup">{{ t.gotIt }}</button>
        </div>
      </div>
    </div>

    <!-- Announcement list -->
    <div v-if="showList" class="ann-list-mask" @click.self="closeList">
      <div class="ann-list" role="dialog" aria-modal="true">
        <div class="ann-list-hd">
          <h3>{{ t.annList }}</h3>
          <button type="button" class="btn btn-ghost btn-sm" @click="closeList">{{ t.close }}</button>
        </div>
        <div class="ann-list-bd">
          <div v-if="!announcements.length" class="empty" style="padding:28px 16px">{{ t.noAnn }}</div>
          <div
            v-for="a in announcements"
            :key="a.id"
            class="ann-item"
            style="cursor:pointer"
            @click="openFromList(a)"
          >
            <div class="ann-item-title">
              <span>{{ a.title }}</span>
              <span v-if="a.popup" class="badge ok" style="font-size:11px">{{ t.popup }}</span>
            </div>
            <div class="ann-item-body">{{ a.body }}</div>
            <div class="ann-item-meta">{{ fmtAnnTime(a.updated_at || a.created_at) }}</div>
          </div>
        </div>
      </div>
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

.user-info-top .brand {
  min-width: 0;
  flex: 1 1 auto;
  align-items: center;
  max-width: calc(100% - 160px);
}
.user-info-top .brand-text {
  position: relative;
  min-width: 0;
  max-width: 100%;
  gap: 0;
  padding-bottom: 0;
}
/* full name on query page — no sidebar ellipsis */
.user-info-top .brand-title {
  display: block;
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.02em;
  line-height: 1.25;
  color: var(--text);
  white-space: normal;
  overflow: visible;
  text-overflow: unset;
  max-width: none;
  word-break: break-word;
}
.user-info-top .brand-rule {
  display: block;
  width: 100%;
  max-width: 100%;
  height: 3px;
  margin-top: 7px;
  background: var(--accent);
  border-radius: 1px;
}
.user-info-page .card,
.user-info-page .panel {
  border-radius: 4px;
  box-shadow: none;
}
.user-info-page .ring-wrap {
  border-radius: 4px;
}
.file-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border, #e2e8f0);
}
.file-row:last-child {
  border-bottom: 0;
}
.file-meta {
  min-width: 0;
  flex: 1;
}
.file-title {
  font-weight: 600;
  font-size: 14px;
  line-height: 1.35;
  word-break: break-word;
}
.file-sub {
  margin-top: 3px;
  font-size: 12px;
  word-break: break-all;
}
</style>
