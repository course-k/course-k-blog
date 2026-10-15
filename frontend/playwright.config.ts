import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  workers: 1,
  timeout: 60_000,
  use: { baseURL: 'http://localhost:8080', channel: 'chrome', headless: true },
  reporter: 'list',
})
