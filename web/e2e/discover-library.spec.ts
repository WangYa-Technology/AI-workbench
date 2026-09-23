import { expect, test } from '@playwright/test'

test('library filters restore from URL and both layouts preserve work actions', async ({ page }) => {
  await page.goto('/discover?kind=image')
  await expect(page.getByRole('heading', { name: 'Inspiration library', exact: true })).toBeVisible()
  const cards = page.locator('.inspiration-card')
  await expect(cards.first()).toBeVisible()
  const title = await cards.first().locator('h2').innerText()
  await page.getByRole('searchbox', { name: 'Search works', exact: true }).fill(title)
  await page.locator('.inspiration-filters').getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page).toHaveURL(/q=/)
  await page.reload()
  await expect(page.getByRole('searchbox', { name: 'Search works', exact: true })).toHaveValue(title)
  await page.getByRole('button', { name: 'Grid view', exact: true }).click()
  await expect(page.locator('.inspiration-catalog')).toHaveClass(/is-grid/)
  await expect(cards.first().getByRole('link', { name: 'Create with reference', exact: true })).toHaveAttribute('href', /sourceWorkId=/)
  for (const width of [1551, 1308, 390]) {
    await page.setViewportSize({ width, height: 901 })
    await expect(page.locator('.category-sidebar')).toBeVisible({ visible: width > 1320 })
    await expect(page.locator('.category-filter')).toBeVisible({ visible: width <= 1320 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
  }
  await page.getByRole('button', { name: 'List view', exact: true }).click()
  await page.getByRole('combobox', { name: 'Media type', exact: true }).click()
  await page.getByRole('option', { name: 'Audio', exact: true }).click()
  await expect(page).toHaveURL(/kind=audio/)
  await expect(page.locator('.inspiration-empty')).toBeVisible()
  await page.locator('.inspiration-empty').getByRole('button', { name: 'Clear filters' }).click()
  await expect(cards.first()).toBeVisible()
  await cards.first().getByRole('link', { name: 'View work', exact: true }).click()
  await expect(page).toHaveURL(/\/works\//)
})

test('library handles initial failure, pagination retry and empty data without stray sections', async ({ page }) => {
  const fixture = (await (await page.request.get('/api/v1/works')).json()).items[0]
  let initialFailed = false
  let nextFailed = false
  await page.route('**/api/v1/works*', async route => {
    const url = new URL(route.request().url())
    if (!initialFailed) { initialFailed = true; await route.abort(); return }
    if (url.searchParams.has('cursor')) {
      if (!nextFailed) { nextFailed = true; await route.abort(); return }
      await route.fulfill({ json: { items: [{ ...fixture, id: '00000000-0000-4000-8000-000000000299', title: 'Second page work' }], nextCursor: null, total: 2, categoryCounts: {} } })
    } else await route.fulfill({ json: { items: [fixture], nextCursor: 'next', total: 2, categoryCounts: {} } })
  })
  await page.goto('/discover')
  await page.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(page.locator('.inspiration-card')).toHaveCount(1)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.locator('.catalog-pagination [role=alert]')).toBeVisible()
  await expect(page.locator('.inspiration-card')).toHaveCount(1)
  await page.locator('.catalog-pagination').getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(page.locator('.inspiration-card')).toHaveCount(2)
  await expect(page.locator('.catalog-pagination')).toHaveCount(0)
  await page.unroute('**/api/v1/works*')
  await page.route('**/api/v1/works*', route => route.fulfill({ json: { items: [], nextCursor: null, total: 0, categoryCounts: {} } }))
  await page.reload()
  await expect(page.locator('.inspiration-empty')).toBeVisible()
  await expect(page.locator('.inspiration-empty').getByRole('link', { name: 'Start creating', exact: true })).toBeVisible()
  await expect(page.locator('.discover-hero, #recent, .discover-pathways')).toHaveCount(0)
})
