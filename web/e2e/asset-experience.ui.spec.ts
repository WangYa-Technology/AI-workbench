import { expect, test, type Page } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'
import { chooseOption } from './helpers/select'
const owner = '00000000-0000-4000-8000-000000006001'
const item = (n: number, video = false) => ({ id: `00000000-0000-4000-8000-${String(7000 + n).padStart(12, '0')}`, title: `Garden asset ${n}`, kind: video ? 'video' : 'image', sourceType: n % 2 ? 'upload' : 'generation', scanStatus: 'clean', mediaUrl: video ? `/fixtures/private-${n}.mp4` : '/brand/logo.png', mimeType: video ? 'video/mp4' : 'image/png', licenseCode: 'creator-owned', versionNumber: 1, isLatestVersion: true, createdAt: '2026-09-27T00:00:00Z', usages: [], versions: [] })
async function fixture(page: Page, count = 4, video = false, batch = 50) {
  const requested: string[] = []
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url()), path = url.pathname
    if (path === '/api/v1/auth/session') return route.fulfill({ json: { user: { id: owner, email: 'experience@fixture.test', handle: 'experience', displayName: 'Experience', role: 'creator', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: ['assets:upload'] } } })
    if (path === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: {} } })
    if (path === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (path === '/api/v1/assets/saved-works') {
      requested.push(url.search)
      const saved = [{ ...item(1), postId: owner, workId: owner, assetId: owner, mediaKind: 'image', authorHandle: 'source', savedAt: '2026-09-27T00:00:00Z' }]
      return route.fulfill({ json: { items: saved.filter(i => i.title.toLowerCase().includes((url.searchParams.get('q') || '').toLowerCase())) } })
    }
    if (path === '/api/v1/assets') {
      requested.push(url.search)
      let items = Array.from({ length: count }, (_, i) => item(i, video))
      for (const [key, property] of [['q', 'title'], ['kind', 'kind'], ['source', 'sourceType'], ['scanStatus', 'scanStatus']] as const) {
        const value = url.searchParams.get(key)
        if (value) items = items.filter(i => key === 'q' ? i[property].toLowerCase().includes(value.toLowerCase()) : i[property] === value)
      }
      const start = Number(url.searchParams.get('cursor') || 0)
      return route.fulfill({ json: { total: items.length, items: items.slice(start, start + batch), nextCursor: start + batch < items.length ? String(start + batch) : undefined } })
    }
    return route.fulfill({ status: 500, json: { error: { code: 'unmocked', message: `Unmocked ${path}`, retryable: false } } })
  })
  await page.goto('/workspace/assets')
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
  return requested
}
test('search and source/type filters survive reload and browser back; no-match can clear', async ({ page }) => {
  const requests = await fixture(page, 41)
  const filters = page.locator('.asset-library-controls')
  const search = page.locator('#header-search').getByRole('searchbox', { name: 'Search titles', exact: true })
  await search.fill('Garden asset 3')
  await search.press('Enter')
  await expect(page).toHaveURL(/q=Garden/)
  await chooseOption(filters.locator('select').nth(0), 'image')
  await expect(page).toHaveURL(/kind=image/)
  await chooseOption(filters.locator('select').nth(1), 'upload')
  await expect(page).toHaveURL(/source=upload/)
  await chooseOption(filters.locator('select').nth(2), 'clean')
  await search.press('Enter')
  await expect(page.locator('.catalog-toolbar [role=status]')).toContainText('6 Assets')
  await expect(page.locator('.asset-row')).toHaveCount(6)
  expect(requests.some(q => q.includes('q=Garden+asset+3') && q.includes('kind=image') && q.includes('source=upload') && q.includes('scanStatus=clean'))).toBeTruthy()
  await page.reload()
  await expect(search).toHaveValue('Garden asset 3')
  await search.fill('Missing title')
  await search.press('Enter')
  await expect(page.getByText('No matching results', { exact: true })).toBeVisible()
  await page.goBack()
  await expect(page.locator('.asset-row')).toHaveCount(6)
  await filters.getByRole('button', { name: 'Clear filters', exact: true }).click()
  await expect(page.locator('.catalog-toolbar [role=status]')).toContainText('41 Assets')
})
test('saved work search is server-backed and tab panels have keyboard/ARIA relationships', async ({ page }) => {
  const requests = await fixture(page)
  const owned = page.getByRole('tab', { name: 'Mine', exact: true })
  await owned.focus(); await owned.press('ArrowRight')
  const saved = page.getByRole('tab', { name: 'Saved', exact: true })
  await expect(saved).toBeFocused(); await expect(saved).toHaveAttribute('aria-selected', 'true')
  await expect(saved).toHaveAttribute('aria-controls', 'workspace-assets-panel-saved')
  await expect(page.getByRole('tabpanel', { name: 'Saved' })).toBeVisible()
  const search = page.locator('#header-search').getByRole('searchbox', { name: 'Search titles', exact: true })
  await search.fill('Missing')
  await search.press('Enter')
  await expect(page.getByText('No matching results', { exact: true })).toBeVisible()
  expect(requests.some(q => q.includes('q=Missing'))).toBeTruthy()
  const axe = await new AxeBuilder({ page }).include('#main-content').withRules(['aria-valid-attr-value', 'aria-required-children', 'aria-required-parent', 'aria-allowed-role', 'aria-prohibited-attr', 'nested-interactive', 'button-name', 'label']).analyze()
  expect(axe.violations).toEqual([])
  await saved.focus(); await saved.press('Home'); await expect(owned).toHaveAttribute('aria-selected', 'true')
})
for (const mobile of [false, true]) test(`large catalog bounds rendered cards, scrolls and changes layout: mobile=${mobile}`, async ({ page }, testInfo) => {
  test.setTimeout(60_000)
  if (mobile) await page.setViewportSize({ width: 390, height: 844 })
  const capacity = mobile ? 400 : 2000
  await fixture(page, capacity)
  for (let loaded = 50; loaded < capacity; loaded += 50) {
    const response = page.waitForResponse(r => r.url().includes(`/assets?cursor=${loaded}`))
    await page.getByRole('button', { name: 'Load more', exact: true }).click()
    await response
    await expect(page.locator('.catalog-toolbar [role=status]')).toContainText(`${capacity} Assets`)
  }
  await expect(page.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)
  await page.evaluate(() => { const main = document.querySelector('#main-content')!; const el = getComputedStyle(main).overflowY === 'visible' ? document.scrollingElement! : main; el.scrollTop = 0 })
  await expect(page.locator('.asset-row').filter({ hasText: 'Garden asset 0' }).first()).toBeVisible()
  await expect.poll(() => page.locator('.asset-row').count()).toBeLessThan(40)
  const firstCard = page.locator('.asset-row').filter({ hasText: 'Garden asset 0' }).first()
  await firstCard.getByRole('link', { name: 'View details', exact: true }).focus()
  await page.keyboard.press('Tab')
  await expect(page.locator('.asset-row').filter({ hasText: 'Garden asset 1' }).first().getByRole('link', { name: 'Garden asset 1', exact: true }).first()).toBeFocused()
  await page.getByRole('button', { name: 'Grid view', exact: true }).click()
  await page.evaluate(() => { const main = document.querySelector('#main-content')!; const el = getComputedStyle(main).overflowY === 'visible' ? document.scrollingElement! : main; el.scrollTop = el.scrollHeight })
  await expect(page.locator('.asset-row').filter({ hasText: `Garden asset ${capacity - 1}` })).toBeVisible()
  expect(await page.locator('.asset-row').count()).toBeLessThan(60)
  const dimensions = await page.locator('#main-content').evaluate(el => ({ width: el.clientWidth, scrollWidth: el.scrollWidth, height: el.scrollHeight, nodes: el.querySelectorAll('*').length }))
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.width + 1)
  await testInfo.attach('catalog-capacity.json', { body: JSON.stringify({ loaded: capacity, rendered: await page.locator('.asset-row').count(), ...dimensions }), contentType: 'application/json' })
})
test('many video assets use static previews with no idle media request, including reduced motion', async ({ page }, testInfo) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  let mediaRequests = 0
  page.on('request', req => { if (req.url().endsWith('.mp4')) mediaRequests++ })
  await fixture(page, 40, true)
  await expect(page.locator('.asset-video-placeholder')).toHaveCount(40)
  await expect(page.locator('video')).toHaveCount(0)
  expect(mediaRequests).toBe(0)
  await testInfo.attach('video-budget.json', { body: JSON.stringify({ videos: 40, playing: 0, idleMediaRequests: mediaRequests, reducedMotion: true }), contentType: 'application/json' })
})


test('sticky asset toolbar retains its position during view loading and search scope is limited to the asset list', async ({ page }) => {
  await fixture(page, 40)
  await page.locator('#main-content').evaluate(el => { el.scrollTop = 450 })
  const toolbar = page.locator('.catalog-toolbar')
  await expect(toolbar).toHaveClass(/is-stuck/)
  const before = await toolbar.boundingBox()
  let release: () => void = () => {}
  const hold = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/v1/assets/saved-works*', async route => {
    await hold
    await route.fulfill({ json: { items: [] } })
  })
  await page.getByRole('tab', { name: 'Saved', exact: true }).click()
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'true')
  await expect(toolbar).toHaveClass(/is-stuck/)
  const during = await toolbar.boundingBox()
  expect(Math.abs(during!.y - before!.y)).toBeLessThan(1)
  expect(Math.abs(during!.height - before!.height)).toBeLessThan(1)
  await expect(page.locator('.catalog-view-count')).toHaveText('—')
  release()
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
  await expect(page.locator('.catalog-toolbar [role=status]')).toContainText('0')
  const search = page.locator('#header-search').getByRole('searchbox', { name: 'Search titles', exact: true })
  await search.fill('x'.repeat(121)); await search.press('Enter')
  await expect(search).toHaveAttribute('aria-invalid', 'true')
  await expect(page).not.toHaveURL(/q=/)
  await search.fill('Garden'); await search.press('Enter')
  await expect(page).toHaveURL(/view=saved&q=Garden/)
  await page.locator('#primary-navigation a[href="/workspace/purchases"]').click()
  await expect(page).toHaveURL(/\/workspace\/purchases$/)
  await expect(page.locator('#header-search .global-search-scope')).toContainText('Purchases')
})
