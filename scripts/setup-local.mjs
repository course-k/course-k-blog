import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'
import { createLocalConfig } from './local-config.mjs'

const root = fileURLToPath(new URL('../', import.meta.url))
const config = `${root}config/`
await createLocalConfig(config, password => {
  const result = spawnSync('go', ['run', './cmd/password'], {
    cwd: `${root}backend`, input: `${password}\n`, encoding: 'utf8',
    env: { ...process.env, GOCACHE: `${config}go-build`, GOPATH: `${config}go-path` },
  })
  if (result.status !== 0) throw new Error('パスワード設定を生成できませんでした。Go 1.27以上の実行環境を確認してください。')
  return result.stdout
})
console.log('ローカル専用の設定を生成しました。ログイン情報はapp/config/local-login.txtに保存されています。')
