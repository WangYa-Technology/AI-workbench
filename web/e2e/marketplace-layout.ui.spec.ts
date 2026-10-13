import { expect, test } from '@playwright/test'

for (const width of [1527, 1000, 390]) {
  test(`resource marketplace shares catalog layout and search at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 891 })
    await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
    let fail = false, failMore = false, hold = false
    let release: () => void = () => {}
    const pending = new Promise<void>(resolve => { release = resolve })
    const requests: string[] = []
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user: { id: 'viewer', handle: 'creator', displayName: 'Creator', email: 'fixture@example.test', role: 'member', status: 'active', permissions: [] } } })
      if (url.pathname.endsWith('/site-config')) return route.fulfill({ json: { siteName: 'HCAI CHAT', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: {} } })
      if (url.pathname.endsWith('/notifications')) return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (url.pathname.endsWith('/meta')) return route.fulfill({ json: { paymentProvider: { enabled: true, liveMode: false } } })
      if (url.pathname.endsWith('/task-types')) return route.fulfill({ json: { items: [{ code: 'market_image', nameEn: 'Images', nameZh: '图片', icon: 'image' }, { code: 'market_prompt', nameEn: 'Prompts', nameZh: '提示词', icon: 'prompt' }] } })
      if (url.pathname.endsWith('/products')) {
        requests.push(url.search)
        if (hold) await pending
        if (fail || (failMore && url.searchParams.has('cursor'))) return route.fulfill({ status: 503, json: { error: { message: 'Catalog temporarily unavailable' } } })
        const empty = url.searchParams.get('q') === 'missing', more = url.searchParams.has('cursor')
        return route.fulfill({ json: { total: empty ? 0 : 13, nextCursor: empty || more ? null : 'next', categoryCounts: { market_image: empty ? 0 : 13 }, items: empty ? [] : Array.from({ length: more ? 1 : 12 }, (_, n) => {
          const i = more ? 12 : n
          return { id: `resource-${i}`, title: `Editorial image collection ${i + 1}`, description: 'A cohesive set of visual assets for your next creative project. Includes production-ready original files.', category: 'market_image', productType: 'asset', mediaUrl: `/tasks/examples/example-${String(i % 12 + 1).padStart(2, '0')}.svg`, mediaKind: 'image', priceCents: 2400 + i * 100, currency: 'USD', seller: { id: 'seller', handle: 'studio', displayName: 'Studio Creator' }, license: { name: 'Creator license', allowsCommercial: i % 2 === 0 }, ownedAssetId: i === 0 ? 'owned-asset' : undefined }
        }) } })
      }
      return route.fulfill({ status: 500, json: { error: { message: `Unmocked ${url.pathname}` } } })
    })
    await page.goto('/market?category=market_image&sort=price_asc')
    const cards = page.locator('.product-card')
    await expect(cards).toHaveCount(12)
    await expect(page.locator('.catalog-views')).toHaveCount(0)
    await expect(cards.first()).toContainText('$24.00')
    await expect(cards.first()).toContainText('Commercial end products allowed')
    await expect(cards.first()).toContainText('Owned')
    await expect(cards.nth(1)).toContainText('Personal, non-commercial use only')
    await expect(cards.first().getByRole('link', { name: 'View details', exact: true })).toHaveAttribute('href', '/market/assets/resource-0')
    await expect(cards.first().getByRole('link', { name: 'Studio Creator', exact: true })).toHaveAttribute('href', '/creators/studio')
    if (width > 767) {
      const targets = await cards.first().evaluate(el => {
        const r = el.getBoundingClientRect(), a = el.querySelector('[href="/creators/studio"]')!.getBoundingClientRect()
        return { card: document.elementFromPoint(r.right - 8, r.top + 8)?.closest('a')?.getAttribute('href'), seller: document.elementFromPoint(a.left + 5, a.top + 5)?.closest('a')?.getAttribute('href') }
      })
      expect(targets).toEqual({ card: '/market/assets/resource-0', seller: '/creators/studio' })
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('list.png') })
    await page.getByRole('button', { name: 'Grid view', exact: true }).click()
    await expect(page.locator('.market-catalog')).toHaveClass(/is-grid/)
    if (width > 767) expect((await cards.first().boundingBox())!.width).toBeGreaterThanOrEqual(300)
    await page.screenshot({ path: testInfo.outputPath('grid.png') })
    await page.evaluate(() => {
      if (innerWidth <= 767) window.scrollTo(0, 900)
      else document.querySelector('#main-content')!.scrollTop = 900
    })
    await expect(page.locator('.catalog-sticky-bar')).toHaveClass(/is-stuck/)
    if (width > 1320) {
      await expect.poll(() => page.locator('.catalog-sticky-bar').evaluate(el => el.getBoundingClientRect().height)).toBe(56)
      expect(await page.locator('.category-sidebar').evaluate(el => el.getBoundingClientRect().top - document.querySelector('.catalog-sticky-bar')!.getBoundingClientRect().bottom)).toBeGreaterThanOrEqual(10)
      const before = await page.locator('#main-content').evaluate(el => el.scrollTop)
      hold = true
      const reset = await page.locator('.catalog-reset').boundingBox()
      await page.mouse.click(reset!.x + reset!.width / 2, reset!.y + reset!.height / 2)
      await expect(page.locator('.market-results')).toHaveAttribute('aria-busy', 'true')
      expect(await page.locator('#main-content').evaluate(el => el.scrollTop)).toBe(before)
      hold = false; release()
      await expect(page.locator('.market-results')).toHaveAttribute('aria-busy', 'false')
    }
    await page.goto('/market?category=market_image&sort=price_asc')
    if (width < 1024) await page.getByRole('button', { name: 'Open search', exact: true }).click()
    const input = page.locator('.global-search-input')
    await expect(page.getByRole('combobox', { name: 'Search scope', exact: true })).toHaveText('Resource market')
    await expect(page.locator('.market-filters input')).toHaveCount(0)
    await input.fill('图'); await input.press('Enter')
    await expect(page.locator('.catalog-keyword')).toContainText('图')
    expect(new URL(page.url()).searchParams.get('category')).toBe('market_image')
    expect(new URL(page.url()).searchParams.get('sort')).toBe('price_asc')
    await expect.poll(() => requests.at(-1)).toContain('q=%E5%9B%BE')
    await page.getByRole('button', { name: 'Clear search keyword', exact: true }).click()
    await expect(input).toHaveValue('')
    await page.goBack(); await expect(input).toHaveValue('图')
    await page.reload()
    if (width < 1024) await page.getByRole('button', { name: 'Open search', exact: true }).click()
    await expect(input).toHaveValue('图')
    await page.getByRole('combobox', { name: 'Sort resources', exact: true }).click()
    await page.getByRole('option', { name: 'Price: high to low', exact: true }).click()
    await expect(page).toHaveURL(/sort=price_desc/)
    failMore = true
    await page.getByRole('button', { name: 'Load more', exact: true }).click()
    await expect(page.locator('.catalog-pagination [role="alert"]')).toBeVisible()
    await expect(cards).toHaveCount(12)
    failMore = false
    await page.locator('.catalog-pagination').getByRole('button', { name: 'Try again', exact: true }).click()
    await expect(cards).toHaveCount(13)
    // Return to the top before operating the header on narrow screens.
    await page.goto('/market?q=missing')
    await expect(page.locator('.ui-empty-state')).toBeVisible()
    await expect(page.locator('.catalog-views')).toHaveCount(0)
    fail = true
    await page.locator('.catalog-reset').click()
    await expect(page.locator('.market-results [role="alert"]')).toBeVisible()
    await expect(page.locator('.catalog-views')).toHaveCount(0)
    fail = false
    await page.locator('.market-results').getByRole('button', { name: 'Try again', exact: true }).click()
    await expect(cards).toHaveCount(12)
  })
}
