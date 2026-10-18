import { defineConfig } from '@playwright/test'

const channel = process.env.PLAYWRIGHT_CHANNEL || 'chrome'
if (!['chrome', 'chromium'].includes(channel)) throw new Error('Choose chrome or chromium explicitly.')

export default defineConfig({
  testDir: './e2e',
  workers: 1,
  timeout: 60_000,
  use: { baseURL: 'http://localhost:8080', channel: channel === 'chrome' ? 'chrome' : undefined, headless: true },
  reporter: 'list',
})
