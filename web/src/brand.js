import { reactive } from 'vue'

const DEFAULT_NAME = 'Mieru'
const DEFAULT_SUBTITLE = '管理节点、用户、隧道与落地计量'

export const brand = reactive({
  name: DEFAULT_NAME,
  subtitle: DEFAULT_SUBTITLE,
  faviconData: '',
  loaded: false,
})

/**
 * Console mark letter. Product identity is always "M" (Mieru),
 * not the panel display name's first CJK char (e.g. 专).
 */
export function brandMarkLetter(_name = brand.name) {
  return 'M'
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

/**
 * Canvas favicon: square mark with letter "M".
 * Golden ratio: font size ≈ size/φ; corner radius ≈ size/(φ·4).
 */
export function setFaviconFromName(_name = brand.name) {
  try {
    const letter = 'M'
    const size = 64
    const phi = 1.618
    const canvas = document.createElement('canvas')
    canvas.width = size
    canvas.height = size
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    const r = Math.round(size / (phi * 4)) // ≈ 10
    ctx.fillStyle = '#0f766e'
    ctx.beginPath()
    ctx.moveTo(r, 0)
    ctx.arcTo(size, 0, size, size, r)
    ctx.arcTo(size, size, 0, size, r)
    ctx.arcTo(0, size, 0, 0, r)
    ctx.arcTo(0, 0, size, 0, r)
    ctx.closePath()
    ctx.fill()
    ctx.fillStyle = '#ffffff'
    const fontPx = Math.round(size / phi) // ≈ 40
    ctx.font = `650 ${fontPx}px Inter, system-ui, sans-serif`
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    // optical vertical center (slightly above mid for capital M)
    ctx.fillText(letter, size / 2, size / 2 + size * 0.02)

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
