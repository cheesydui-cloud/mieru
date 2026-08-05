<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useFlash } from '../flash'
import { useRouter } from 'vue-router'
import { api, formatBytes, statusBadge } from '../api'

const router = useRouter()
const diag = ref(null)
const flash = useFlash()
const rebuilding = ref(false)
const rebuildStatus = ref(null)
let timer

async function load() {
  try {
    const [d, rb] = await Promise.all([
      api('/api/admin/diagnose'),
      api('/api/admin/rebuild-status').catch(() => null),
    ])
    diag.value = d
    rebuildStatus.value = d?.rebuild || rb || null
    flash.clear()
  } catch (e) {
    flash.err(e.message)
  }
}

function fmtRebuildShort(rb) {
  if (!rb || !rb.at) return '尚未重建'
  const age = typeof rb.age_sec === 'number' ? rb.age_sec : null
  const ageTxt =
    age == null ? '' : age < 60 ? `${age}s 前` : age < 3600 ? `${Math.round(age / 60)} 分前` : `${Math.round(age / 3600)} 小时前`
  const st = rb.ok === false ? '失败' : '成功'
  return ageTxt ? `${st} · ${ageTxt}` : st
}

const nodes = computed(() => diag.value?.nodes || [])
const fronts = computed(() =>
  nodes.value.filter((n) => n.role === 'relay' || n.role === 'entry'),
)
const exits = computed(() =>
  nodes.value.filter((n) => n.role === 'exit' || n.role === 'hybrid'),
)
const tunnelEdges = computed(() => diag.value?.tunnel_edges || [])
const stats = computed(() => diag.value?.stats || {})
const todayTotal = computed(() => Number(stats.value.today_total || 0))
const todayUp = computed(() => Number(stats.value.today_up || 0))
const todayDown = computed(() => Number(stats.value.today_down || 0))
const monthTotal = computed(() => Number(stats.value.month_total || 0))
const monthUp = computed(() => Number(stats.value.month_up || 0))
const monthDown = computed(() => Number(stats.value.month_down || 0))

/** 24 local hours 0–23 for today's traffic curve */
const hourlyPoints = computed(() => {
  const raw = diag.value?.traffic_hourly
  const base = Array.from({ length: 24 }, (_, h) => ({
    hour: h,
    up: 0,
    down: 0,
    total: 0,
  }))
  if (!Array.isArray(raw)) return base
  for (const p of raw) {
    const h = Number(p.hour)
    if (h >= 0 && h < 24) {
      const up = Number(p.up || 0)
      const down = Number(p.down || 0)
      base[h] = { hour: h, up, down, total: Number(p.total != null ? p.total : up + down) }
    }
  }
  return base
})

const hourlyMax = computed(() => {
  let m = 0
  for (const p of hourlyPoints.value) {
    if (p.total > m) m = p.total
  }
  // nice headroom; when empty still show a readable axis
  if (m <= 0) return 4 * 1024 * 1024 * 1024 // 4 GiB placeholder scale like clean empty charts
  return m * 1.15
})

const chartHover = ref(null)
const chartTip = ref({ x: 0, y: 0, show: false })

const chartLayout = {
  w: 960,
  h: 280,
  padL: 48,
  padR: 18,
  padT: 18,
  padB: 36,
}

function chartX(h) {
  const { w, padL, padR } = chartLayout
  const inner = w - padL - padR
  return padL + (h / 23) * inner
}

function chartY(v) {
  const { h, padT, padB } = chartLayout
  const inner = h - padT - padB
  const max = hourlyMax.value || 1
  return padT + inner * (1 - Math.min(v, max) / max)
}

const chartLinePath = computed(() => {
  const pts = hourlyPoints.value
  if (!pts.length) return ''
  return pts
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${chartX(p.hour).toFixed(2)} ${chartY(p.total).toFixed(2)}`)
    .join(' ')
})

const chartAreaPath = computed(() => {
  const pts = hourlyPoints.value
  if (!pts.length) return ''
  const { h, padB } = chartLayout
  const baseY = h - padB
  const line = pts
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${chartX(p.hour).toFixed(2)} ${chartY(p.total).toFixed(2)}`)
    .join(' ')
  const lastX = chartX(pts[pts.length - 1].hour).toFixed(2)
  const firstX = chartX(pts[0].hour).toFixed(2)
  return `${line} L ${lastX} ${baseY} L ${firstX} ${baseY} Z`
})

const yTicks = computed(() => {
  const max = hourlyMax.value
  const vals = [0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(max * f))
  const uniq = []
  for (const v of vals) {
    if (!uniq.length || uniq[uniq.length - 1] !== v) uniq.push(v)
  }
  return uniq.map((v) => ({ v, y: chartY(v), label: formatBytes(v) }))
})

// every hour label like reference 00:00 … 23:00
const xTicks = Array.from({ length: 24 }, (_, h) => h)

function hourLabel(h) {
  return String(h).padStart(2, '0') + ':00'
}

function onChartMove(ev) {
  const svg = ev.currentTarget
  const rect = svg.getBoundingClientRect()
  const { w, padL, padR, padT } = chartLayout
  const x = ((ev.clientX - rect.left) / rect.width) * w
  const inner = w - padL - padR
  let h = Math.round(((x - padL) / inner) * 23)
  if (h < 0) h = 0
  if (h > 23) h = 23
  const pt = hourlyPoints.value[h]
  chartHover.value = pt
  // tip position in SVG coords, keep inside
  let tipX = chartX(h)
  let tipY = chartY(pt.total) - 12
  if (tipY < padT + 56) tipY = chartY(pt.total) + 56
  if (tipX < padL + 90) tipX = padL + 90
  if (tipX > w - padR - 90) tipX = w - padR - 90
  chartTip.value = { x: tipX, y: tipY, show: true }
}

function onChartLeave() {
  chartHover.value = null
  chartTip.value = { x: 0, y: 0, show: false }
}

const onlineCount = computed(() => nodes.value.filter((n) => n.status === 'online').length)
const offlineCount = computed(() => Math.max(0, nodes.value.length - onlineCount.value))

const monthBarPct = computed(() => {
  if (!monthTotal.value) return 0
  return Math.min(100, Math.round((todayTotal.value / monthTotal.value) * 100))
})
const onlinePct = computed(() => {
  if (!nodes.value.length) return 0
  return Math.round((onlineCount.value / nodes.value.length) * 100)
})

const allIssueItems = computed(() => {
  const items = []
  for (const it of diag.value?.global_issue_items || []) {
    items.push(it)
  }
  if (!items.length) {
    for (const t of diag.value?.global_issues || []) {
      items.push({ text: t, href: '/nodes', kind: 'global' })
    }
  }
  for (const node of nodes.value) {
    if (node.issue_items?.length) {
      for (const it of node.issue_items) {
        items.push({
          ...it,
          text: `${node.name}：${it.text}`,
        })
      }
    } else {
      for (const t of node.issues || []) {
        const href =
          node.role === 'exit' || node.role === 'hybrid'
            ? '/nodes?tab=exit'
            : node.role === 'relay' || node.role === 'entry'
              ? '/nodes?tab=front'
              : '/nodes'
        items.push({ text: `${node.name}：${t}`, href, kind: 'node', node_id: node.id })
      }
    }
  }
  return items
})

const issueCount = computed(() => allIssueItems.value.length)

/** Compact alert chips — only non-zero counts, clickable */
const alertChips = computed(() => {
  const s = stats.value || {}
  const chips = []
  if (offlineCount.value > 0) {
    chips.push({ key: 'offline', label: '离线节点', value: offlineCount.value, to: '/nodes', tone: 'err' })
  }
  if (s.agent_behind > 0) {
    chips.push({ key: 'behind', label: 'Agent 可升', value: s.agent_behind, to: '/nodes', tone: 'warn' })
  }
  if (s.config_stale > 0) {
    chips.push({ key: 'stale', label: '配置未生效', value: s.config_stale, to: '/nodes', tone: 'warn' })
  }
  if (s.traffic_silent > 0) {
    chips.push({
      key: 'silent',
      label: '计量沉默',
      value: s.traffic_silent,
      to: { path: '/nodes', query: { tab: 'exit' } },
      tone: 'warn',
    })
  }
  if (s.expiring_soon > 0) {
    chips.push({ key: 'expiring', label: '3 天内到期', value: s.expiring_soon, to: '/users', tone: 'warn' })
  }
  if (s.over_quota > 0) {
    chips.push({ key: 'quota', label: '已超流量', value: s.over_quota, to: '/users', tone: 'warn' })
  }
  if (s.expired_users > 0) {
    chips.push({ key: 'expired', label: '已到期', value: s.expired_users, to: '/users', tone: 'err' })
  }
  return chips
})

function roleLabel(role) {
  const m = { relay: '前置', entry: '前置', exit: '落地', hybrid: '混合' }
  return m[role] || role || '—'
}

function statusLabel(s) {
  if (s === 'online') return '在线'
  if (s === 'degraded') return '异常'
  if (s === 'offline') return '离线'
  return s || '未知'
}

function healthLabel(h) {
  const m = { ok: '通', degraded: '部分通', down: '不通', unknown: '未测' }
  return m[h] || h || '未测'
}

function healthClass(h) {
  if (h === 'ok') return 'ok'
  if (h === 'degraded') return 'warn'
  if (h === 'down') return 'err'
  return ''
}

function edgeTone(e) {
  if (e.health === 'ok') return 'ok'
  if (e.health === 'degraded') return 'warn'
  if (e.health === 'down') return 'err'
  return ''
}

function goIssue(it) {
  if (it?.href) router.push(it.href)
}

function goRoute() {
  router.push('/routes')
}

function goChip(chip) {
  if (chip?.to) router.push(chip.to)
}

async function rebuild() {
  if (!confirm('强制重建全部节点配置？平时改配置/升级已会自动下发。')) return
  rebuilding.value = true
  try {
    const res = await api('/api/admin/rebuild', { method: 'POST' })
    rebuildStatus.value = res?.rebuild || rebuildStatus.value
    flash.ok('已重建全部节点配置')
    await load()
  } catch (e) {
    flash.err(e.message)
  } finally {
    rebuilding.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 8000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div
    v-if="flash.msg"
    class="action-feedback page-action-feedback"
    :class="flash.kind"
    @click="flash.clear()"
  >
    {{ flash.msg }}
  </div>

  <!-- Top toolbar -->
  <div class="dash-toolbar">
    <div class="dash-toolbar-left">
      <h2 class="dash-title">总览</h2>
      <span class="muted dash-sub">
        {{ fronts.length }} 前置 · {{ exits.length }} 落地 · {{ diag?.enabled_routes || 0 }} 隧道
      </span>
    </div>
    <div class="row-actions">
      <span class="muted" style="font-size:12px" :title="fmtRebuildShort(rebuildStatus)">
        重建 {{ fmtRebuildShort(rebuildStatus) }}
      </span>
      <button class="btn btn-ghost btn-sm" type="button" @click="load">刷新</button>
      <button
        class="btn btn-ghost btn-sm"
        type="button"
        :disabled="rebuilding"
        title="应急手动重建"
        @click="rebuild"
      >
        {{ rebuilding ? '重建中…' : '重建配置' }}
      </button>
    </div>
  </div>

  <!-- KPI row (reference-style clean cards) -->
  <div class="dash-kpi" v-if="diag">
    <button type="button" class="kpi-card" @click="router.push('/users')">
      <div class="kpi-top">
        <span class="kpi-label">总流量</span>
        <span class="kpi-ico teal" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 18V6M10 18V10M16 18V8M20 18H3"/></svg>
        </span>
      </div>
      <div class="kpi-value">{{ formatBytes(monthTotal) }}</div>
      <div class="kpi-foot">
        <span>本月 · 每月 1 日 0 点重置</span>
      </div>
      <div class="kpi-bar"><i :style="{ width: Math.min(100, monthBarPct || 8) + '%' }" /></div>
      <div class="kpi-meta">↓ {{ formatBytes(monthDown) }} · ↑ {{ formatBytes(monthUp) }}</div>
    </button>

    <button type="button" class="kpi-card" @click="router.push('/users')">
      <div class="kpi-top">
        <span class="kpi-label">今日流量</span>
        <span class="kpi-ico mint" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 16l5-5 4 4 7-8"/><path d="M14 7h6v6"/></svg>
        </span>
      </div>
      <div class="kpi-value">{{ formatBytes(todayTotal) }}</div>
      <div class="kpi-foot">
        <span>↓ {{ formatBytes(todayDown) }} · ↑ {{ formatBytes(todayUp) }}</span>
        <span class="kpi-chip">今日</span>
      </div>
      <div class="kpi-bar"><i :style="{ width: (todayTotal ? 72 : 6) + '%' }" /></div>
    </button>

    <button type="button" class="kpi-card" @click="router.push('/nodes')">
      <div class="kpi-top">
        <span class="kpi-label">节点在线</span>
        <span class="kpi-ico violet" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3.5" y="4" width="7" height="7" rx="1.5"/><rect x="13.5" y="4" width="7" height="7" rx="1.5"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.5"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.5"/></svg>
        </span>
      </div>
      <div class="kpi-value">{{ onlineCount }}<span class="kpi-slash"> / {{ nodes.length }}</span></div>
      <div class="kpi-foot">
        <span>{{ offlineCount ? offlineCount + ' 离线' : '全部在线' }}</span>
      </div>
      <div class="kpi-bar"><i :style="{ width: onlinePct + '%' }" /></div>
    </button>

    <button type="button" class="kpi-card" :class="{ warn: issueCount }" @click="issueCount ? null : router.push('/nodes')">
      <div class="kpi-top">
        <span class="kpi-label">待处理</span>
        <span class="kpi-ico sand" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 8v5"/><circle cx="12" cy="16.5" r="0.9" fill="currentColor" stroke="none"/><path d="M10.2 4.8h3.6L20 18.2H4L10.2 4.8z"/></svg>
        </span>
      </div>
      <div class="kpi-value">{{ issueCount }}</div>
      <div class="kpi-foot">
        <span>{{ issueCount ? '点击下方列表处理' : '系统正常' }}</span>
      </div>
      <div class="kpi-bar"><i :style="{ width: issueCount ? Math.min(100, 20 + issueCount * 12) + '%' : '8%' }" /></div>
    </button>
  </div>

  <!-- 24h traffic chart -->
  <div class="panel dash-traffic" v-if="diag">
    <div class="panel-hd dash-traffic-hd">
      <div class="dash-traffic-title">
        <span class="dash-traffic-dot" aria-hidden="true" />
        <h2>24小时流量统计</h2>
      </div>
      <div class="muted" style="font-size:12px">本地 0–23 点 · 今日合计 {{ formatBytes(todayTotal) }}</div>
    </div>
    <div class="panel-bd dash-chart-wrap">
      <svg
        class="dash-chart"
        :viewBox="`0 0 ${chartLayout.w} ${chartLayout.h}`"
        preserveAspectRatio="none"
        @mousemove="onChartMove"
        @mouseleave="onChartLeave"
        role="img"
        aria-label="今日按小时流量曲线"
      >
        <!-- horizontal grid -->
        <line
          v-for="t in yTicks"
          :key="'yg' + t.v"
          :x1="chartLayout.padL"
          :x2="chartLayout.w - chartLayout.padR"
          :y1="t.y"
          :y2="t.y"
          class="dash-chart-grid"
        />
        <!-- vertical grid every hour -->
        <line
          v-for="h in xTicks"
          :key="'xg' + h"
          :x1="chartX(h)"
          :x2="chartX(h)"
          :y1="chartLayout.padT"
          :y2="chartLayout.h - chartLayout.padB"
          class="dash-chart-vgrid"
        />
        <!-- axes -->
        <line
          :x1="chartLayout.padL"
          :y1="chartLayout.padT"
          :x2="chartLayout.padL"
          :y2="chartLayout.h - chartLayout.padB"
          class="dash-chart-axis"
        />
        <line
          :x1="chartLayout.padL"
          :y1="chartLayout.h - chartLayout.padB"
          :x2="chartLayout.w - chartLayout.padR"
          :y2="chartLayout.h - chartLayout.padB"
          class="dash-chart-axis"
        />
        <text
          v-for="t in yTicks"
          :key="'yl' + t.v"
          :x="chartLayout.padL - 8"
          :y="t.y + 3"
          class="dash-chart-label"
          text-anchor="end"
        >{{ t.label }}</text>
        <text
          v-for="h in xTicks"
          :key="'xl' + h"
          :x="chartX(h)"
          :y="chartLayout.h - 10"
          class="dash-chart-xlabel"
          text-anchor="middle"
        >{{ hourLabel(h) }}</text>
        <path :d="chartAreaPath" class="dash-chart-area" />
        <path :d="chartLinePath" class="dash-chart-line" fill="none" />
        <circle
          v-for="p in hourlyPoints"
          :key="'p' + p.hour"
          :cx="chartX(p.hour)"
          :cy="chartY(p.total)"
          r="0"
          class="dash-chart-hit"
        />
        <template v-if="chartHover">
          <line
            :x1="chartX(chartHover.hour)"
            :x2="chartX(chartHover.hour)"
            :y1="chartLayout.padT"
            :y2="chartLayout.h - chartLayout.padB"
            class="dash-chart-guide"
          />
          <circle
            :cx="chartX(chartHover.hour)"
            :cy="chartY(chartHover.total)"
            r="5"
            class="dash-chart-focus"
          />
          <circle
            :cx="chartX(chartHover.hour)"
            :cy="chartY(chartHover.total)"
            r="2.5"
            class="dash-chart-focus-core"
          />
        </template>
      </svg>
      <div
        v-if="chartTip.show && chartHover"
        class="dash-chart-tip"
        :style="{
          left: (chartTip.x / chartLayout.w * 100) + '%',
          top: (chartTip.y / chartLayout.h * 100) + '%',
        }"
      >
        <div>时间: {{ hourLabel(chartHover.hour) }}</div>
        <div class="tip-val">流量: {{ formatBytes(chartHover.total) }}</div>
        <div class="tip-sub">↓ {{ formatBytes(chartHover.down) }} · ↑ {{ formatBytes(chartHover.up) }}</div>
      </div>
    </div>
  </div>

  <!-- Alert chips: only when something needs attention -->
  <div v-if="alertChips.length" class="dash-alerts">
    <button
      v-for="c in alertChips"
      :key="c.key"
      type="button"
      class="dash-alert-chip"
      :class="c.tone"
      @click="goChip(c)"
    >
      <strong>{{ c.value }}</strong>
      <span>{{ c.label }}</span>
    </button>
  </div>

  <!-- Issues panel: only when non-empty -->
  <div v-if="allIssueItems.length" class="panel dash-issues">
    <div class="panel-hd">
      <h2>待处理问题</h2>
      <span class="badge warn">{{ allIssueItems.length }}</span>
    </div>
    <ul class="dash-issue-list">
      <li
        v-for="(iss, i) in allIssueItems"
        :key="'i' + i"
        class="dash-issue-item"
        @click="goIssue(iss)"
      >
        <span class="dash-issue-text">{{ iss.text }}</span>
        <span v-if="iss.href" class="muted dash-issue-go">查看</span>
      </li>
    </ul>
  </div>

  <!-- Tunnel topology -->
  <div class="panel">
    <div class="panel-hd">
      <h2>隧道拓扑</h2>
      <button class="btn btn-ghost btn-sm" type="button" @click="router.push('/routes')">管理隧道</button>
    </div>

    <div v-if="tunnelEdges.length" class="topo-edges">
      <div
        v-for="e in tunnelEdges"
        :key="e.route_id"
        class="topo-edge"
        :class="edgeTone(e)"
        @click="goRoute(e.route_id)"
      >
        <div class="te-row">
          <div class="te-name">{{ e.name }}</div>
          <span class="badge" :class="healthClass(e.health)">{{ healthLabel(e.health) }}</span>
        </div>
        <div class="te-path mono">
          <span>{{ e.front_name || '前置' }}</span>
          <span class="muted">
            {{ e.front_host || '?' }}{{ e.front_port ? ':' + e.front_port : '' }}
          </span>
          <span class="te-arrow">→</span>
          <span>{{ e.exit_name || '落地' }}</span>
          <span class="muted">mita {{ e.exit_port || '?' }}</span>
        </div>
        <div class="te-meta">
          <span class="muted">用户 {{ e.user_count || 0 }}</span>
        </div>
      </div>
    </div>
    <div v-else class="empty">
      还没有启用隧道
      <div class="row-actions" style="margin-top:12px;justify-content:center">
        <button class="btn btn-primary btn-sm" type="button" @click="router.push('/nodes')">去节点</button>
        <button class="btn btn-ghost btn-sm" type="button" @click="router.push('/routes')">去隧道</button>
      </div>
    </div>
  </div>

  <!-- Compact node health (slim, not a full ops table) -->
  <div class="panel" v-if="nodes.length">
    <div class="panel-hd">
      <h2>节点状态</h2>
      <button class="btn btn-ghost btn-sm" type="button" @click="router.push('/nodes')">全部节点</button>
    </div>
    <div class="panel-bd">
      <table class="data dash-node-table">
        <thead>
          <tr>
            <th>名称</th>
            <th>角色</th>
            <th>状态</th>
            <th>地址</th>
            <th>Agent</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="n in nodes"
            :key="n.id"
            class="route-row"
            @click="
              router.push(
                n.role === 'exit' || n.role === 'hybrid'
                  ? { path: '/nodes', query: { tab: 'exit' } }
                  : n.role === 'relay' || n.role === 'entry'
                    ? { path: '/nodes', query: { tab: 'front' } }
                    : '/nodes',
              )
            "
          >
            <td>
              <div class="name-link">{{ n.name }}</div>
            </td>
            <td><span class="badge">{{ roleLabel(n.role) }}</span></td>
            <td>
              <span class="badge" :class="statusBadge(n.status)">
                <span class="dot"></span>{{ statusLabel(n.status) }}
              </span>
            </td>
            <td class="mono" style="font-size:12px">
              {{ n.dial_host || n.public_ip || '—' }}
              <span v-if="n.public_port">:{{ n.public_port }}</span>
            </td>
            <td class="mono" style="font-size:12px">
              {{ n.agent_version ? 'v' + n.agent_version : '—' }}
              <span v-if="n.config_stale || n.version_behind" class="row-meta warn">
                {{ n.config_stale ? '配置未生效' : '可升级' }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.dash-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 2px;
}
.dash-toolbar-left {
  display: flex;
  align-items: baseline;
  gap: 12px;
  min-width: 0;
}
.dash-title {
  margin: 0;
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.02em;
  color: var(--text);
}
.dash-sub {
  font-size: 12.5px;
}

.dash-alerts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.dash-alert-chip {
  appearance: none;
  border: 1px solid var(--border-line);
  background: var(--bg-surface);
  border-radius: 999px;
  padding: 6px 12px;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-size: 12.5px;
  color: var(--text-secondary);
  transition: background 120ms ease, border-color 120ms ease;
}
.dash-alert-chip strong {
  font-variant-numeric: tabular-nums;
  font-weight: 650;
  color: var(--text);
}
.dash-alert-chip:hover {
  background: var(--bg-hover);
}
.dash-alert-chip.warn {
  border-color: rgba(180, 83, 9, 0.28);
  background: var(--warning-soft);
}
.dash-alert-chip.warn strong {
  color: var(--warning);
}
.dash-alert-chip.err {
  border-color: rgba(185, 28, 28, 0.28);
  background: var(--danger-soft);
}
.dash-alert-chip.err strong {
  color: var(--danger);
}

.dash-issues .panel-hd {
  align-items: center;
}
.dash-issue-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.dash-issue-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 11px 16px;
  border-top: 1px solid var(--border);
  cursor: pointer;
  font-size: 13px;
  color: var(--text);
}
.dash-issue-item:first-child {
  border-top: 0;
}
.dash-issue-item:hover {
  background: var(--bg-hover);
}
.dash-issue-text {
  min-width: 0;
  line-height: 1.45;
  word-break: break-word;
}
.dash-issue-go {
  flex-shrink: 0;
  font-size: 12px;
}

.topo-edges {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px 16px;
}
.topo-edge {
  border: 1px solid var(--border-line);
  border-radius: var(--radius);
  padding: 12px 14px;
  background: var(--bg-surface);
  cursor: pointer;
  display: grid;
  gap: 6px;
}
.topo-edge:hover {
  background: var(--bg-hover);
}
.topo-edge.ok {
  border-color: rgba(21, 128, 61, 0.28);
}
.topo-edge.warn {
  border-color: rgba(180, 83, 9, 0.32);
}
.topo-edge.err {
  border-color: rgba(185, 28, 28, 0.32);
}
.te-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.te-name {
  font-weight: 650;
  font-size: 13.5px;
}
.te-path {
  font-size: 12.5px;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.te-arrow {
  font-weight: 700;
  color: var(--text-muted);
}
.te-meta {
  font-size: 12px;
}

.dash-node-table td,
.dash-node-table th {
  padding: 9px 12px;
}



/* ── Dashboard KPI + 24h chart (reference-inspired, teal ops) ── */
.dash-kpi {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
  margin: 4px 0 12px;
}
@media (max-width: 1100px) {
  .dash-kpi { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 560px) {
  .dash-kpi { grid-template-columns: 1fr; }
}
.kpi-card {
  appearance: none;
  text-align: left;
  border: 1px solid var(--border-line);
  background: var(--bg-surface);
  border-radius: 12px;
  padding: 16px 16px 14px;
  cursor: pointer;
  transition: border-color 120ms ease, background 120ms ease;
  min-height: 132px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.kpi-card:hover {
  border-color: var(--border-strong);
  background: #fff;
}
.kpi-card.warn {
  border-color: rgba(180, 83, 9, 0.28);
}
.kpi-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.kpi-label {
  font-size: 13px;
  color: var(--text-secondary);
  font-weight: 500;
}
.kpi-ico {
  width: 32px;
  height: 32px;
  border-radius: 10px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
}
.kpi-ico.teal { background: rgba(15, 118, 110, 0.12); color: #0f766e; }
.kpi-ico.mint { background: rgba(21, 128, 61, 0.12); color: #15803d; }
.kpi-ico.violet { background: rgba(15, 118, 110, 0.10); color: #0f766e; }
.kpi-ico.sand { background: rgba(180, 83, 9, 0.12); color: #b45309; }
.kpi-value {
  font-size: 24px;
  font-weight: 700;
  letter-spacing: -0.03em;
  color: var(--text);
  line-height: 1.15;
  margin-top: 2px;
}
.kpi-slash {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-muted);
}
.kpi-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  color: var(--text-muted);
  margin-top: 2px;
}
.kpi-chip {
  font-size: 11px;
  color: var(--text-secondary);
  background: var(--bg-elevated);
  border: 1px solid var(--border-line);
  border-radius: 999px;
  padding: 1px 8px;
  white-space: nowrap;
}
.kpi-bar {
  height: 4px;
  border-radius: 999px;
  background: #eef1f4;
  overflow: hidden;
  margin-top: 4px;
}
.kpi-bar i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: linear-gradient(90deg, #0f766e 0%, #2dd4bf 100%);
}
.kpi-meta {
  font-size: 11.5px;
  color: var(--text-muted);
  margin-top: 2px;
}

.dash-traffic {
  margin-bottom: 12px;
  border-radius: 12px;
  overflow: hidden;
}
.dash-traffic-hd {
  align-items: center;
}
.dash-traffic-title {
  display: flex;
  align-items: center;
  gap: 8px;
}
.dash-traffic-title h2 {
  margin: 0;
  font-size: 14.5px;
  font-weight: 650;
}
.dash-traffic-dot {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background:
    conic-gradient(var(--accent) 0 70%, #dbe4ea 0);
  box-shadow: inset 0 0 0 2px #fff;
  flex-shrink: 0;
}
.dash-chart-wrap {
  position: relative;
  padding: 4px 10px 14px;
}
.dash-chart {
  width: 100%;
  height: 280px;
  display: block;
  cursor: crosshair;
}
.dash-chart-grid {
  stroke: #e8ecf0;
  stroke-width: 1;
  stroke-dasharray: 4 4;
}
.dash-chart-vgrid {
  stroke: #eef1f4;
  stroke-width: 1;
  stroke-dasharray: 3 5;
}
.dash-chart-axis {
  stroke: #d5dbe3;
  stroke-width: 1;
}
.dash-chart-label {
  fill: #98a2b3;
  font-size: 10px;
  font-family: var(--mono);
}
.dash-chart-xlabel {
  fill: #98a2b3;
  font-size: 9px;
  font-family: var(--mono);
}
.dash-chart-area {
  fill: rgba(15, 118, 110, 0.08);
}
.dash-chart-line {
  stroke: #0f766e;
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
}
.dash-chart-guide {
  stroke: #94a3b8;
  stroke-width: 1;
  stroke-dasharray: 3 3;
}
.dash-chart-focus {
  fill: #fff;
  stroke: #0f766e;
  stroke-width: 2;
}
.dash-chart-focus-core {
  fill: #0f766e;
  stroke: none;
}
.dash-chart-tip {
  position: absolute;
  transform: translate(-50%, -100%);
  pointer-events: none;
  background: #fff;
  border: 1px solid var(--border-line);
  border-radius: 10px;
  padding: 10px 12px;
  box-shadow: 0 8px 24px rgba(15, 23, 42, 0.08);
  font-size: 12.5px;
  color: var(--text);
  min-width: 128px;
  z-index: 3;
  line-height: 1.45;
}
.dash-chart-tip .tip-val {
  color: #0f766e;
  font-weight: 600;
  margin-top: 2px;
}
.dash-chart-tip .tip-sub {
  color: var(--text-muted);
  font-size: 11.5px;
  margin-top: 2px;
}
</style>
