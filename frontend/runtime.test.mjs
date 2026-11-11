import test from 'node:test'
import assert from 'node:assert/strict'
import { createHandler } from './runtime.mjs'

test('health response bypasses both SSR and static dependencies', async () => {
  const fail = () => { throw new Error('upstream unavailable') }
  const handler = createHandler({ render: fail, staticFiles: fail })
  for (const method of ['GET', 'HEAD']) {
    const result = await handler(new Request('http://localhost/healthz', { method }))
    assert.equal(result.status, 200)
    assert.equal(result.headers.get('Cache-Control'), 'no-store')
    assert.equal(await result.text(), method === 'HEAD' ? '' : '{"ok":true}')
  }
})

test('static, SSR, not-found and failure all have protection headers', async () => {
  const cases = [
    { path: '/asset.js', status: 200 },
    { path: '/posts/example', status: 200 },
    { path: '/missing', status: 404 },
    { path: '/error', status: 500 },
  ]
  for (const { path, status } of cases) {
    const handler = createHandler({
      staticFiles: (request, next) => new URL(request.url).pathname === '/asset.js'
        ? new Response('asset') : next(),
      render: () => {
        if (path === '/error') throw new Error('private detail')
        return new Response('html', { status })
      },
    })
    const result = await handler(new Request('http://localhost' + path))
    assert.equal(result.headers.get('X-Frame-Options'), 'DENY', path)
    assert.equal(result.headers.get('X-Content-Type-Options'), 'nosniff', path)
    assert.equal(result.headers.get('Referrer-Policy'), 'strict-origin-when-cross-origin', path)
    assert.equal(result.status, status, path)
    assert.ok(!(await result.text()).includes('private detail'), path)
  }
})

test('response wrapper preserves distinct cookies and redirect status', async () => {
  const headers = new Headers({ Location: '/admin' })
  headers.append('Set-Cookie', 'a=1; HttpOnly')
  headers.append('Set-Cookie', 'b=2; HttpOnly')
  const handler = createHandler({
    staticFiles: (_, next) => next(),
    render: () => new Response(null, { status: 303, headers }),
  })
  const result = await handler(new Request('http://localhost/'))
  assert.equal(result.status, 303)
  assert.equal(result.headers.get('Location'), '/admin')
  assert.deepEqual(result.headers.getSetCookie(), ['a=1; HttpOnly', 'b=2; HttpOnly'])
})

test('first page permanently redirects before rendering and preserves other parameters', async () => {
  const fail = () => { throw new Error('redirect must bypass rendering') }
  const handler = createHandler({ render: fail, staticFiles: fail })
  for (const method of ['GET', 'HEAD']) {
    for (const [path, location] of [
      ['/?page=1', '/'],
      ['/?page=1&tag=react', '/?tag=react'],
      ['/?tag=react&page=1', '/?tag=react'],
    ]) {
      const result = await handler(new Request('http://localhost' + path, { method }))
      assert.equal(result.status, 308)
      assert.equal(result.headers.get('Location'), location)
      assert.equal(result.headers.get('X-Content-Type-Options'), 'nosniff')
      assert.equal(await result.text(), '')
    }
  }
})

test('first-page redirect leaves home, later pages and other endpoints unchanged', async () => {
  const handler = createHandler({
    staticFiles: (_, next) => next(),
    render: () => new Response('rendered'),
  })
  for (const [path, method] of [
    ['/', 'GET'],
    ['/?page=2', 'GET'],
    ['/?page=3', 'GET'],
    ['/admin?page=1', 'GET'],
    ['/posts/example?page=1', 'GET'],
    ['/?page=1&page=2', 'GET'],
    ['/?page=1', 'POST'],
  ]) {
    const result = await handler(new Request('http://localhost' + path, { method }))
    assert.equal(result.status, 200, path)
    assert.equal(result.headers.get('Location'), null, path)
    assert.equal(await result.text(), 'rendered', path)
  }
})

test('with an edge cache, only successful public pages may be kept by it', async () => {
  const cookie = new Headers({ 'Set-Cookie': 'a=1' })
  const handler = createHandler({
    edgeSeconds: 86400,
    staticFiles: (_, next) => next(),
    articleImages: request => new URL(request.url).pathname.startsWith('/og/') ? new Response('png') : null,
    render: request => {
      const { pathname } = new URL(request.url)
      if (pathname === '/posts/missing') return new Response('not found', { status: 404 })
      if (pathname === '/posts/broken') throw new Error('private detail')
      if (pathname === '/posts/cookie') return new Response('html', { headers: cookie })
      return new Response('html')
    },
  })
  const kept = 'public, max-age=0, s-maxage=86400'
  const cases = [
    ['GET', '/', kept],
    ['HEAD', '/', kept],
    ['GET', '/?page=2&month=2026-10', kept],
    ['GET', '/posts/example', kept],
    ['GET', '/og/posts/example.png', kept],
    ['GET', '/_serverFn/abc?payload=x', kept],
    // The owner's screens and anything that is not a plain success stay private.
    ['GET', '/admin', 'no-store'],
    ['GET', '/admin/anything', 'no-store'],
    ['GET', '/healthz', 'no-store'],
    ['GET', '/?page=1', 'no-store'],
    ['GET', '/posts/missing', 'no-store'],
    ['GET', '/posts/broken', 'no-store'],
    ['GET', '/posts/cookie', 'no-store'],
    ['POST', '/_serverFn/abc', 'no-store'],
  ]
  for (const [method, path, expected] of cases) {
    const result = await handler(new Request('http://localhost' + path, { method }))
    assert.equal(result.headers.get('Cache-Control'), expected, `${method} ${path}`)
  }
})

test('without an edge cache nothing is cacheable, as before', async () => {
  const handler = createHandler({ staticFiles: (_, next) => next(), render: () => new Response('html') })
  for (const path of ['/', '/posts/example', '/admin']) {
    const result = await handler(new Request('http://localhost' + path))
    assert.equal(result.headers.get('Cache-Control'), 'no-store', path)
  }
})
