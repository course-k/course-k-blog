// Reader-facing data comes from one of two places. With PUBLIC_DATA_URL set,
// lists and posts are read from the files the API publishes, so rendering
// needs neither the API nor the database. Without it, the API answers.
export class NotFound extends Error {}

const unavailable = () => new Error('記事を取得できませんでした。')

function source(env) {
  const files = env.PUBLIC_DATA_URL?.replace(/\/+$/, '')
  return { files, api: env.API_BASE_URL || 'http://127.0.0.1:8081' }
}

async function read(fetch, url) {
  const response = await fetch(url, { signal: AbortSignal.timeout(5000) })
  if (response.status === 404) return undefined
  if (!response.ok) throw unavailable()
  return response.json()
}

const pageFile = (files, page, month) => `${files}/data/articles/${month ?? 'all'}/${page}.json`

export async function loadArticlePage({ page, month }, { fetch = globalThis.fetch, env = process.env } = {}) {
  const { files, api } = source(env)
  if (!files) {
    const query = new URLSearchParams({ page: String(page) })
    if (month) query.set('month', month)
    const result = await read(fetch, `${api}/api/articles?${query}`)
    if (!result) throw unavailable()
    return result
  }
  const result = await read(fetch, pageFile(files, page, month))
  if (result) return result
  // Only existing pages are published. A page past the end, or a month without
  // articles, is an empty list that still offers the archive months.
  const first = page === 1 && !month ? undefined : await read(fetch, pageFile(files, 1))
  if (!first) throw unavailable()
  return { months: first.months, articles: [], hasNext: false }
}

export async function loadPost(id, { fetch = globalThis.fetch, env = process.env } = {}) {
  const { files, api } = source(env)
  const post = await read(fetch, files ? `${files}/data/posts/${id}.json` : `${api}/api/posts/${encodeURIComponent(id)}`)
  if (!post) throw new NotFound('post not found')
  return post
}
