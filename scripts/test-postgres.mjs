import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const root = fileURLToPath(new URL('../', import.meta.url))
const connection = process.env.TEST_DATABASE_URL
if (!connection) {
  throw new Error('TEST_DATABASE_URLで専用のテストDBを指定してください。既存の開発DBへは自動接続しません。')
}
const target = new URL(connection)
if (!['postgres:', 'postgresql:'].includes(target.protocol) || !target.pathname.endsWith('_test')) {
  throw new Error('DB名が_testで終わるPostgreSQLの専用接続先が必要です。')
}
const result = spawnSync('go', ['test', '-count=1', '-race', '-v', './internal/postgres'], {
  cwd: `${root}backend`, stdio: 'inherit',
  env: { ...process.env, TEST_TLS_DATABASE_URL: '', GOCACHE: `${root}config/go-build`, GOPATH: `${root}config/go-path` },
})
if (result.error) throw new Error('Goを起動できませんでした。実行環境を確認してください。')
process.exit(result.status ?? 1)
