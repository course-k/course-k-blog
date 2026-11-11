import test from 'node:test'
import assert from 'node:assert/strict'
import { loadArticlePage, loadPost, NotFound } from './publicData.mjs'

const id = 'a'.repeat(43)
const months = [{ month: '2026-10', count: 22 }]

// Answers from a table of URL -> JSON body; anything else is a 404.
function fixture(table) {
  const requested = []
  const fetch = async url => {
    requested.push(String(url))
    const body = table[String(url)]
    if (body instanceof Error) throw body
    if (typeof body === 'number') return new Response('{}', { status: body })
    return body === undefined
      ? new Response('missing', { status: 404 })
      : new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } })
  }
  return { fetch, requested }
}

test('without a file location the API answers, as before', async () => {
  const page = { months, articles: [], hasNext: false }
  const { fetch, requested } = fixture({
    'http://api:8081/api/articles?page=2&month=2026-10': page,
    [`http://api:8081/api/posts/${id}`]: { id, title: 't' },
  })
  const env = { API_BASE_URL: 'http://api:8081' }
  assert.deepEqual(await loadArticlePage({ page: 2, month: '2026-10' }, { fetch, env }), page)
  assert.deepEqual(await loadPost(id, { fetch, env }), { id, title: 't' })
  assert.deepEqual(requested, ['http://api:8081/api/articles?page=2&month=2026-10', `http://api:8081/api/posts/${id}`])
})

test('with a file location lists and posts are read from the published files', async () => {
  const all = { months, articles: [{ kind: 'own', id }], hasNext: true }
  const october = { months, articles: [], hasNext: false }
  const { fetch, requested } = fixture({
    'https://files.example/data/articles/all/1.json': all,
    'https://files.example/data/articles/2026-10/3.json': october,
    [`https://files.example/data/posts/${id}.json`]: { id, title: 't' },
  })
  // A trailing slash in the setting must not produce a double slash.
  const env = { PUBLIC_DATA_URL: 'https://files.example/', API_BASE_URL: 'http://api:8081' }
  assert.deepEqual(await loadArticlePage({ page: 1 }, { fetch, env }), all)
  assert.deepEqual(await loadArticlePage({ page: 3, month: '2026-10' }, { fetch, env }), october)
  assert.deepEqual(await loadPost(id, { fetch, env }), { id, title: 't' })
  assert.ok(requested.every(url => url.startsWith('https://files.example/data/')), 'the API must not be contacted')
})

test('a page past the end is an empty list that keeps the archive months', async () => {
  const { fetch } = fixture({
    'https://files.example/data/articles/all/1.json': { months, articles: [{ kind: 'own', id }], hasNext: false },
  })
  const env = { PUBLIC_DATA_URL: 'https://files.example' }
  const expected = { months, articles: [], hasNext: false }
  assert.deepEqual(await loadArticlePage({ page: 9 }, { fetch, env }), expected)
  assert.deepEqual(await loadArticlePage({ page: 1, month: '2019-01' }, { fetch, env }), expected)
})

test('files that were never published are an error, not an empty blog', async () => {
  // The API always writes the first page, even for a blog without articles.
  const { fetch } = fixture({})
  await assert.rejects(
    loadArticlePage({ page: 1 }, { fetch, env: { PUBLIC_DATA_URL: 'https://files.example' } }),
    error => !(error instanceof NotFound),
  )
})

test('a missing post is reported as not found in both modes', async () => {
  const { fetch } = fixture({})
  await assert.rejects(loadPost(id, { fetch, env: { PUBLIC_DATA_URL: 'https://files.example' } }), NotFound)
  await assert.rejects(loadPost(id, { fetch, env: { API_BASE_URL: 'http://api:8081' } }), NotFound)
})

test('an unavailable source is an error, never an empty blog', async () => {
  const env = { PUBLIC_DATA_URL: 'https://files.example' }
  const failing = fixture({
    'https://files.example/data/articles/all/1.json': 503,
    [`https://files.example/data/posts/${id}.json`]: new Error('network down'),
  })
  await assert.rejects(loadArticlePage({ page: 1 }, { fetch: failing.fetch, env }), error => !(error instanceof NotFound))
  await assert.rejects(loadPost(id, { fetch: failing.fetch, env }), error => !(error instanceof NotFound))
})
