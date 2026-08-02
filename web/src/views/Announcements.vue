<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '../api'
import { useFlash } from '../flash'

const list = ref([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const flash = useFlash()
const formFlash = useFlash()

const show = ref(false)
const mode = ref('create') // create | edit
const editingId = ref(null)

const form = reactive({
  title: '',
  body: '',
  enabled: true,
  popup: false,
})

const sorted = computed(() => list.value || [])

function fmtTime(t) {
  if (!t) return '—'
  try {
    const d = new Date(t)
    if (Number.isNaN(d.getTime())) return String(t).slice(0, 19)
    return d.toLocaleString()
  } catch {
    return String(t)
  }
}

function resetForm() {
  form.title = ''
  form.body = ''
  form.enabled = true
  form.popup = false
  editingId.value = null
  formFlash.clear()
}

function openCreate() {
  mode.value = 'create'
  resetForm()
  show.value = true
}

function openEdit(a) {
  mode.value = 'edit'
  editingId.value = a.id
  form.title = a.title || ''
  form.body = a.body || ''
  form.enabled = !!a.enabled
  form.popup = !!a.popup
  formFlash.clear()
  show.value = true
}

function closeModal() {
  show.value = false
  resetForm()
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await api('/api/admin/announcements')
    list.value = Array.isArray(res) ? res : []
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  formFlash.clear()
  try {
    const body = {
      title: form.title,
      body: form.body,
      enabled: !!form.enabled,
      popup: !!form.popup,
    }
    if (mode.value === 'create') {
      await api('/api/admin/announcements', {
        method: 'POST',
        body: JSON.stringify(body),
      })
      formFlash.ok('已发布')
    } else {
      await api(`/api/admin/announcements/${editingId.value}`, {
        method: 'PUT',
        body: JSON.stringify(body),
      })
      formFlash.ok('已保存')
    }
    await load()
    setTimeout(() => {
      closeModal()
    }, 700)
  } catch (e) {
    formFlash.err(e.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function remove(a) {
  if (!a?.id) return
  if (!confirm(`确定删除公告「${a.title}」？`)) return
  try {
    await api(`/api/admin/announcements/${a.id}`, { method: 'DELETE' })
    flash.ok('已删除')
    await load()
  } catch (e) {
    flash.err(e.message || '删除失败')
  }
}

async function setPopup(a, on) {
  try {
    await api(`/api/admin/announcements/${a.id}/popup`, {
      method: 'POST',
      body: JSON.stringify({ popup: !!on }),
    })
    flash.ok(on ? '已设为弹窗公告（用户打开查询页会弹出）' : '已取消弹窗')
    await load()
  } catch (e) {
    flash.err(e.message || '操作失败')
  }
}

async function toggleEnabled(a) {
  try {
    await api(`/api/admin/announcements/${a.id}`, {
      method: 'PUT',
      body: JSON.stringify({
        title: a.title,
        body: a.body,
        enabled: !a.enabled,
        popup: !!a.popup,
      }),
    })
    flash.ok(!a.enabled ? '已启用' : '已停用')
    await load()
  } catch (e) {
    flash.err(e.message || '操作失败')
  }
}

onMounted(load)
</script>

<template>
  <div v-if="error" class="error" @click="error = ''">{{ error }}</div>
  <div
    v-if="flash.msg"
    class="action-feedback"
    :class="flash.kind"
    style="margin-bottom: 10px"
    @click="flash.clear()"
  >
    {{ flash.msg }}
  </div>

  <div class="panel">
    <div class="panel-hd">
      <div>
        <h2>公告</h2>
        <div class="muted" style="font-size: 12px; margin-top: 3px">
          发布后展示在用户查询页；可指定一条为「弹窗」——用户每次打开查询页自动弹出 60 秒
        </div>
      </div>
      <div class="row-actions">
        <button class="btn btn-ghost btn-sm" :disabled="loading" @click="load">刷新</button>
        <button class="btn btn-primary btn-sm" @click="openCreate">新建公告</button>
      </div>
    </div>
    <div class="panel-bd" style="padding: 0">
      <div v-if="loading && !sorted.length" class="empty">加载中…</div>
      <div v-else-if="!sorted.length" class="empty">暂无公告，点右上角「新建公告」</div>
      <table v-else class="data">
        <thead>
          <tr>
            <th style="width: 72px">ID</th>
            <th>标题</th>
            <th style="width: 88px">状态</th>
            <th style="width: 100px">弹窗</th>
            <th style="width: 160px">更新时间</th>
            <th style="width: 220px">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in sorted" :key="a.id">
            <td class="mono">{{ a.id }}</td>
            <td>
              <div style="font-weight: 600">{{ a.title }}</div>
              <div class="muted" style="font-size: 12px; margin-top: 2px; max-width: 420px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis">
                {{ a.body }}
              </div>
            </td>
            <td>
              <span class="badge" :class="a.enabled ? 'ok' : 'warn'">
                {{ a.enabled ? '启用' : '停用' }}
              </span>
            </td>
            <td>
              <span v-if="a.popup" class="badge ok">弹窗中</span>
              <span v-else class="muted" style="font-size: 12px">—</span>
            </td>
            <td class="mono" style="font-size: 12px">{{ fmtTime(a.updated_at) }}</td>
            <td>
              <div class="row-actions" style="flex-wrap: wrap; gap: 4px">
                <button class="btn btn-link btn-sm" type="button" @click="openEdit(a)">编辑</button>
                <button
                  v-if="!a.popup"
                  class="btn btn-link btn-sm"
                  type="button"
                  @click="setPopup(a, true)"
                >
                  设为弹窗
                </button>
                <button
                  v-else
                  class="btn btn-link btn-sm"
                  type="button"
                  @click="setPopup(a, false)"
                >
                  取消弹窗
                </button>
                <button class="btn btn-link btn-sm" type="button" @click="toggleEnabled(a)">
                  {{ a.enabled ? '停用' : '启用' }}
                </button>
                <button class="btn btn-link-danger btn-sm" type="button" @click="remove(a)">删除</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <div v-if="show" class="modal-mask" @click.self="closeModal">
    <div class="modal" style="width: min(640px, 100%)">
      <div class="modal-hd">
        <h3>{{ mode === 'create' ? '新建公告' : '编辑公告' }}</h3>
        <button type="button" class="btn btn-ghost btn-sm" @click="closeModal">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="field">
          <label>标题</label>
          <input v-model="form.title" maxlength="80" placeholder="例如：线路维护通知" />
        </div>
        <div class="field">
          <label>内容</label>
          <textarea
            v-model="form.body"
            rows="8"
            placeholder="支持多行纯文本。用户查询页会展示全文。"
            style="resize: vertical; min-height: 140px"
          />
        </div>
        <label class="muted" style="display: flex; align-items: center; gap: 8px; font-size: 13px">
          <input type="checkbox" v-model="form.enabled" />
          启用（停用后查询页不显示）
        </label>
        <label class="muted" style="display: flex; align-items: center; gap: 8px; font-size: 13px">
          <input type="checkbox" v-model="form.popup" />
          设为弹窗公告（全局仅一条；用户每次打开查询页自动弹出 60 秒）
        </label>
      </div>
      <div class="modal-ft" style="justify-content: space-between; align-items: center">
        <div
          v-if="formFlash.msg"
          class="action-feedback"
          :class="formFlash.kind"
          style="margin: 0"
          @click="formFlash.clear()"
        >
          {{ formFlash.msg }}
        </div>
        <span v-else />
        <div class="row-actions">
          <button type="button" class="btn btn-ghost" :disabled="saving" @click="closeModal">取消</button>
          <button type="button" class="btn btn-primary" :disabled="saving" @click="save">
            {{ saving ? '保存中…' : mode === 'create' ? '发布' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
