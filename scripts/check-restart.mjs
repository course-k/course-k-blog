import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'
import { deepStrictEqual } from 'node:assert'
const root = fileURLToPath(new URL('../', import.meta.url))
const origin = 'http://localhost:8080'
const text = await readFile(`${root}config/local-login.txt`, 'utf8')
const password = text.match(/^Password: (.+)$/m)?.[1]
const login = await fetch(`${origin}/api/session`, { method: 'POST', headers: { Origin: origin, 'Content-Type': 'application/json' }, body: JSON.stringify({ password }) })
if (!login.ok) throw new Error('Local fixture login failed')
const cookie = login.headers.getSetCookie()[0]?.split(';')[0]
if (!cookie) throw new Error('No session cookie')
async function articles() {
  const r = await fetch(`${origin}/api/admin/posts`, { headers: { Cookie: cookie } })
  if (!r.ok) throw new Error(`Authenticated read failed: ${r.status}`)
  return (await r.json()).sort((a,b) => a.id.localeCompare(b.id))
}
const before = await articles()
const restart = spawnSync('docker', ['compose', 'restart', 'db', 'api', 'web'], { cwd: root, encoding: 'utf8' })
if (restart.status !== 0) throw new Error('Compose restart failed')
let ready = false
for (let i = 0; i < 60; i++) {
  try { if ((await fetch(`${origin}/api/readyz`)).ok && (await fetch(`${origin}/`)).ok) { ready = true; break } } catch {}
  await new Promise(resolve => setTimeout(resolve, 500))
}
if (!ready) throw new Error('Services did not become ready after restart')
deepStrictEqual(await articles(), before)
const logout = await fetch(`${origin}/api/session`, { method: 'DELETE', headers: { Origin: origin, 'Content-Type': 'application/json', Cookie: cookie }, body: '{}' })
if (!logout.ok) throw new Error('Fixture logout failed')
console.log(JSON.stringify({ restart: 'pass', articleCount: before.length, articleContentsUnchanged: true, sessionPersisted: true }))
