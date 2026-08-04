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
  // headroom so line doesn't touch top
  return m > 0 ? m * 1.08 : 1
})

const chartHover = ref(null)

const chartLayout = {
  w: 720,
  h: 200,
  padL: 52,
  padR: 16,
  padT: 16,
  padB: 28,
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
  return padT + inner * (1 - v / max)
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
  // 4 ticks including 0
  const vals = [0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(max * f))
  // de-dupe when tiny
  const uniq = []
  for (const v of vals) {
    if (!uniq.length || uniq[uniq.length - 1] !== v) uniq.push(v)
  }
  return uniq.map((v) => ({ v, y: chartY(v), label: formatBytes(v) }))
})

const xTicks = [0, 3, 6, 9, 12, 15, 18, 21, 23]

function onChartMove(ev) {
  const svg = ev.currentTarget
  const rect = svg.getBoundingClientRect()
  const { w, padL, padR } = chartLayout
  const x = ((ev.clientX - rect.left) / rect.width) * w
  const inner = w - padL - padR
  let h = Math.round(((x - padL) / inner) * 23)
  if (h < 0) h = 0
  if (h > 23) h = 23
  chartHover.value = hourlyPoints.value[h]
}

function onChartLeave() {
  chartHover.value = null
}

function hourLabel(h) {
  return String(h).padStart(2, '0') + ':00'
}

const onlineCount = computed(() => nodes.value.filter((n) => n.status === 'online').length)
const offlineCount = computed(() => Math.max(0, nodes.value.length - onlineCount.value))

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

  <!-- 4 primary KPIs: 总流量 · 今日 · 节点 · 待处理 -->
  <div class="grid-stats dash-kpi" v-if="diag">
    <div class="card clickable" :class="monthTotal ? 'card-ok' : ''" @click="router.push('/users')">
      <h3>总流量</h3>
      <div class="value" style="font-size:20px">{{ formatBytes(monthTotal) }}</div>
      <div class="sub">本月 · 每月 1 日 0 点重置 · ↓ {{ formatBytes(monthDown) }} · ↑ {{ formatBytes(monthUp) }}</div>
    </div>
    <div class="card clickable" @click="router.push('/users')">
      <h3>今日流量</h3>
      <div class="value" style="font-size:20px">{{ formatBytes(todayTotal) }}</div>
      <div class="sub">↓ {{ formatBytes(todayDown) }} · ↑ {{ formatBytes(todayUp) }}</div>
    </div>
    <div
      class="card clickable"
      :class="offlineCount === 0 && nodes.length ? 'card-ok' : offlineCount ? 'card-warn' : ''"
      @click="router.push('/nodes')"
    >
      <h3>节点在线</h3>
      <div class="value">
        {{ onlineCount }}<span class="slash"> / {{ nodes.length }}</span>
      </div>
      <div class="sub" v-if="offlineCount">{{ offlineCount }} 离线</div>
      <div class="sub" v-else>全部在线</div>
    </div>
    <div
      class="card clickable"
      :class="issueCount ? 'card-warn' : 'card-ok'"
      @click="issueCount ? null : router.push('/nodes')"
    >
      <h3>待处理</h3>
      <div class="value">{{ issueCount }}</div>
      <div class="sub">{{ issueCount ? '点击下方列表处理' : '系统正常' }}</div>
    </div>
  </div>

  <!-- Today hourly traffic curve -->
  <div class="panel dash-traffic" v-if="diag">
    <div class="panel-hd">
      <div>
        <h2>今日流量</h2>
        <div class="muted" style="font-size:12px;margin-top:3px">
          按小时 · 本地 0–23 点 · 总计 {{ formatBytes(todayTotal) }}
        </div>
      </div>
      <div class="dash-chart-hover mono" v-if="chartHover">
        <strong>{{ hourLabel(chartHover.hour) }}</strong>
        <span>{{ formatBytes(chartHover.total) }}</span>
        <span class="muted">↓ {{ formatBytes(chartHover.down) }} · ↑ {{ formatBytes(chartHover.up) }}</span>
      </div>
      <div class="muted dash-chart-hover" v-else style="font-size:12px">悬停查看各小时</div>
    </div>
    <div class="panel-bd dash-chart-wrap">
      <svg
        class="dash-chart"
        viewBox="0 0 720 200"
        preserveAspectRatio="none"
        @mousemove="onChartMove"
        @mouseleave="onChartLeave"
        role="img"
        aria-label="今日按小时流量曲线"
      >
        <!-- grid + axes -->
        <line
          v-for="t in yTicks"
          :key="'yg' + t.v"
          :x1="chartLayout.padL"
          :x2="720 - chartLayout.padR"
          :y1="t.y"
          :y2="t.y"
          class="dash-chart-grid"
        />
        <line
          :x1="chartLayout.padL"
          :y1="chartLayout.padT"
          :x2="chartLayout.padL"
          :y2="200 - chartLayout.padB"
          class="dash-chart-axis"
        />
        <line
          :x1="chartLayout.padL"
          :y1="200 - chartLayout.padB"
          :x2="720 - chartLayout.padR"
          :y2="200 - chartLayout.padB"
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
          :y="200 - 8"
          class="dash-chart-label"
          text-anchor="middle"
        >{{ h }}</text>
        <!-- area + line -->
        <path :d="chartAreaPath" class="dash-chart-area" />
        <path :d="chartLinePath" class="dash-chart-line" fill="none" />
        <!-- points -->
        <circle
          v-for="p in hourlyPoints"
          :key="'p' + p.hour"
          :cx="chartX(p.hour)"
          :cy="chartY(p.total)"
          r="2.5"
          class="dash-chart-dot"
          :class="{ active: chartHover && chartHover.hour === p.hour }"
        />
        <!-- hover guide -->
        <template v-if="chartHover">
          <line
            :x1="chartX(chartHover.hour)"
            :x2="chartX(chartHover.hour)"
            :y1="chartLayout.padT"
            :y2="200 - chartLayout.padB"
            class="dash-chart-guide"
          />
          <circle
            :cx="chartX(chartHover.hour)"
            :cy="chartY(chartHover.total)"
            r="4.5"
            class="dash-chart-dot active"
          />
        </template>
      </svg>
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
.dash-kpi {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}
@media (max-width: 1100px) {
  .dash-kpi {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 560px) {
  .dash-kpi {
    grid-template-columns: 1fr;
  }
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

.dash-traffic .panel-hd {
  align-items: flex-start;
}
.dash-chart-hover {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  font-size: 12.5px;
  color: var(--text-secondary);
}
.dash-chart-hover strong {
  color: var(--text);
  font-weight: 650;
}
.dash-chart-wrap {
  padding: 4px 12px 12px;
}
.dash-chart {
  width: 100%;
  height: 200px;
  display: block;
  cursor: crosshair;
}
.dash-chart-grid {
  stroke: var(--border-line);
  stroke-width: 1;
  stroke-dasharray: 3 4;
  opacity: 0.9;
}
.dash-chart-axis {
  stroke: var(--border-strong, var(--border-line));
  stroke-width: 1.2;
}
.dash-chart-label {
  fill: var(--text-muted, var(--text-secondary));
  font-size: 10px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.dash-chart-area {
  fill: var(--accent-soft);
}
.dash-chart-line {
  stroke: var(--accent);
  stroke-width: 2.2;
  stroke-linejoin: round;
  stroke-linecap: round;
}
.dash-chart-dot {
  fill: var(--bg-surface, #fff);
  stroke: var(--accent);
  stroke-width: 1.6;
  opacity: 0.55;
}
.dash-chart-dot.active {
  opacity: 1;
  fill: var(--accent);
  stroke: var(--accent);
}
.dash-chart-guide {
  stroke: var(--accent);
  stroke-width: 1;
  stroke-dasharray: 3 3;
  opacity: 0.45;
}
</style>
