<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { api, formatBytes, formatBps, statusBadge } from '../api'

const stats = ref(null)
const nodes = ref([])
const rates = ref([])
const error = ref('')
let timer

async function load() {
  try {
    const [st, ns, rs] = await Promise.all([
      api('/api/admin/dashboard'),
      api('/api/admin/nodes'),
      api('/api/admin/metrics/rates'),
    ])
    stats.value = st
    nodes.value = ns
    rates.value = rs || []
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 5000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div v-if="error" class="error">{{ error }}</div>
  <div class="grid-stats" v-if="stats">
    <div class="card">
      <h3>节点在线 / 总数</h3>
      <div class="value">{{ stats.online_nodes }}<span class="slash"> / {{ stats.total_nodes }}</span></div>
    </div>
    <div class="card">
      <h3>用户正常 / 总数</h3>
      <div class="value">{{ stats.active_users }}<span class="slash"> / {{ stats.total_users }}</span></div>
    </div>
    <div class="card">
      <h3>今日上行</h3>
      <div class="value" style="font-size:22px">{{ formatBytes(stats.today_up) }}</div>
    </div>
    <div class="card">
      <h3>今日下行</h3>
      <div class="value" style="font-size:22px">{{ formatBytes(stats.today_down) }}</div>
    </div>
    <div class="card">
      <h3>异常节点</h3>
      <div class="value">{{ stats.unhealthy_nodes || 0 }}</div>
    </div>
    <div class="card">
      <h3>实时上报用户</h3>
      <div class="value">{{ rates.length }}</div>
    </div>
    <div class="card">
      <h3>节点总数</h3>
      <div class="value">{{ stats.total_nodes }}</div>
    </div>
    <div class="card">
      <h3>自动刷新</h3>
      <div class="value" style="font-size:16px;font-weight:500;color:var(--text-secondary)">5s</div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <h2>节点状态</h2>
      <span class="muted" style="font-size:12px">5s 自动刷新</span>
    </div>
    <div class="panel-bd">
      <table class="data" v-if="nodes.length">
        <thead>
          <tr>
            <th>名称</th>
            <th>角色</th>
            <th>域名 / IP</th>
            <th>区域</th>
            <th>状态</th>
            <th>配置版本</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="n in nodes" :key="n.id">
            <td><span class="name-link">{{ n.name }}</span></td>
            <td><span class="badge">{{ n.role }}</span></td>
            <td class="mono">{{ n.hostname || n.public_ip || '—' }}</td>
            <td>{{ n.region || '—' }}</td>
            <td>
              <span class="badge" :class="statusBadge(n.status)">
                <span class="dot"></span>{{ n.status === 'online' ? '在线' : n.status }}
              </span>
            </td>
            <td class="num">v{{ n.config_version }}</td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty">暂无节点，先去「节点」创建 Entry / Relay / Exit</div>
    </div>
  </div>

  <div class="panel">
    <div class="panel-hd">
      <h2>实时网速（落地上报）</h2>
    </div>
    <div class="panel-bd">
      <table class="data" v-if="rates.length">
        <thead>
          <tr>
            <th>用户 ID</th>
            <th>上行</th>
            <th>下行</th>
            <th>时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in rates" :key="r.user_id">
            <td class="mono">{{ r.user_id }}</td>
            <td class="num">{{ formatBps(r.up_bps) }}</td>
            <td class="num">{{ formatBps(r.down_bps) }}</td>
            <td class="mono">{{ r.ts ? new Date(r.ts * 1000).toLocaleTimeString() : '—' }}</td>
          </tr>
        </tbody>
      </table>
      <div v-else class="empty">等待 Exit Agent 上报流量样本</div>
    </div>
  </div>
</template>
