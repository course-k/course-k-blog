import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import sharp from 'sharp'

// Use the bundled font on hosts and minimal containers alike.
process.env.FONTCONFIG_FILE = fileURLToPath(new URL('./assets/fonts/fonts.conf', import.meta.url))
const fontfile = fileURLToPath(new URL('./assets/fonts/NotoSansJP.ttf', import.meta.url))
const brandDirectory = new URL('./dist/client/brand/', import.meta.url)
const escapeMarkup = text => text.replace(/[&<>"']/g, char => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;',
})[char])
let brandAssets

async function loadBrandAssets() {
  // These are bundled assets, never paths supplied by an incoming request.
  brandAssets ??= Promise.all([
    readFile(new URL('mark.svg', brandDirectory)).then(data => sharp(data).resize({ width: 57 }).png().toBuffer()),
    readFile(new URL('wordmark.png', brandDirectory)).then(data => sharp(data).resize({ width: 350 }).png().toBuffer()),
    sharp({ text: { text: '<span foreground="#626972">blog.course-k.com</span>', font: 'Noto Sans JP 22', fontfile, rgba: true } }).png().toBuffer(),
  ])
  return brandAssets
}

export async function renderArticleImage(title) {
  const text = `<span foreground="#292d32">${escapeMarkup(title.replace(/[\u0000-\u001f\u007f]/g, ' ').trim())}</span>`
  const input = { text, font: 'Noto Sans JP Bold 64', fontfile, width: 1036, wrap: 'word-char', spacing: 14, rgba: true }
  let rendered = await sharp({ text: input }).png().toBuffer({ resolveWithObject: true })
  // Short titles stay at 64px; only titles exceeding the available area shrink.
  if (rendered.info.height > 250) {
    rendered = await sharp({ text: { ...input, height: 250 } }).png().toBuffer({ resolveWithObject: true })
  }
  const [mark, wordmark, domain] = await loadBrandAssets()
  return sharp({ create: { width: 1200, height: 630, channels: 3, background: '#fff' } })
    .composite([
      { input: mark, left: 82, top: 70 },
      { input: wordmark, left: 157, top: 74 },
      { input: rendered.data, left: 82, top: 225 },
      { input: domain, left: 82, top: 534 },
    ])
    .png()
    .toBuffer()
}

export function createArticleImageHandler({
  apiBase = process.env.API_BASE_URL || 'http://127.0.0.1:8081',
  fetchPost = globalThis.fetch,
  renderImage = renderArticleImage,
} = {}) {
  const cache = new Map()
  return async request => {
    const { pathname } = new URL(request.url)
    if (!pathname.startsWith('/og/posts/')) return null
    const match = /^\/og\/posts\/([A-Za-z0-9_-]{43})\.png$/.exec(pathname)
    if (!match) return new Response('Not found', { status: 404 })
    if (!['GET', 'HEAD'].includes(request.method)) {
      return new Response(null, { status: 405, headers: { Allow: 'GET, HEAD' } })
    }
    const id = match[1]
    let post
    try {
      // Recheck the public API on every request, even when an image is cached.
      // No owner cookies or authorization headers are forwarded to this API.
      const response = await fetchPost(`${apiBase}/api/posts/${id}`, { signal: AbortSignal.timeout(5000) })
      if (response.status === 404) return new Response('Not found', { status: 404 })
      if (!response.ok) return new Response('Service unavailable', { status: 502 })
      post = await response.json()
      if (post.id !== id || typeof post.title !== 'string' || !post.title.trim() || [...post.title].length > 200) {
        return new Response('Service unavailable', { status: 502 })
      }
    } catch {
      return new Response('Service unavailable', { status: 502 })
    }
    const key = `${id}\0${post.title}`
    let png = cache.get(key)
    if (!png) {
      try {
        png = await renderImage(post.title)
      } catch {
        return new Response('Service unavailable', { status: 503 })
      }
      if (cache.size >= 16) cache.delete(cache.keys().next().value)
      cache.set(key, png)
    }
    return new Response(request.method === 'HEAD' ? null : png, {
      headers: {
        'Content-Type': 'image/png',
        'Content-Length': String(png.length),
        'Cache-Control': 'no-store',
      },
    })
  }
}
