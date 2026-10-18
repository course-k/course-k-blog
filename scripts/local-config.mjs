import { randomBytes } from 'node:crypto'
import { access, link, lstat, mkdir, mkdtemp, rm, unlink, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

const files = ['db-password.txt', 'admin-hash.txt', 'local-login.txt']

// Prepare all values before publishing any file. Never combine old and new credentials.
export async function createLocalConfig(configDirectory, hashPassword) {
  await mkdir(configDirectory, { recursive: true, mode: 0o700 })
  for (const name of files) {
    const exists = await access(join(configDirectory, name)).then(() => true, error => {
      if (error.code === 'ENOENT') return false
      throw error
    })
    if (exists) throw new Error('既存または一部生成済みの設定があります。DBとの対応を確認し、設定一式を復元してから再実行してください。上書きはしません。')
  }
  const password = randomBytes(24).toString('base64url')
  const hash = await hashPassword(password)
  if (!hash.trim()) throw new Error('空のパスワードハッシュは保存できません。')
  const contents = [
    randomBytes(32).toString('hex'), hash.trim(),
    `ローカル専用です。本番には使わないでください。\nURL: http://localhost:8080/admin\nPassword: ${password}\n`,
  ]
  const staging = await mkdtemp(join(configDirectory, '.setup-'))
  const published = []
  try {
    for (let index = 0; index < files.length; index++) {
      await writeFile(join(staging, files[index]), contents[index], { mode: 0o600, flag: 'wx' })
    }
    for (const name of files) {
      const source = join(staging, name)
      const target = join(configDirectory, name)
      // Hard-link creation is exclusive: a concurrent file must never be overwritten.
      await link(source, target)
      published.push({ target, inode: (await lstat(source)).ino })
    }
  } catch (error) {
    for (const { target, inode } of published) {
      if ((await lstat(target).catch(() => null))?.ino === inode) await unlink(target)
    }
    throw error
  } finally {
    await rm(staging, { recursive: true, force: true })
  }
}
