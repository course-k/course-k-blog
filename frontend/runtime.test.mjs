import test from 'node:test'
import assert from 'node:assert/strict'
import { createHandler } from './runtime.mjs'

test('health response bypasses both SSR and static dependencies', async () => {
 const fail = () => { throw new Error('upstream unavailable') }
 const handler = createHandler({ render: fail, staticFiles: fail })
 for (const method of ['GET','HEAD']) {
  const result = await handler(new Request('http://localhost/healthz',{method}))
  assert.equal(result.status,200)
  assert.equal(result.headers.get('Cache-Control'),'no-store')
  assert.equal(await result.text(),method==='HEAD'?'':'{"ok":true}')
 }
})
test('static, SSR, not-found and failure all have protection headers', async () => {
 for (const path of ['/asset.js','/posts/example','/missing','/error']) {
  const handler = createHandler({
   staticFiles: (request,next) => new URL(request.url).pathname==='/asset.js' ? new Response('asset') : next(),
   render: request => { if (path==='/error') throw new Error('private detail'); return new Response('html',{status:path==='/missing'?404:200}) },
  })
  const result = await handler(new Request('http://localhost'+path))
  assert.equal(result.headers.get('X-Frame-Options'),'DENY')
  assert.equal(result.headers.get('X-Content-Type-Options'),'nosniff')
  assert.equal(result.headers.get('Referrer-Policy'),'strict-origin-when-cross-origin')
  assert.equal(result.status,path==='/error'?500:path==='/missing'?404:200)
  assert.ok(!(await result.text()).includes('private detail'))
 }
})
test('response wrapper preserves distinct cookies and redirect status', async () => {
 const headers=new Headers({Location:'/admin'})
 headers.append('Set-Cookie','a=1; HttpOnly'); headers.append('Set-Cookie','b=2; HttpOnly')
 const handler=createHandler({staticFiles:(_,next)=>next(),render:()=>new Response(null,{status:303,headers})})
 const result=await handler(new Request('http://localhost/'))
 assert.equal(result.status,303); assert.equal(result.headers.get('Location'),'/admin')
 assert.deepEqual(result.headers.getSetCookie(),['a=1; HttpOnly','b=2; HttpOnly'])
})
