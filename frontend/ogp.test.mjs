import test from 'node:test'
import assert from 'node:assert/strict'
import sharp from 'sharp'
import { createArticleImageHandler, renderArticleImage } from './ogp.mjs'
import { createHandler } from './runtime.mjs'

const id = 'a'.repeat(43)
const url = `http://localhost/og/posts/${id}.png`
const publicPost = title => new Response(JSON.stringify({ id, title }))

test('article PNG renders Japanese, markup characters and long unbroken titles within 1200×630', async () => {
  for (const title of ['日本語の記事 <タグ> & "引用"', '長'.repeat(200)]) {
    const png = await renderArticleImage(title)
    const info = await sharp(png).metadata()
    assert.equal(info.format, 'png')
    assert.equal(info.width, 1200)
    assert.equal(info.height, 630)
    const pixels = await sharp(png).removeAlpha().raw().toBuffer()
    let titlePixels = 0
    for (let y = 225; y < 475; y++) {
      for (let x = 82; x < 1118; x++) {
        const offset = (y * 1200 + x) * 3
        if (pixels[offset] < 128) titlePixels++
      }
    }
    assert(titlePixels > 1000, 'the article title must actually be drawn in the title area')
  }
});

test('cached images still recheck publication and never use request cookies', async () => {
  let published = true, reads = 0, renders = 0
  const handler = createArticleImageHandler({
    fetchPost: async (target, options) => {
      reads++
      assert.equal(target, `http://127.0.0.1:8081/api/posts/${id}`)
      assert.equal(options.headers, undefined)
      return published ? publicPost('公開タイトル') : new Response(null, { status: 404 })
    },
    renderImage: async () => { renders++; return Buffer.from('fixture-png') },
  })
  const first = await handler(new Request(url, { headers: { Cookie: 'owner=private' } }))
  assert.equal(first.status, 200)
  assert.equal(first.headers.get('Content-Type'), 'image/png')
  assert.equal(first.headers.get('Cache-Control'), 'no-store')
  const head = await handler(new Request(url, { method: 'HEAD' }))
  assert.equal(head.status, 200)
  assert.equal(await head.text(), '')
  assert.equal(head.headers.get('Content-Length'), String(Buffer.byteLength('fixture-png')))
  assert.equal(renders, 1)
  published = false
  const hidden = await handler(new Request(url))
  assert.equal(hidden.status, 404)
  assert.equal(reads, 3)
  assert.equal(renders, 1)
});

test('published title changes replace the image; rendering errors do not expose internals', async () => {
  let title = '公開前半', failure = false
  const rendered = []
  const handler = createArticleImageHandler({
    fetchPost: async () => publicPost(title),
    renderImage: async value => {
      if (failure) throw new Error('private renderer path')
      rendered.push(value)
      return Buffer.from(value)
    },
  })
  assert.equal((await handler(new Request(url))).status, 200)
  title = '更新した公開タイトル'
  const updated = await handler(new Request(url))
  assert.equal(await updated.text(), title)
  assert.deepEqual(rendered, ['公開前半', title])
  title = '別のタイトル'; failure = true
  const failed = await handler(new Request(url))
  assert.equal(failed.status, 503)
  assert.equal(await failed.text(), 'Service unavailable')
});

test('invalid IDs and writes never reach the public API; upstream failure is not an image', async () => {
  let reads = 0
  const images = createArticleImageHandler({ fetchPost: async () => { reads++; throw new Error('upstream detail') } })
  assert.equal(await images(new Request('http://localhost/')), null)
  assert.equal((await images(new Request('http://localhost/og/posts/invalid.png'))).status, 404)
  const write = await images(new Request(url, { method: 'POST' }))
  assert.equal(write.status, 405)
  assert.equal(write.headers.get('Allow'), 'GET, HEAD')
  assert.equal(reads, 0)
  const handler = createHandler({
    articleImages: images,
    render: () => { throw new Error('must not fall through to SSR') },
    staticFiles: () => { throw new Error('must not fall through to assets') },
  })
  const failed = await handler(new Request(url))
  assert.equal(failed.status, 502)
  assert.equal(failed.headers.get('X-Content-Type-Options'), 'nosniff')
  assert.equal(await failed.text(), 'Service unavailable')
});
