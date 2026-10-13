import { expect, test } from '@playwright/test'

for (const width of [1527, 1000, 390]) {
  test(`inspiration shares catalog controls and preserves actions at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 891 })
    await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
    let hold = false
    let release: (() => void) | undefined
    const pending = new Promise<void>(resolve => { release = resolve })
    const requests: string[] = []
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: { id: 'viewer', handle: 'creator', displayName: 'Creator', email: 'fixture@example.test', role: 'member', status: 'active', permissions: [] } } })
      if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: {} } })
      if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (url.pathname === '/api/v1/meta') return route.fulfill({ json: {} })
      if (url.pathname === '/api/v1/works') {
        requests.push(url.search)
        if (hold) await pending
        const empty = url.searchParams.get('q') === 'missing'
        return route.fulfill({ json: { total: empty ? 0 : 12, nextCursor: null, categoryCounts: { image: empty ? 0 : 12 }, items: empty ? [] : Array.from({ length: 12 }, (_, i) => ({ id: `work-${i}`, title: `Reading club visual study ${i + 1}`, summary: 'A clear composition with references, production notes and a reusable visual rhythm.', mediaKind: 'image', mediaUrl: `/tasks/examples/example-${String(i + 1).padStart(2, '0')}.svg`, author: { handle: 'studio', displayName: 'Studio Creator' }, modelName: 'Image model', promptVisibility: i % 2 ? 'public' : 'private', publishedAt: '2026-10-08T09:00:00Z' })) } })
      }
      return route.fulfill({ status: 500, json: { error: { message: `Unmocked ${url.pathname}` } } })
    })
    await page.goto('/discover?kind=image&promptVisibility=public')
    const cards = page.locator('.inspiration-card')
    await expect(cards).toHaveCount(12)
    await expect(page.locator('.content-context-bar')).toHaveCount(0)
    await expect(page.locator('.catalog-views')).toHaveCount(0)
    await expect(cards.first().getByRole('link', { name: 'Create with reference', exact: true })).toHaveAttribute('href', '/create/image?sourceWorkId=work-0')
    await expect(cards.first().getByRole('link', { name: 'View work', exact: true })).toHaveAttribute('href', '/works/work-0')
    if (width > 767) {
      const targets = await cards.first().evaluate(el => {
        const r = el.getBoundingClientRect(), a = el.querySelector('[href="/creators/studio"]')!.getBoundingClientRect()
        return { card: document.elementFromPoint(r.right - 8, r.top + 8)?.closest('a')?.getAttribute('href'), author: document.elementFromPoint(a.left + 5, a.top + 5)?.closest('a')?.getAttribute('href') }
      })
      expect(targets).toEqual({ card: '/works/work-0', author: '/creators/studio' })
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('list.png') })
    await page.getByRole('button', { name: 'Grid view', exact: true }).click()
    await expect(page.locator('.inspiration-catalog')).toHaveClass(/is-grid/)
    if (width > 767) expect((await cards.first().boundingBox())!.width).toBeGreaterThanOrEqual(300)
    await page.screenshot({ path: testInfo.outputPath('grid.png') })
    await page.evaluate(() => {
      if (innerWidth <= 767) window.scrollTo(0, 900)
      else document.querySelector('#main-content')!.scrollTop = 900
    })
    await expect(page.locator('.catalog-sticky-bar')).toHaveClass(/is-stuck/)
    if (width > 767) await expect.poll(() => page.locator('.catalog-sticky-bar').evaluate(el => el.getBoundingClientRect().height)).toBe(56)
    if (width > 1320) {
      expect(await page.locator('.category-sidebar').evaluate(el => el.getBoundingClientRect().top - document.querySelector('.catalog-sticky-bar')!.getBoundingClientRect().bottom)).toBeGreaterThanOrEqual(10)
      const before = await page.locator('#main-content').evaluate(el => el.scrollTop)
      hold = true
      const reset = await page.locator('.catalog-reset').boundingBox()
      await page.mouse.click(reset!.x + reset!.width / 2, reset!.y + reset!.height / 2)
      await expect(page.locator('.inspiration-results')).toHaveAttribute('aria-busy', 'true')
      await expect(page.locator('.catalog-sticky-bar')).toHaveClass(/is-stuck/)
      expect(await page.locator('#main-content').evaluate(el => el.scrollTop)).toBe(before)
      hold = false
      release!()
      await expect(page.locator('.inspiration-results')).toHaveAttribute('aria-busy', 'false')
    }
    await page.goto('/discover?kind=image&promptVisibility=public')
    if (width < 1024) await page.getByRole('button', { name: 'Open search', exact: true }).click()
    const input = page.locator('.global-search-input')
    await expect(page.getByRole('combobox', { name: 'Search scope', exact: true })).toHaveText('Inspiration library')
    await expect(page.locator('.inspiration-filters input')).toHaveCount(0)
    await input.fill('图')
    await input.press('Enter')
    await expect(page.locator('.catalog-keyword')).toContainText('图')
    expect(new URL(page.url()).searchParams.get('kind')).toBe('image')
    expect(new URL(page.url()).searchParams.get('promptVisibility')).toBe('public')
    await page.getByRole('button', { name: 'Clear search keyword', exact: true }).click()
    await expect(input).toHaveValue('')
    await page.goBack()
    await expect(input).toHaveValue('图')
    await page.reload()
    if (width < 1024) await page.getByRole('button', { name: 'Open search', exact: true }).click()
    await expect(input).toHaveValue('图')
    const previous = requests.length
    await input.fill('图'.repeat(121))
    await input.press('Enter')
    await expect(page.locator('#header-search-error')).toBeVisible()
    expect(requests.length).toBe(previous)
    await input.fill('missing')
    await input.press('Enter')
    await expect(page.locator('.inspiration-empty')).toBeVisible()
    await expect(page.locator('.catalog-views')).toHaveCount(0)
    await page.getByRole('combobox', { name: 'Search scope', exact: true }).click()
    await page.getByRole('option', { name: 'All content', exact: true }).click()
    const fullInput = page.locator('.global-search-input');
    await fullInput.fill('creative work')
    await fullInput.press('Enter')
    await expect(page).toHaveURL(/\/search\?q=creative/)
  })
}
