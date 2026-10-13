import { defineConfig } from '@playwright/test'
import base from './playwright.ui.config'
export default defineConfig(base, {
  testMatch: ['**/tasks-experience.ui.spec.ts', '**/orders-experience.ui.spec.ts', '**/purchases-experience.ui.spec.ts', '**/order-delivery.ui.spec.ts', '**/asset-experience.ui.spec.ts', '**/asset-handover.ui.spec.ts', '**/asset-review-fixes.ui.spec.ts'],
  outputDir: './test-results/assets-purchases-ui/tests',
  reporter: [['list'], ['html', { open: 'never', outputFolder: './test-results/assets-purchases-ui/report' }], ['json', { outputFile: './test-results/assets-purchases-ui/results.json' }]],
  use: { trace: 'on', screenshot: 'on' },
})
