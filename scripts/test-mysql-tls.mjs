// Real TLS checks use a private temporary CA and a disposable MySQL instance.
import { spawnSync } from 'node:child_process'
import { mkdtemp, chmod, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createServer } from 'node:net'
import { randomBytes } from 'node:crypto'
import { fileURLToPath } from 'node:url'
const root = fileURLToPath(new URL('../', import.meta.url))
const directory = await mkdtemp(join(tmpdir(), 'blog-mysql-tls-'))
const container = `blog-mysql-tls-${randomBytes(6).toString('hex')}`
let started = false
function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', timeout: 180_000, ...options })
  if (result.status !== 0) throw new Error(`${command} failed: ${result.stderr || result.error || 'unknown error'}`)
  return result.stdout
}
try {
  for (const name of ['ca', 'wrong']) run('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', `/CN=${name}`, '-keyout', join(directory, `${name}.key`), '-out', join(directory, `${name}.pem`)])
  run('openssl', ['req', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=localhost', '-keyout', join(directory, 'server.key'), '-out', join(directory, 'server.csr')])
  await writeFile(join(directory, 'server.ext'), 'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\n')
  run('openssl', ['x509', '-req', '-days', '1', '-in', join(directory, 'server.csr'), '-CA', join(directory, 'ca.pem'), '-CAkey', join(directory, 'ca.key'), '-CAcreateserial', '-extfile', join(directory, 'server.ext'), '-out', join(directory, 'server.pem')])
  // Host directory is 0700; mysqld's container UID needs these bind-mounted files.
  for (const name of ['ca.pem', 'server.pem', 'server.key']) await chmod(join(directory, name), 0o444)
  const server = createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  run('docker', ['run', '--rm', '-d', '--name', container, '-e', 'MYSQL_ALLOW_EMPTY_PASSWORD=yes', '-e', 'MYSQL_ROOT_HOST=%', '-e', 'MYSQL_DATABASE=blog_test', '-p', `127.0.0.1:${port}:3306`, '-v', `${directory}:/tls:ro`, 'mysql:8.4', '--ssl-ca=/tls/ca.pem', '--ssl-cert=/tls/server.pem', '--ssl-key=/tls/server.key', '--require-secure-transport=ON', '--log-bin-trust-function-creators=ON'])
  started = true
  let ready = false
  for (let attempt = 0; attempt < 90; attempt++) {
    ready = spawnSync('docker', ['exec', container, 'mysqladmin', 'ping', '-h', '127.0.0.1', '--silent'], { timeout: 5000 }).status === 0
    if (ready) break
    await new Promise(resolve => setTimeout(resolve, 1000))
  }
  if (!ready) throw new Error('TLS MySQL did not become ready')
  const target = new URL(`mysql://root@localhost:${port}/blog_test?tls=verify-full`)
  target.searchParams.set('sslrootcert', join(directory, 'ca.pem'))
  const appPassword = randomBytes(32).toString('base64url')
  const migrationPassword = randomBytes(32).toString('base64url')
  const env = { ...process.env, DATABASE_HOST: 'localhost', DATABASE_PORT: String(port), DATABASE_NAME: 'blog_test', DATABASE_USER: 'root', DATABASE_PASSWORD: '', DATABASE_CA_FILE: join(directory, 'ca.pem'), BLOG_APP_PASSWORD: appPassword, BLOG_MIGRATOR_PASSWORD: migrationPassword }
  run('go', ['run', './cmd/bootstrap'], { cwd: `${root}backend`, env })
  const migration = new URL(target)
  migration.username = 'blog_migrator'
  migration.password = migrationPassword
  run('go', ['run', './cmd/api', 'migrate'], { cwd: `${root}backend`, env: { ...env, APP_ORIGIN: 'http://localhost:8080', DATABASE_URL: migration.href } })
  const runtime = new URL(target)
  runtime.username = 'blog_app'
  runtime.password = appPassword
  run('go', ['test', '-count=1', '-race', '-v', './internal/mysql', '-run', '^Test(TLSVerification|RuntimeDatabasePrivileges)$'], {
    cwd: `${root}backend`, stdio: 'inherit', env: { ...process.env, TEST_DATABASE_URL: '', TEST_TLS_DATABASE_URL: target.href, TEST_TLS_WRONG_CA_FILE: join(directory, 'wrong.pem'), TEST_RUNTIME_DATABASE_URL: runtime.href },
  })
} finally {
  if (started) run('docker', ['stop', container])
  await rm(directory, { recursive: true, force: true })
}
