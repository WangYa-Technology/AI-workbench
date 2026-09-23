import { defineConfig, devices } from '@playwright/test'

if (!process.env.HCAI_LIVE_AUTH_EMAIL || !process.env.HCAI_LIVE_AUTH_INPUT_DIR) {
  throw new Error('Live auth testing requires an explicitly chosen inbox and private input directory')
}

// Explicit opt-in only; never starts services, seeds data, or changes the running app.
export default defineConfig({
  testDir: './e2e', testMatch: 'identity-full-flow.spec.ts', workers: 1,
  outputDir: '../.tmp/live-auth-results',
  reporter: [['list']],
  use: { ...devices['Desktop Chrome'], baseURL: 'http://127.0.0.1:5173', trace: 'off', screenshot: 'off', video: 'off' },
})
