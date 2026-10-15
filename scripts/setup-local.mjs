import { randomBytes } from 'node:crypto'
import { mkdir, writeFile, access } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const root = fileURLToPath(new URL('../', import.meta.url))
const config = `${root}config/`
if (await access(`${config}admin-hash.txt`).then(() => true, () => false)) {
  throw new Error('既存のローカル設定を上書きしません。必要なら設定とDBの対応を確認してから手動で変更してください。')
}
await mkdir(config, { recursive: true, mode: 0o700 })
const password = randomBytes(24).toString('base64url')
const result = spawnSync('go', ['run', './cmd/password'], {
  cwd: `${root}backend`, input: `${password}\n`, encoding: 'utf8',
  env: { ...process.env, GOCACHE: `${config}go-build`, GOPATH: `${config}go-path` },
})
if (result.status !== 0) throw new Error('パスワード設定を生成できませんでした。Go 1.27以上の実行環境を確認してください。')
await writeFile(`${config}db-password.txt`, randomBytes(32).toString('hex'), { mode: 0o600, flag: 'wx' })
await writeFile(`${config}admin-hash.txt`, result.stdout.trim(), { mode: 0o600, flag: 'wx' })
await writeFile(`${config}local-login.txt`, `ローカル専用です。本番には使わないでください。\nURL: http://localhost:8080/admin\nPassword: ${password}\n`, { mode: 0o600, flag: 'wx' })
console.log('ローカル専用の設定を生成しました。ログイン情報はapp/config/local-login.txtに保存されています。')
