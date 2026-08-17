import { expect, test } from '@playwright/test'

test('lets a visitor browse a task and return after creating an account', async ({ page }) => {
  const runID = Date.now().toString(36)
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/market/demands')

  await expect(page.getByRole('heading', { name: 'Task marketplace', exact: true })).toBeVisible()
  const firstTask = page.locator('.task-row').first()
  await expect(firstTask).toBeVisible()
  await firstTask.click()

  await expect(page).toHaveURL(/\/market\/demands\/[0-9a-f-]+$/)
  const taskPath = new URL(page.url()).pathname
  await expect(page.getByRole('heading', { name: 'Ready to work on this brief?', exact: true })).toBeVisible()
  await expect(page.getByText('Tasks are temporarily unavailable', { exact: true })).toHaveCount(0)
  await page.locator('.market-auth-prompt').getByRole('link', { name: 'Create account', exact: true }).click()

  await expect(page).toHaveURL(/\/settings\?auth=register/)
  await expect(page.locator('.auth-tabs').getByRole('button', { name: 'Create account', exact: true })).toHaveClass(/active/)
  await page.getByLabel('Display name', { exact: true }).fill('Public Market Visitor')
  await page.getByLabel('Handle', { exact: true }).fill(`visitor_${runID}`)
  await page.getByLabel('Email', { exact: true }).fill(`visitor-${runID}@example.com`)
  await page.locator('.account-form').getByLabel('Password', { exact: false }).fill('visitor-password-2026')
  await page.locator('.account-form').getByRole('button', { name: 'Create account', exact: true }).click()

  await expect(page).toHaveURL(taskPath)
  await expect(page.getByRole('button', { name: 'Submit proposal', exact: true })).toBeVisible()
})

test('lets an anonymous visitor inspect product rights before sign-in', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/market')

  await expect(page.getByRole('heading', { name: 'Digital marketplace', exact: true })).toBeVisible()
  const firstProduct = page.locator('.product-card').first()
  await expect(firstProduct).toBeVisible()
  await firstProduct.click()

  await expect(page.getByRole('heading', { name: 'Sign in to license this product', exact: true })).toBeVisible()
  await expect(page.getByText('Local Test price', { exact: true })).toBeVisible()
  await expect(page.getByText('I reviewed and accept', { exact: false })).toHaveCount(0)
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await page.locator('.market-auth-prompt').getByRole('link', { name: 'Sign in', exact: true }).click()
  await expect(page).toHaveURL(/\/settings\?auth=login/)
  await expect(page.locator('.auth-tabs').getByRole('button', { name: 'Sign in', exact: true })).toHaveClass(/active/)
})
