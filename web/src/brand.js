import { reactive } from 'vue'

const DEFAULT_NAME = 'Mieru'

export const brand = reactive({
  name: DEFAULT_NAME,
  loaded: false,
})

export function brandMarkLetter(name = brand.name) {
  const s = String(name || DEFAULT_NAME).trim()
  if (!s) return 'M'
  // Prefer first CJK / letter / digit
  const m = s.match(/[\u4e00-\u9fffA-Za-z0-9]/)
  return (m ? m[0] : s[0]).toUpperCase()
}

export function applyDocumentBrand(name = brand.name) {
  const n = String(name || DEFAULT_NAME).trim() || DEFAULT_NAME
  document.title = `${n} 控制台`
  setFaviconFromName(n)
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
    // rounded square
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

    const url = canvas.toDataURL('image/png')
    let link = document.querySelector("link[rel='icon']")
    if (!link) {
      link = document.createElement('link')
      link.rel = 'icon'
      document.head.appendChild(link)
    }
    link.type = 'image/png'
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

export function setBrandName(name) {
  const n = String(name || '').trim() || DEFAULT_NAME
  brand.name = n
  brand.loaded = true
  applyDocumentBrand(n)
  try {
    localStorage.setItem('mieru_panel_name', n)
  } catch {
    /* ignore */
  }
}

/** Load brand for any page (login + admin). Uses public endpoint. */
export async function loadBrand() {
  // optimistic from cache so title/icon appear immediately
  try {
    const cached = localStorage.getItem('mieru_panel_name')
    if (cached) {
      brand.name = cached
      applyDocumentBrand(cached)
    } else {
      applyDocumentBrand(brand.name)
    }
  } catch {
    applyDocumentBrand(brand.name)
  }
  try {
    const r = await fetch('/api/brand?t=' + Date.now(), {
      cache: 'no-store',
      headers: { 'Cache-Control': 'no-cache' },
    })
    if (!r.ok) return brand
    const j = await r.json()
    if (j && j.panel_name) setBrandName(j.panel_name)
  } catch {
    /* ignore network */
  }
  return brand
}
