import { expect, test } from '@playwright/test'

test('market catalog switches layouts, filters and opens product details', async ({ page }) => {
  await page.goto('/market')
  const catalog = page.locator('.market-catalog')
  await expect(catalog.locator('.product-card').first()).toBeVisible()
  await expect(catalog).not.toHaveClass(/is-grid/)
  const count = await catalog.locator('.product-card').count()
  await page.getByRole('button', { name: 'Grid view', exact: true }).click()
  await expect(catalog).toHaveClass(/is-grid/)
  await expect(catalog.locator('.product-card')).toHaveCount(count)
  await page.getByRole('button', { name: 'List view', exact: true }).click()
  await expect(catalog).not.toHaveClass(/is-grid/)
  await page.locator('.market-search input').fill('no-such-product-987654321')
  await page.locator('.market-search-submit').click()
  await expect(page).toHaveURL(/q=no-such-product/)
  await page.getByRole('button', { name: 'Clear filters', exact: true }).click()
  await expect(catalog.locator('.product-card')).toHaveCount(count)
  for (const width of [1551, 1308, 390]) {
    await page.setViewportSize({ width, height: 901 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
    await expect(page.locator('.category-filter')).toBeVisible({ visible: width <= 1320 })
  }
  const card = catalog.locator('.product-card').first()
  const title = await card.locator('h2').innerText()
  await card.click()
  await expect(page).toHaveURL(/\/market\/assets\//)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
})
