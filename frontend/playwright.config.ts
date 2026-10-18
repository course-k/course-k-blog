import { defineConfig } from '@playwright/test'

const channel = process.env.PLAYWRIGHT_CHANNEL || 'chrome'
if (!['chrome', 'chromium'].includes(channel)) throw new Error('Choose chrome or chromium explicitly.')

const origin = process.env.BLOG_E2E_ORIGIN || 'http://localhost:8080'
const target = new URL(origin)
if (target.protocol !== 'http:' || !['localhost', '127.0.0.1'].includes(target.hostname)
  || target.pathname !== '/' || target.search || target.hash || target.username || target.password) {
  throw new Error('E2E must use an explicit loopback HTTP origin.')
}

export default defineConfig({
  testDir: './e2e',
  workers: 1,
  timeout: 60_000,
  use: { baseURL: origin, channel: channel === 'chrome' ? 'chrome' : undefined, headless: true },
  reporter: 'list',
})
