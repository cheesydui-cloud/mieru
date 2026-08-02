import { reactive } from 'vue'

const DEFAULT_NAME = 'Mieru'
const DEFAULT_SUBTITLE = '管理节点、用户、隧道与落地计量'

export const brand = reactive({
  name: DEFAULT_NAME,
  subtitle: DEFAULT_SUBTITLE,
  faviconData: '',
  loaded: false,
})

export function brandMarkLetter(name = brand.name) {
  const s = String(name || DEFAULT_NAME).trim()
  if (!s) return 'M'
  const m = s.match(/[\u4e00-\u9fffA-Za-z0-9]/)
  return (m ? m[0] : s[0]).toUpperCase()
}

export function applyDocumentBrand(name = brand.name, faviconData = brand.faviconData) {
  const n = String(name || DEFAULT_NAME).trim() || DEFAULT_NAME
  document.title = `${n} 控制台`
  if (faviconData && String(faviconData).startsWith('data:image/')) {
    setFaviconURL(faviconData)
  } else {
    setFaviconFromName(n)
  }
}

function setFaviconURL(url) {
  try {
    let link = document.querySelector("link[rel='icon']")
    if (!link) {
      link = document.createElement('link')
      link.rel = 'icon'
      document.head.appendChild(link)
    }
    link.type = url.includes('svg') ? 'image/svg+xml' : 'image/png'
    link.href = url

    let apple = document.querySelector("link[rel='apple-touch-icon']")
    if (!apple) {
      apple = document.createElement('link')
      apple.rel = 'apple-touch-icon'
      document.head.appendChild(apple)
    }
    apple.href = url
  } catch {
    /* ignore */
  }
}

/** Canvas favicon so tab icon shows panel name initial (not generic globe). */
export function setFaviconFromName(name = brand.name) {
  try {
    const letter = brandMarkLetter(name)
    const size = 64
    const canvas = document.createElement('canvas')
    canvas.width = size
    canvas.height = size
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    const r = 12
    ctx.fillStyle = '#1a2332'
    ctx.beginPath()
    ctx.moveTo(r, 0)
    ctx.arcTo(size, 0, size, size, r)
    ctx.arcTo(size, size, 0, size, r)
    ctx.arcTo(0, size, 0, 0, r)
    ctx.arcTo(0, 0, size, 0, r)
    ctx.closePath()
    ctx.fill()
    ctx.fillStyle = '#ffffff'
    ctx.font = `700 ${letter.length > 1 ? 28 : 34}px Inter, "PingFang SC", system-ui, sans-serif`
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(letter, size / 2, size / 2 + 1)

    setFaviconURL(canvas.toDataURL('image/png'))
  } catch {
    /* ignore */
  }
}

export function setBrandName(name) {
  const n = String(name || '').trim() || DEFAULT_NAME
  brand.name = n
  brand.loaded = true
  applyDocumentBrand(n, brand.faviconData)
  try {
    localStorage.setItem('mieru_panel_name', n)
  } catch {
    /* ignore */
  }
}

export function setBrandMeta({ name, subtitle, faviconData } = {}) {
  if (name != null) {
    brand.name = String(name || '').trim() || DEFAULT_NAME
    try {
      localStorage.setItem('mieru_panel_name', brand.name)
    } catch {
      /* ignore */
    }
  }
  if (subtitle != null) {
    brand.subtitle = String(subtitle || '').trim() || DEFAULT_SUBTITLE
    try {
      localStorage.setItem('mieru_panel_subtitle', brand.subtitle)
    } catch {
      /* ignore */
    }
  }
  if (faviconData != null) {
    brand.faviconData = String(faviconData || '')
    try {
      if (brand.faviconData) localStorage.setItem('mieru_panel_favicon', brand.faviconData)
      else localStorage.removeItem('mieru_panel_favicon')
    } catch {
      /* ignore */
    }
  }
  brand.loaded = true
  applyDocumentBrand(brand.name, brand.faviconData)
}

/** Load brand for any page (login + admin). Uses public endpoint. */
export async function loadBrand() {
  try {
    const cached = localStorage.getItem('mieru_panel_name')
    const cachedSub = localStorage.getItem('mieru_panel_subtitle')
    const cachedFav = localStorage.getItem('mieru_panel_favicon')
    if (cached) brand.name = cached
    if (cachedSub) brand.subtitle = cachedSub
    if (cachedFav) brand.faviconData = cachedFav
    applyDocumentBrand(brand.name, brand.faviconData)
  } catch {
    applyDocumentBrand(brand.name, brand.faviconData)
  }
  try {
    const r = await fetch('/api/brand?t=' + Date.now(), {
      cache: 'no-store',
      headers: { 'Cache-Control': 'no-cache' },
    })
    if (!r.ok) return brand
    const j = await r.json()
    if (j) {
      setBrandMeta({
        name: j.panel_name || brand.name,
        subtitle: j.panel_subtitle != null ? j.panel_subtitle : brand.subtitle,
        faviconData: j.favicon_data != null ? j.favicon_data : brand.faviconData,
      })
    }
  } catch {
    /* ignore network */
  }
  return brand
}
