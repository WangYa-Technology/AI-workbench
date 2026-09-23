import { defineConfig } from '@playwright/test'
import base from './playwright.config'

// Uses the same isolated schema as the regular suite: run suites sequentially.
export default defineConfig({
  ...base,
  testIgnore: [],
  testMatch: ['**/*.payment.spec.ts', '**/text-preview.spec.ts'],
  webServer: {
    ...base.webServer,
    command: './scripts/e2e.sh',
    cwd: '..',
    url: `http://127.0.0.1:${process.env.E2E_WEB_PORT ?? '15173'}/health`,
    env: { E2E_PAYMENT_FIXTURE: '1' },
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
