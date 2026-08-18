import { expect, test } from '@playwright/test'

test('enforces operations permissions and records an audited credit adjustment', async ({ page }) => {
  const creatorSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(creatorSession.ok()).toBeTruthy()
  await page.goto('/admin')
  await expect(page.getByRole('heading', { name: 'Operations access required', exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Use local operations account', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Finance', exact: true })).toBeVisible()
  await expect(page.getByText('Providers', { exact: true }).last()).toBeVisible()

  await page.getByRole('button', { name: 'Finance', exact: true }).click()
  const target = page.locator('.finance-admin-list article').filter({ hasText: 'Northstar Studio' })
  await expect(target).toBeVisible()
  await target.getByRole('button', { name: 'Adjust credits', exact: true }).click()
  await page.getByLabel('Adjustment in cents', { exact: true }).fill('1')
  await page.getByLabel('Required reason', { exact: true }).fill('Repeatable browser test of an audited Local Test support adjustment')
  await page.getByLabel('I reviewed the target and confirm this operation.', { exact: true }).check()
  await page.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Audit', exact: true }).click()
  await expect(page.getByText('admin.billing_adjusted', { exact: true }).first()).toBeVisible()
  await expect(page.locator('.audit-list article').filter({ hasText: 'Repeatable browser test' }).first()).toBeVisible()
})

test('charges a completed generation once and exposes the personal statement', async ({ page }) => {
  const runID = Date.now().toString(36)
  const creatorSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(creatorSession.ok()).toBeTruthy()

  await page.goto('/create/image')
  await expect(page.getByText('Available credits', { exact: true })).toBeVisible()
  await page.locator('.studio-composer textarea').fill(`Billing capture verification with precise geometry ${runID}`)
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()
  await expect(page.locator('.studio-task').first().locator('.studio-task-status')).toContainText('Saved to Assets')

  await page.getByRole('link', { name: 'View Local Test credit statement', exact: true }).click()
  await expect(page).toHaveURL(/\/workspace\/billing$/)
  await expect(page.getByRole('heading', { name: 'Account statement', exact: true })).toBeVisible()
  const charge = page.locator('.billing-entry').filter({ hasText: 'Local Test image generation' }).first()
  await expect(charge).toBeVisible()
  await expect(charge).toContainText('$0.05')
  await expect(page.getByText('Reserved for active work', { exact: true })).toBeVisible()
})

test('keeps operations usable without page overflow on a mobile viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto('/admin?tab=providers')
  await expect(page.getByRole('heading', { name: 'Operations', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Providers', exact: true })).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
  await expect(page.getByText('Local Image Test', { exact: true })).toBeVisible()
  await expect(page.getByText('External configuration required', { exact: true }).first()).toBeVisible()
})
