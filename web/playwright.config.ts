import { defineConfig, devices } from '@playwright/test'

const webPort = process.env.E2E_WEB_PORT ?? '15173'

export default defineConfig({
  testDir: './e2e',
  globalTeardown: './e2e/global-teardown.ts',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  // Workflow tests use one isolated PostgreSQL schema and deterministic demo actors.
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: `http://127.0.0.1:${webPort}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 } } },
  ],
  webServer: {
    command: './scripts/e2e.sh',
    cwd: '..',
    url: `http://127.0.0.1:${webPort}/health`,
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
