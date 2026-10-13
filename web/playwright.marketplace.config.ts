import { defineConfig, devices } from '@playwright/test'

if (!/^hcai_e2e_[0-9a-f]{32}$/.test(process.env.E2E_OWNED_DATABASE || '') || !process.env.E2E_ARTIFACTS) {
  throw new Error('Run via scripts/e2e-marketplace.py; an owned disposable database is required')
}
export default defineConfig({
  testDir: './e2e',
  testMatch: ['**/purchased-assets-pagination.spec.ts', '**/asset-experience.spec.ts', '**/asset-upload.spec.ts', '**/asset-publish-drawer.spec.ts', '**/asset-usage.spec.ts', '**/content-drafts-asset-versions.spec.ts', '**/saved-assets.spec.ts', '**/marketplace-backend.spec.ts', '**/product-publication.spec.ts', '**/product-media.payment.spec.ts', '**/public-marketplace.spec.ts', '**/support-lifecycle.spec.ts'],
  timeout: 90_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  outputDir: `${process.env.E2E_ARTIFACTS}/tests`,
  reporter: [['list'], ['html', { open: 'never', outputFolder: `${process.env.E2E_ARTIFACTS}/report` }], ['json', { outputFile: `${process.env.E2E_ARTIFACTS}/results.json` }]],
  use: {
    baseURL: `http://127.0.0.1:${process.env.E2E_WEB_PORT}`,
    trace: 'on', screenshot: 'on',
  },
  projects: [{ name: 'isolated-chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 900 } } }],
})
