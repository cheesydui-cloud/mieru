const TOKEN_KEY = 'mieru_token'
const ROLE_KEY = 'mieru_role'
const USER_KEY = 'mieru_user'

export function getToken() {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function getRole() {
  return localStorage.getItem(ROLE_KEY) || ''
}

export function getUsername() {
  return localStorage.getItem(USER_KEY) || ''
}

export function setSession({ token, role, username }) {
  localStorage.setItem(TOKEN_KEY, token)
  localStorage.setItem(ROLE_KEY, role)
  localStorage.setItem(USER_KEY, username)
}

export function clearSession() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(ROLE_KEY)
  localStorage.removeItem(USER_KEY)
}

export async function api(path, options = {}) {
  const headers = {
    'Content-Type': 'application/json',
    ...(options.headers || {}),
  }
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`

  const res = await fetch(path, { ...options, headers })
  const text = await res.text()
  let data = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = text
  }
  if (!res.ok) {
    const msg = (data && data.error) || res.statusText || 'request failed'
    throw new Error(msg)
  }
  return data
}

/** Clipboard that works on plain HTTP (non-secure context). */
export async function copyText(text) {
  const value = String(text ?? '')
  if (!value) throw new Error('empty')
  // Prefer modern API only in secure contexts (https / localhost)
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      // fall through
    }
  }
  const ta = document.createElement('textarea')
  ta.value = value
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.top = '0'
  ta.style.left = '0'
  ta.style.width = '1px'
  ta.style.height = '1px'
  ta.style.padding = '0'
  ta.style.border = 'none'
  ta.style.outline = 'none'
  ta.style.boxShadow = 'none'
  ta.style.background = 'transparent'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.focus()
  ta.select()
  ta.setSelectionRange(0, value.length)
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  document.body.removeChild(ta)
  if (!ok) throw new Error('copy failed')
  return true
}

export function formatBytes(n) {
  if (n == null) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = Number(n)
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}

export function formatBps(n) {
  if (!n) return '0 bps'
  const u = ['bps', 'Kbps', 'Mbps', 'Gbps']
  let i = 0
  let v = Number(n)
  while (v >= 1000 && i < u.length - 1) {
    v /= 1000
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}

export function statusBadge(status) {
  if (status === 'online' || status === 'active') return 'ok'
  if (status === 'expired' || status === 'over_quota' || status === 'offline') return 'err'
  if (status === 'degraded' || status === 'disabled') return 'warn'
  return ''
}
