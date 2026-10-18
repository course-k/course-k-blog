import { spawn, spawnSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { chmod, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createServer } from 'node:net'
import { fileURLToPath } from 'node:url'
import { createLocalConfig } from './local-config.mjs'

const root = fileURLToPath(new URL('../', import.meta.url))
const directory = await mkdtemp(join(tmpdir(), 'blog-e2e-'))
const project = `blog-e2e-${randomBytes(6).toString('hex')}`
const override = join(directory, 'compose.override.yaml')
const compose = ['compose', '-p', project, '-f', `${root}compose.yaml`, '-f', override]
let configured = false
console.log(`Disposable E2E project: ${project}`)

async function run(command, args, options = {}) {
  const child = spawn(command, args, { cwd: root, stdio: 'inherit', timeout: 600_000, ...options })
  await new Promise((resolve, reject) => {
    child.once('error', reject)
    child.once('exit', code => code === 0 ? resolve() : reject(new Error(`${command} failed (${code})`)))
  })
}
async function unusedPort() {
  const server = createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  return port
}
try {
  const port = await unusedPort()
  await createLocalConfig(directory, password => {
    const result = spawnSync('go', ['run', './cmd/password'], {
      cwd: `${root}backend`, input: `${password}\n`, encoding: 'utf8',
    })
    if (result.status !== 0) throw new Error('Cannot generate disposable test credentials')
    return result.stdout
  })
  // Linux Compose file secrets retain the host file mode. The app runs as UID 65532.
  // This disposable directory is 0700; allow its two bind-mounted files to be read
  // by the container user without granting other host users directory access.
  for (const name of ['db-password.txt', 'admin-hash.txt']) {
    await chmod(join(directory, name), 0o444)
  }
  // Same Dockerfiles, Caddy routing and service definitions as local development.
  // Only credentials, published ports and owner origin differ. Compose project owns its DB volume.
  await writeFile(override, `services:
  db:
    ports: !override []
  migrate:
    environment:
      APP_ORIGIN: http://localhost:${port}
  api:
    environment:
      APP_ORIGIN: http://localhost:${port}
  gateway:
    ports: !override ["127.0.0.1:${port}:8080"]
secrets:
  db_password:
    file: ${join(directory, 'db-password.txt')}
  admin_hash:
    file: ${join(directory, 'admin-hash.txt')}
`, { mode: 0o600 })
  configured = true
  await run('docker', [...compose, 'up', '--build', '--detach', '--wait', '--wait-timeout', '180'])
  const origin = `http://localhost:${port}`
  let ready = false
  for (let attempt = 0; attempt < 60; attempt++) {
    ready = await fetch(`${origin}/api/readyz`).then(response => response.ok, () => false)
    if (ready) break
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  if (!ready) throw new Error('Disposable API did not become ready')
  await run('npm', ['run', 'test:e2e'], {
    cwd: `${root}frontend`, env: { ...process.env, RUN_LOCAL_E2E: '1',
      BLOG_E2E_ORIGIN: origin, BLOG_E2E_LOGIN_FILE: join(directory, 'local-login.txt') },
  })
} finally {
  // This exact random project owns every resource removed here; never target the development project.
  if (configured) {
    const cleanup = spawnSync('docker', [...compose, 'down', '--volumes', '--rmi', 'local'], { cwd: root, stdio: 'inherit', timeout: 60_000 })
    if (cleanup.status !== 0) throw new Error(`Cleanup failed; remove only Compose project ${project}. Credentials retained at ${directory}`)
  }
  await rm(directory, { recursive: true, force: true })
}
