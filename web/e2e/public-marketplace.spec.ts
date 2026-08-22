import { expect, test } from '@playwright/test'

test('lets a visitor browse a task and return after creating an account', async ({ page }) => {
  const runID = Date.now().toString(36)
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/market/demands')

  await expect(page.getByRole('heading', { name: 'Task marketplace', exact: true })).toBeVisible()
  const signIn = page.getByRole('link', { name: 'Sign in', exact: true }).first()
  await expect(signIn).toHaveCSS('border-radius', '8px')
  await expect(signIn).toHaveCSS('box-shadow', 'none')
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

test('keeps desktop header actions separate and icon controls square', async ({ page }) => {
  await page.setViewportSize({ width: 1257, height: 950 })
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/market')

  const search = page.locator('.global-search')
  const searchInput = page.locator('.global-search-input')
  const searchSubmit = page.locator('.global-search-submit')
  const publish = page.locator('.header-publish-action')
  await expect(search).toBeVisible()
  await expect(publish).toBeVisible()

  const [searchBox, inputBox, submitBox, publishBox] = await Promise.all([
    search.boundingBox(),
    searchInput.boundingBox(),
    searchSubmit.boundingBox(),
    publish.boundingBox(),
  ])
  expect(searchBox).not.toBeNull()
  expect(inputBox).not.toBeNull()
  expect(submitBox).not.toBeNull()
  expect(publishBox).not.toBeNull()
  expect(searchBox!.x + searchBox!.width).toBeLessThanOrEqual(publishBox!.x)
  expect(submitBox!.y).toBeGreaterThanOrEqual(inputBox!.y)
  expect(submitBox!.y + submitBox!.height).toBeLessThanOrEqual(inputBox!.y + inputBox!.height)

  const sortSelect = page.locator('.market-filter-control').nth(1)
  const sortTrigger = sortSelect.locator('.ui-select__trigger')
  await expect(page.locator('label.market-filter-control')).toHaveCount(0)
  await sortTrigger.focus()
  await expect(sortSelect).toHaveCSS('box-shadow', 'none')
  await expect(sortTrigger).toHaveCSS('border-radius', '8px')

  const iconSizes = await page.locator('.ui-icon-button').evaluateAll(elements => elements.map((element) => {
    const rect = element.getBoundingClientRect()
    return { width: rect.width, height: rect.height }
  }))
  expect(iconSizes.length).toBeGreaterThan(0)
  for (const size of iconSizes) expect(Math.abs(size.width - size.height)).toBeLessThanOrEqual(0.5)
})
