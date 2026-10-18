import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, readdir, rm, writeFile, stat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createLocalConfig } from './local-config.mjs'

async function fixture(t) {
  const directory = await mkdtemp(join(tmpdir(), 'blog-config-test-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  return directory
}
test('creates a matching private configuration set', async t => {
  const directory = await fixture(t)
  let password
  await createLocalConfig(directory, input => { password = input; return 'test-hash' })
  assert.match(await readFile(join(directory, 'local-login.txt'), 'utf8'), new RegExp(`Password: ${password}`))
  assert.equal(await readFile(join(directory, 'admin-hash.txt'), 'utf8'), 'test-hash')
  for (const file of await readdir(directory)) assert.equal((await stat(join(directory, file))).mode & 0o777, 0o600)
})
for (const existing of ['db-password.txt', 'admin-hash.txt', 'local-login.txt']) {
  test(`partial configuration: preserves ${existing} without hashing or writing`, async t => {
    const directory = await fixture(t)
    await writeFile(join(directory, existing), 'existing')
    await assert.rejects(createLocalConfig(directory, () => { throw new Error('must not hash') }), /上書きはしません/)
    assert.deepEqual(await readdir(directory), [existing])
    assert.equal(await readFile(join(directory, existing), 'utf8'), 'existing')
  })
}
test('hash failure publishes no files', async t => {
  const directory = await fixture(t)
  await assert.rejects(createLocalConfig(directory, () => { throw new Error('hash failed') }), /hash failed/)
  assert.deepEqual(await readdir(directory), [])
})
test('publication collision rolls back its files and preserves the concurrent file', async t => {
  const directory = await fixture(t)
  await assert.rejects(createLocalConfig(directory, async () => {
    await writeFile(join(directory, 'local-login.txt'), 'concurrent-file')
    return 'test-hash'
  }), { code: 'EEXIST' })
  assert.deepEqual(await readdir(directory), ['local-login.txt'])
  assert.equal(await readFile(join(directory, 'local-login.txt'), 'utf8'), 'concurrent-file')
})
