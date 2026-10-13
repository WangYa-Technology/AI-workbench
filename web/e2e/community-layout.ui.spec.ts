import { expect, test } from '@playwright/test'

// Isolated presentation fixtures: no posts are published to the development database.
for (const width of [1527, 1000, 390]) {
  test(`community controls remain usable over populated content at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 891 })
    await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
    let release: (() => void) | undefined
    let hold = false
    const pending = new Promise<void>(resolve => { release = resolve })
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      const path = url.pathname
      if (path === '/api/v1/community/posts' && hold) await pending
      if (path === '/api/v1/auth/session') return route.fulfill({ json: { user: { id: 'viewer', handle: 'creator', displayName: 'Creator', email: 'fixture@example.test', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: [] } } })
      if (path === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: {} } })
      if (path === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (path === '/api/v1/meta') return route.fulfill({ json: {} })
      if (path === '/api/v1/task-types') return route.fulfill({ json: { items: [{ code: 'general', nameEn: 'General', nameZh: '综合', icon: 'mixed' }, { code: 'showcase', nameEn: 'Showcase', nameZh: '作品展示', icon: 'image' }] } })
      if (path === '/api/v1/community/capabilities') return route.fulfill({ json: { canSaveDraft: true, canPublish: true } })
      if (path === '/api/v1/community/posts') return route.fulfill({ json: { total: 12, categoryCounts: { general: 6, showcase: 6 }, items: Array.from({ length: 12 }, (_, i) => ({ id: `post-${i}`, title: ['How I planned a visual identity for a weekend reading club', 'Building a reusable subtitle workflow', 'A product photography lighting study'][i % 3], body: 'Presentation fixture: sharing the references, production process and lessons from this creative project. What would you approach differently?', category: i % 2 ? 'general' : 'showcase', authorName: 'Studio Creator', authorHandle: 'studio', publishedAt: '2026-10-08T09:00:00Z', status: url.searchParams.get('view') === 'drafts' ? 'draft' : 'published', mediaUrl: i % 3 ? `/tasks/examples/example-${String(i + 1).padStart(2, '0')}.svg` : undefined, mediaKind: i % 3 ? 'image' : undefined, commentCount: i + 2, likeCount: i * 3 })) } })
      return route.fulfill({ status: 500, json: { error: { message: `Unmocked ${path}` } } })
    })
    await page.goto('/community')
    await expect(page.locator('.community-post-row')).toHaveCount(12)
    await expect(page.locator('.content-context-bar')).toHaveCount(0)
    await expect(page.locator('.catalog-sticky-title')).toBeHidden()
    await expect(page.locator('.community-controls-row')).toContainText('12')
    const overflow = () => page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
    expect(await overflow()).toBe(false)
    await page.screenshot({ path: testInfo.outputPath('list.png') })
    await page.locator('.community-controls-row .ui-layout-switcher button').last().click()
    await expect(page.locator('.community-feed')).toHaveClass(/is-grid/)
    await page.screenshot({ path: testInfo.outputPath('grid.png') })
    await page.evaluate(() => {
      if (innerWidth <= 767) window.scrollTo(0, 900)
      else document.querySelector('#main-content')!.scrollTop = 900
    })
    await expect(page.locator('.catalog-sticky-bar')).toHaveClass(/is-stuck/)
    await expect.poll(() => page.locator('.catalog-sticky-bar').evaluate(el => {
      const root = document.querySelector('#main-content')!
      const edge = innerWidth <= 767 ? 52 : root.getBoundingClientRect().top + root.clientTop
      return Math.abs(el.getBoundingClientRect().top - edge)
    })).toBeLessThan(2)
    if (width > 767) {
      await expect(page.locator('.catalog-sticky-title')).toBeVisible()
      const alignment = await page.locator('.view-switcher-bar').evaluate(el => {
        const tabs = el.getBoundingClientRect()
        const title = document.querySelector('.catalog-sticky-title')!.getBoundingClientRect()
        return { centerGap: Math.abs((tabs.top + tabs.bottom - title.top - title.bottom) / 2), horizontalGap: tabs.left - title.right }
      })
      expect(alignment.centerGap).toBeLessThan(2)
      expect(alignment.horizontalGap).toBeGreaterThan(0)
    } else {
      await expect(page.locator('.catalog-sticky-title')).toBeHidden()
    }
    if (width === 1527) {
      await expect.poll(() => page.locator('.catalog-sticky-bar').evaluate(el => el.getBoundingClientRect().height)).toBeLessThan(64)
    }
    if (width > 1320) {
      const gap = await page.locator('.category-sidebar').evaluate(el => el.getBoundingClientRect().top - document.querySelector('.community-controls-row')!.getBoundingClientRect().bottom)
      expect(gap).toBeGreaterThanOrEqual(10)
      const scrollBefore = await page.locator('#main-content').evaluate(el => el.scrollTop)
      hold = true
      // Click the visible sticky tab without locator auto-scroll changing the baseline.
      const target = await page.getByRole('tab').nth(1).boundingBox()
      await page.mouse.click(target!.x + target!.width / 2, target!.y + target!.height / 2)
      await expect(page.locator('.community-results')).toHaveAttribute('aria-busy', 'true')
      const stable = await page.evaluate(async () => {
        const start = performance.now()
        let stable = true
        while (performance.now() - start < 400) {
          await new Promise(requestAnimationFrame)
          const bar = document.querySelector('.catalog-sticky-bar')!
          stable &&= bar.classList.contains('is-stuck') && Math.abs(bar.getBoundingClientRect().height - 56) < 1
        }
        return stable
      })
      release!()
      expect(stable).toBe(true)
      await expect(page.locator('.community-results')).toHaveAttribute('aria-busy', 'false')
      await expect.poll(() => page.locator('#main-content').evaluate(el => el.scrollTop)).toBe(scrollBefore)
      await expect(page.getByRole('tab', { selected: true }).locator('.catalog-view-count')).toHaveText('12')
    }
    await page.locator('.community-sort-control button').click()
    await page.getByRole('option').last().click()
    await expect(page).toHaveURL(/sort=discussed/)
    await page.locator('.community-tools > button').click()
    await expect(page.locator('.community-tools [role="menuitem"]')).toHaveCount(2)
    await page.keyboard.press('Escape')
    await expect(page.locator('.community-tools > button')).toBeFocused()
    await page.getByRole('tab').last().focus()
    await page.getByRole('tab').last().press('Enter')
    await expect(page).toHaveURL(/view=drafts/)
    await expect(page.locator('.community-post-row').first()).toContainText('Edit')
    expect(await overflow()).toBe(false)
    await page.evaluate(() => {
      if (innerWidth <= 767) window.scrollTo(0, 0)
      else document.querySelector('#main-content')!.scrollTop = 0
    })
    await expect(page.locator('.catalog-sticky-title')).toBeHidden()
    const input = page.locator('.global-search-input')
    if (width < 1024) await page.getByRole('button', { name: 'Open search', exact: true }).click()
    await expect(page.getByRole('combobox', { name: 'Search scope' })).toHaveText('Community')
    await expect(page.locator('.community-controls-row input[type="search"]')).toHaveCount(0)
    await input.fill('图')
    await input.press('Enter')
    await expect(page.locator('.catalog-keyword')).toContainText('图')
    expect(new URL(page.url()).searchParams.get('view')).toBe('drafts')
    expect(new URL(page.url()).searchParams.get('sort')).toBe('discussed')
    await page.getByRole('button', { name: 'Clear search keyword', exact: true }).click()
    await expect(input).toHaveValue('')
    await page.goBack()
    await expect(input).toHaveValue('图')
    await page.reload()
    if (width < 1024) await page.getByRole('button', { name: 'Open search', exact: true }).click()
    await expect(input).toHaveValue('图')
    await page.getByRole('combobox', { name: 'Search scope' }).click()
    await page.getByRole('option', { name: 'All content', exact: true }).click()
    await input.fill('creative work')
    await input.press('Enter')
    await expect(page).toHaveURL(/\/search\?q=creative/)
  })
}
