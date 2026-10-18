import { spawn, spawnSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { chmod, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createServer } from 'node:net'
import { fileURLToPath } from 'node:url'
import { createLocalConfig } from './local-config.mjs'

const singleTask = process.argv.includes('--single-task')
if (process.argv.slice(2).some(arg => arg !== '--single-task')) throw new Error('Unknown E2E option')

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
  // Sharing the gateway's network namespace reproduces a task's localhost links.
  // It does not reproduce a shared Fargate CPU quota, task IAM or lifecycle.
  const singleTaskOverride = singleTask ? `
  api:
    network_mode: service:gateway
    environment:
      APP_ORIGIN: http://localhost:${port}
      EXTERNAL_SYNC_ENABLED: "false"
      LISTEN_ADDR: 127.0.0.1:8081
  web:
    network_mode: service:gateway
    environment:
      HOST: 127.0.0.1
      API_BASE_URL: http://127.0.0.1:8081
  gateway:
    image: ${project}-router
    build: ${root}infra/router
    depends_on: !reset {}
    volumes: !override [router_tmp:/tmp/nginx]
    read_only: true
    cap_drop: [ALL]
    ports: !override ["127.0.0.1:${port}:8080"]
` : ''
  // Use the production Dockerfiles with a disposable DB and credentials.
  // Each random Compose project owns its DB, media, backup and router scratch volumes.
  await writeFile(override, `services:
  db:
    ports: !override []
  migrate:
    environment:
      APP_ORIGIN: http://localhost:${port}
${singleTask ? singleTaskOverride : `  api:
    environment:
      APP_ORIGIN: http://localhost:${port}
      EXTERNAL_SYNC_ENABLED: "false"
  gateway:
    ports: !override ["127.0.0.1:${port}:8080"]
`}

secrets:
  db_password:
    file: ${join(directory, 'db-password.txt')}
  admin_hash:
    file: ${join(directory, 'admin-hash.txt')}
volumes:
  router_tmp:
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
  if (singleTask) {
    const router = await fetch(`${origin}/gateway-healthz`)
    if (!router.ok || (await router.json()).ok !== true) throw new Error('Task router health check failed')
    const nonApi = await fetch(`${origin}/apix`, { redirect: 'manual' })
    if (nonApi.status !== 404 || !nonApi.headers.get('content-type')?.includes('text/html')) throw new Error('API path boundary failed')
    // A proxy's default error/access formats may expose OIDC codes or cookies.
    const sentinel = `router-credential-fixture-${randomBytes(12).toString('hex')}`
    await fetch(`${origin}/api/session?code=${sentinel}`, { headers: { Authorization: `Bearer ${sentinel}`, Cookie: `fixture=${sentinel}` } })
    const logs = spawnSync('docker', [...compose, 'logs', '--no-color', 'gateway'], { cwd: root, encoding: 'utf8', timeout: 10_000 })
    if (logs.status !== 0 || `${logs.stdout}${logs.stderr}`.includes(sentinel)) throw new Error('Router credential-log check failed')
  }
  // Fixed metadata isolates acceptance tests from upstream availability and article changes.
  // Supply credentials through the container environment, never CLI arguments.
  await run('docker', [...compose, 'exec', '-T', 'db', 'sh', '-c',
    'export MYSQL_PWD="$(cat /run/secrets/db_password)"; exec mysql -u blog blog_test -e "$1"', 'seed',
    "INSERT INTO external_posts(source,source_id,title,url,published_at) VALUES('qiita','e2e-external','外部の記事の元タイトル','https://qiita.com/course_k/items/e2e-external','2020-01-01 00:00:00')"])
  await run('npm', ['run', 'test:e2e'], {
    cwd: `${root}frontend`, env: { ...process.env, RUN_LOCAL_E2E: '1',
      BLOG_E2E_ORIGIN: origin, BLOG_E2E_LOGIN_FILE: join(directory, 'local-login.txt') },
  })
} catch (error) {
  if (configured) spawnSync('docker', [...compose, 'logs', '--no-color', '--tail', '20', 'migrate'], { cwd: root, stdio: 'inherit', timeout: 10_000 })
  if (singleTask && configured) spawnSync('docker', [...compose, 'logs', '--no-color', '--tail', '30', 'gateway'], { cwd: root, stdio: 'inherit', timeout: 10_000 })
  throw error
} finally {
  // This exact random project owns every resource removed here; never target the development project.
  if (configured) {
    const cleanup = spawnSync('docker', [...compose, 'down', '--volumes', '--rmi', 'local'], { cwd: root, stdio: 'inherit', timeout: 60_000 })
    if (cleanup.status !== 0) throw new Error(`Cleanup failed; remove only Compose project ${project}. Credentials retained at ${directory}`)
  }
  await rm(directory, { recursive: true, force: true })
}
