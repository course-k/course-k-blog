import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'
const root = fileURLToPath(new URL('../', import.meta.url))
const password = (await readFile(`${root}config/db-password.txt`, 'utf8')).trim()
const url = new URL('postgres://127.0.0.1:5432/blog_test?sslmode=disable')
url.username = 'blog'; url.password = password
const result = spawnSync('go', ['test', '-v', './internal/postgres'], {
  cwd: `${root}backend`, stdio: 'inherit',
  env: { ...process.env, TEST_DATABASE_URL: url.href, GOCACHE: `${root}config/go-build`, GOPATH: `${root}config/go-path` },
})
process.exit(result.status ?? 1)
