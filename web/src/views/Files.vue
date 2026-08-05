<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api, formatBytes, getToken } from '../api'
import { useFlash } from '../flash'

const list = ref([])
const loading = ref(false)
const uploading = ref(false)
const error = ref('')
const flash = useFlash()
const formFlash = useFlash()

const showUpload = ref(false)
const showEdit = ref(false)
const editingId = ref(null)

const form = reactive({
  title: '',
  enabled: true,
})
const fileInput = ref(null)
const pickedName = ref('')
const pickedSize = ref(0)
const uploadPct = ref(0)

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

function resetUpload() {
  form.title = ''
  form.enabled = true
  pickedName.value = ''
  pickedSize.value = 0
  uploadPct.value = 0
  if (fileInput.value) fileInput.value.value = ''
  formFlash.clear()
}

function authHeaders(extra = {}) {
  const headers = { ...extra }
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  return headers
}

async function readJSON(res) {
  const text = await res.text()
  let data = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = null
  }
  return { text, data }
}

/** Chunked upload: each request stays under reverse-proxy 1MB body limits. */
async function uploadChunked(file) {
  const initRes = await fetch('/api/admin/files/upload/init', {
    method: 'POST',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({
      filename: file.name,
      title: form.title.trim() || file.name,
      size: file.size,
      content_type: file.type || 'application/octet-stream',
      enabled: !!form.enabled,
    }),
  })
  const init = await readJSON(initRes)
  if (!initRes.ok) {
    if (initRes.status === 413) {
      throw new Error('前置代理限制了请求大小（413）。请升级面板后使用分片上传，或在 Nginx 设置 client_max_body_size 64m;')
    }
    throw new Error((init.data && init.data.error) || init.text || initRes.statusText || '初始化上传失败')
  }
  const uploadId = init.data?.upload_id
  let chunkSize = Number(init.data?.chunk_size) || 512 * 1024
  if (chunkSize < 64 * 1024) chunkSize = 512 * 1024
  if (chunkSize > 900 * 1024) chunkSize = 512 * 1024
  if (!uploadId) throw new Error('初始化上传失败：无 upload_id')

  let offset = 0
  try {
    while (offset < file.size) {
      const end = Math.min(offset + chunkSize, file.size)
      const blob = file.slice(offset, end)
      const chunkRes = await fetch(
        `/api/admin/files/upload/${encodeURIComponent(uploadId)}/chunk?offset=${offset}`,
        {
          method: 'PUT',
          headers: authHeaders({ 'Content-Type': 'application/octet-stream' }),
          body: blob,
        },
      )
      const chunk = await readJSON(chunkRes)
      if (!chunkRes.ok) {
        if (chunkRes.status === 413) {
          throw new Error('前置代理限制了请求大小（413）。请在 Nginx 增加 client_max_body_size 64m; 后重试')
        }
        throw new Error((chunk.data && chunk.data.error) || chunk.text || chunkRes.statusText || '分片上传失败')
      }
      const received = Number(chunk.data?.received)
      if (Number.isFinite(received) && received > offset) {
        offset = received
      } else {
        offset = end
      }
      uploadPct.value = Math.min(99, Math.round((offset / file.size) * 100))
    }

    const doneRes = await fetch(`/api/admin/files/upload/${encodeURIComponent(uploadId)}/complete`, {
      method: 'POST',
      headers: authHeaders({ 'Content-Type': 'application/json' }),
      body: '{}',
    })
    const done = await readJSON(doneRes)
    if (!doneRes.ok) {
      throw new Error((done.data && done.data.error) || done.text || doneRes.statusText || '完成上传失败')
    }
    uploadPct.value = 100
    return done.data
  } catch (e) {
    try {
      await fetch(`/api/admin/files/upload/${encodeURIComponent(uploadId)}`, {
        method: 'DELETE',
        headers: authHeaders(),
      })
    } catch {
      /* ignore abort errors */
    }
    throw e
  }
}

function openUpload() {
  resetUpload()
  showUpload.value = true
}

function closeUpload() {
  showUpload.value = false
  resetUpload()
}

function onPick(e) {
  const f = e?.target?.files?.[0]
  if (!f) {
    pickedName.value = ''
    pickedSize.value = 0
    return
  }
  pickedName.value = f.name || ''
  pickedSize.value = f.size || 0
  if (!form.title) form.title = f.name || ''
}

function openEdit(row) {
  editingId.value = row.id
  form.title = row.title || row.filename || ''
  form.enabled = !!row.enabled
  formFlash.clear()
  showEdit.value = true
}

function closeEdit() {
  showEdit.value = false
  editingId.value = null
  formFlash.clear()
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await api('/api/admin/files')
    list.value = Array.isArray(res) ? res : []
  } catch (e) {
    error.value = e.message || '加载失败'
  } finally {
    loading.value = false
  }
}

async function doUpload() {
  const input = fileInput.value
  const f = input?.files?.[0]
  if (!f) {
    formFlash.err('请选择文件')
    return
  }
  if (f.size > 50 * 1024 * 1024) {
    formFlash.err('文件过大（上限 50MB）')
    return
  }
  uploading.value = true
  uploadPct.value = 0
  formFlash.clear()
  try {
    // Always chunked: works behind nginx default client_max_body_size 1m
    await uploadChunked(f)
    formFlash.ok('已上传')
    await load()
    setTimeout(() => closeUpload(), 600)
  } catch (e) {
    formFlash.err(e.message || '上传失败')
  } finally {
    uploading.value = false
  }
}

async function saveEdit() {
  if (!editingId.value) return
  uploading.value = true
  formFlash.clear()
  try {
    await api(`/api/admin/files/${editingId.value}`, {
      method: 'PUT',
      body: JSON.stringify({
        title: form.title,
        enabled: !!form.enabled,
      }),
    })
    formFlash.ok('已保存')
    await load()
    setTimeout(() => closeEdit(), 600)
  } catch (e) {
    formFlash.err(e.message || '保存失败')
  } finally {
    uploading.value = false
  }
}

async function remove(row) {
  if (!row?.id) return
  if (!confirm(`确定删除文件「${row.title || row.filename}」？客户将无法再下载。`)) return
  try {
    await api(`/api/admin/files/${row.id}`, { method: 'DELETE' })
    flash.ok('已删除')
    await load()
  } catch (e) {
    flash.err(e.message || '删除失败')
  }
}

async function toggleEnabled(row) {
  try {
    await api(`/api/admin/files/${row.id}`, {
      method: 'PUT',
      body: JSON.stringify({
        title: row.title || row.filename,
        enabled: !row.enabled,
      }),
    })
    flash.ok(!row.enabled ? '已启用' : '已停用')
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
        <h2>客户文件</h2>
        <div class="muted" style="font-size: 12px; margin-top: 3px">
          上传后展示在用户查询页，客户可直接下载（全局共享，上限 50MB/文件）
        </div>
      </div>
      <div class="row-actions">
        <button class="btn btn-ghost btn-sm" :disabled="loading" @click="load">刷新</button>
        <button class="btn btn-primary btn-sm" @click="openUpload">上传文件</button>
      </div>
    </div>
    <div class="panel-bd" style="padding: 0">
      <div v-if="loading && !sorted.length" class="empty">加载中…</div>
      <div v-else-if="!sorted.length" class="empty">暂无文件，点右上角「上传文件」</div>
      <table v-else class="data">
        <thead>
          <tr>
            <th style="width: 64px">ID</th>
            <th>显示名称</th>
            <th>原文件名</th>
            <th style="width: 100px">大小</th>
            <th style="width: 88px">状态</th>
            <th style="width: 160px">更新时间</th>
            <th style="width: 180px">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in sorted" :key="row.id">
            <td class="mono">{{ row.id }}</td>
            <td style="font-weight: 600">{{ row.title || row.filename }}</td>
            <td class="mono" style="font-size: 12px">{{ row.filename }}</td>
            <td class="mono" style="font-size: 12px">{{ formatBytes(row.size) }}</td>
            <td>
              <span class="badge" :class="row.enabled ? 'ok' : 'warn'">
                {{ row.enabled ? '启用' : '停用' }}
              </span>
            </td>
            <td class="mono" style="font-size: 12px">{{ fmtTime(row.updated_at) }}</td>
            <td>
              <div class="row-actions" style="flex-wrap: wrap; gap: 4px">
                <button class="btn btn-link btn-sm" type="button" @click="openEdit(row)">编辑</button>
                <button class="btn btn-link btn-sm" type="button" @click="toggleEnabled(row)">
                  {{ row.enabled ? '停用' : '启用' }}
                </button>
                <button class="btn btn-link-danger btn-sm" type="button" @click="remove(row)">删除</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <div v-if="showUpload" class="modal-mask" @click.self="closeUpload">
    <div class="modal" style="width: min(520px, 100%)">
      <div class="modal-hd">
        <h3>上传文件</h3>
        <button type="button" class="btn btn-ghost btn-sm" @click="closeUpload">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="field">
          <label>文件</label>
          <input ref="fileInput" type="file" @change="onPick" />
          <div v-if="pickedName" class="muted" style="font-size: 12px; margin-top: 6px">
            {{ pickedName }} · {{ formatBytes(pickedSize) }}
          </div>
        </div>
        <div class="field">
          <label>显示名称（查询页标题）</label>
          <input v-model="form.title" maxlength="120" placeholder="例如：Windows 客户端" />
        </div>
        <label class="muted" style="display: flex; align-items: center; gap: 8px; font-size: 13px">
          <input type="checkbox" v-model="form.enabled" />
          启用（停用后查询页不显示）
        </label>
        <div v-if="uploading" class="muted" style="font-size: 12px; margin-top: 10px">
          上传中 {{ uploadPct }}%
          <div
            style="margin-top: 6px; height: 6px; background: #e2e8f0; border-radius: 3px; overflow: hidden"
          >
            <div
              :style="{
                width: uploadPct + '%',
                height: '100%',
                background: 'var(--accent, #0f766e)',
                transition: 'width 0.15s linear',
              }"
            />
          </div>
        </div>
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
          <button type="button" class="btn btn-ghost" :disabled="uploading" @click="closeUpload">取消</button>
          <button type="button" class="btn btn-primary" :disabled="uploading" @click="doUpload">
            {{ uploading ? `上传中 ${uploadPct}%` : '上传' }}
          </button>
        </div>
      </div>
    </div>
  </div>

  <div v-if="showEdit" class="modal-mask" @click.self="closeEdit">
    <div class="modal" style="width: min(480px, 100%)">
      <div class="modal-hd">
        <h3>编辑文件</h3>
        <button type="button" class="btn btn-ghost btn-sm" @click="closeEdit">关闭</button>
      </div>
      <div class="modal-bd">
        <div class="field">
          <label>显示名称</label>
          <input v-model="form.title" maxlength="120" />
        </div>
        <label class="muted" style="display: flex; align-items: center; gap: 8px; font-size: 13px">
          <input type="checkbox" v-model="form.enabled" />
          启用（停用后查询页不显示）
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
          <button type="button" class="btn btn-ghost" :disabled="uploading" @click="closeEdit">取消</button>
          <button type="button" class="btn btn-primary" :disabled="uploading" @click="saveEdit">
            {{ uploading ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
