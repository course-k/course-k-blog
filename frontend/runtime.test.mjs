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
