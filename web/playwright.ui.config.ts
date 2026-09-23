import { defineConfig, devices } from '@playwright/test'
import { fileURLToPath } from 'node:url'

const port = process.env.E2E_UI_WEB_PORT || '15179'
if (!/^\d+$/.test(port) || Number(port) < 1024 || Number(port) > 65535) {
  throw new Error('E2E_UI_WEB_PORT must be a port between 1024 and 65535')
}

// UI contract tests intercept every API request and need no database, mail,
// worker or payment service. Serve a production build on a dedicated port.
export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.ui.spec.ts',
  outputDir: './test-results/ui-contracts',
  timeout: 30_000,
  expect: { timeout: 5_000 },
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    cwd: fileURLToPath(new URL('.', import.meta.url)),
    command: `npm run build && node ./node_modules/vite/bin/vite.js preview --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
