import { expect, test } from '@playwright/test'

for (const width of [1551, 390]) {
  test(`market pagination preserves filters, retries and totals at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 901 })
    const response = await page.request.get('/api/v1/products')
    expect(response.ok()).toBeTruthy()
    const base = (await response.json()).items[0]
    expect(base).toBeTruthy()
    const first = { ...base, title: 'Catalogue first page' }
    const second = { ...base, id: '00000000-0000-4000-8000-000000009902', title: 'Catalogue next page' }
    let attempts = 0
    await page.route(/\/api\/v1\/products(?:\?.*)?$/, async route => {
      const params = new URL(route.request().url()).searchParams
      expect(params.get('q')).toBe('catalogue')
      expect(params.get('category')).toBe('market_workflow')
      expect(params.get('sort')).toBe('price_asc')
      const counts = { market_workflow: 2 }
      if (params.has('cursor')) {
        expect(params.get('cursor')).toBe('catalogue-next')
        attempts++
        if (attempts === 1) {
          await route.fulfill({ status: 503, json: { error: { code: 'temporary_failure', message: 'Temporary failure', retryable: true } } })
          return
        }
        // A concurrent catalogue change may return a previously loaded item.
        await route.fulfill({ json: { items: [first, second], total: 2, categoryCounts: counts } })
      } else {
        await route.fulfill({ json: { items: [first], total: 2, categoryCounts: counts, nextCursor: 'catalogue-next' } })
      }
    })
    await page.goto('/market?q=catalogue&category=market_workflow&sort=price_asc')
    await expect(page.getByRole('heading', { name: first.title, exact: true })).toBeVisible()
    await expect(page.locator('.market-results-summary strong')).toHaveText(/^2 /)
    await page.locator('.market-search input').fill('unsubmitted draft')
    await page.getByRole('button', { name: 'Load more', exact: true }).click()
    await expect(page.locator('.catalog-pagination [role="alert"]')).toBeVisible()
    await expect(page.locator('.product-card')).toHaveCount(1)
    const retry = page.locator('.catalog-pagination').getByRole('button', { name: 'Try again', exact: true })
    await retry.scrollIntoViewIfNeeded()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
    const bounds = await retry.boundingBox()
    expect(bounds).not.toBeNull()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    await page.screenshot({ path: testInfo.outputPath(`market-pagination-${width}.png`), fullPage: true })
    await retry.click()
    await expect(page.getByRole('heading', { name: second.title, exact: true })).toBeVisible()
    await expect(page.locator('.product-card')).toHaveCount(2)
    await expect(page.locator('.catalog-pagination')).toHaveCount(0)
    expect(attempts).toBe(2)
  })
}

for (const lateFailure of [false, true]) {
  test(`market ignores late continuation ${lateFailure ? 'failure' : 'success'} after changing filters`, async ({ page }) => {
    const response = await page.request.get('/api/v1/products')
    expect(response.ok()).toBeTruthy()
    const base = (await response.json()).items[0]
    const first = { ...base, title: 'Original catalogue' }
    const stale = { ...base, id: '00000000-0000-4000-8000-000000009903', title: 'Stale catalogue continuation' }
    const current = { ...base, id: '00000000-0000-4000-8000-000000009904', title: 'Filtered catalogue' }
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    await page.route(/\/api\/v1\/products(?:\?.*)?$/, async route => {
      const params = new URL(route.request().url()).searchParams
      if (params.has('cursor')) {
        await gate
        await route.fulfill(lateFailure
          ? { status: 503, json: { error: { code: 'temporary_failure', message: 'Stale failure', retryable: true } } }
          : { json: { items: [stale], total: 2, categoryCounts: {} } })
      } else if (params.get('q') === 'filtered') {
        await route.fulfill({ json: { items: [current], total: 1, categoryCounts: {} } })
      } else {
        await route.fulfill({ json: { items: [first], total: 2, categoryCounts: {}, nextCursor: 'stale-next' } })
      }
    })
    try {
      await page.goto('/market')
      await expect(page.getByRole('heading', { name: first.title, exact: true })).toBeVisible()
      const pending = page.waitForRequest(request => request.url().includes('cursor=stale-next'))
      await page.getByRole('button', { name: 'Load more', exact: true }).click()
      await pending
      await page.locator('.market-search input').fill('filtered')
      await page.locator('.market-search-submit').click()
      await expect(page.getByRole('heading', { name: current.title, exact: true })).toBeVisible()
      const finished = page.waitForResponse(response => response.url().includes('cursor=stale-next'))
      release()
      await (await finished).finished()
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.product-card')).toHaveCount(1)
      await expect(page.getByRole('heading', { name: stale.title, exact: true })).toHaveCount(0)
      await expect(page.locator('.market-results-summary strong')).toHaveText(/^1 /)
      await expect(page.locator('.market-page [role="alert"]')).toHaveCount(0)
    } finally {
      release()
    }
  })
}
