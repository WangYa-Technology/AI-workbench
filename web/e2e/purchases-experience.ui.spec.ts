import { expect, test, type Page } from '@playwright/test'
import { chooseOption } from './helpers/select'
const buyer = '00000000-0000-4000-8000-000000008100'
const order = '00000000-0000-4000-8000-000000008200'
const make = (n: number, kind = 'document', mimeType = 'text/plain', canReuse = true, status = 'available') => ({
  id: `00000000-0000-4000-8000-${String(8300 + n).padStart(12, '0')}`, title: `Purchase ${n}`, sourceType: 'purchase', kind, mimeType, mediaUrl: '/brand/logo.png', scanStatus: 'clean', licenseCode: 'hcai-commercial-standard-v1', versionNumber: 1, isLatestVersion: true, createdAt: '2026-09-27T00:00:00Z',
  purchaseAccess: { orderId: order, canDownload: ['available','refund_pending'].includes(status), canReuse, status },
})
async function shell(page: Page, locale = 'en-US', count = 5) {
  const items = count === 5 ? [make(0), make(1,'document','application/zip',false), make(2,'image','image/png',false,'unavailable'), make(3,'video','video/mp4',true,'refund_pending'), make(4,'audio','audio/mpeg')] : Array.from({ length: count }, (_, n) => make(n))
  let fileStatus = 206
  let revoked = false
  const requests: URL[] = []
  await page.addInitScript(value => localStorage.setItem('hcai-locale', value), locale)
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: { id: buyer, email: 'buyer@fixture.test', handle: 'buyer', displayName: 'Buyer', role: 'member', status: 'active', locale, timezone: 'UTC', permissions: [] } } })
    if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: {} } })
    if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (url.pathname === '/api/v1/assets') {
      requests.push(url)
      expect(url.searchParams.get('source')).toBe('purchase')
      const filtered = items.filter(i => !revoked && i.title.toLowerCase().includes((url.searchParams.get('q') || '').toLowerCase()) && (!url.searchParams.get('kind') || i.kind === url.searchParams.get('kind')))
      const start = Number(url.searchParams.get('cursor') || 0)
      return route.fulfill({ json: { items: filtered.slice(start,start+50), total: filtered.length, nextCursor: start+50 < filtered.length ? String(start+50) : undefined } })
    }
    const item = items.find(i => url.pathname === `/api/v1/assets/${i.id}`)
    if (item) return route.fulfill({ json: { ...item, purchaseAccess: revoked ? { ...item.purchaseAccess, status: 'revoked', canReuse: false, canDownload: false } : item.purchaseAccess, usages: [], versions: [], provenance: { purchase: { ...item.purchaseAccess, canDownload: !revoked && item.purchaseAccess.canDownload, canReuse: !revoked && item.purchaseAccess.canReuse, orderId: order, productId: order, productTitle: 'Frozen title', sellerId: buyer, sellerName: 'Seller', sellerHandle: 'seller', licenseCode: item.licenseCode, licenseName: 'Frozen terms', orderStatus: revoked ? 'refunded' : 'fulfilled', grantedAt: item.createdAt, paymentMode: 'stripe', realCharge: false } } } })
    if (url.pathname.endsWith('/content')) return route.fulfill({ status: fileStatus, contentType: 'text/plain', body: 'x' })
    return route.fulfill({ status: 500, json: { error: { code: 'unmocked', message: url.pathname } } })
  })
  return { items, requests, deny: (status: number) => { fileStatus = status }, revoke: () => { revoked = true } }
}
for (const locale of ['en-US','zh-CN']) for (const width of [390,1308]) test(`purchase capabilities and media labels ${locale} ${width}px`, async ({ page }, info) => {
  await page.setViewportSize({ width, height: 900 })
  await shell(page, locale)
  await page.goto('/workspace/purchases')
  const cards = page.locator('.asset-row')
  await expect(cards).toHaveCount(5)
  await expect(page.locator('main [role=tablist], main .page-hero-stats, main .asset-search-filters, main .workspace-switcher')).toHaveCount(0)
  await expect(page.locator('#header-search .global-search-scope')).toContainText(locale === 'en-US' ? 'Purchases' : '已购内容')
  await expect(cards.nth(0)).toContainText(locale === 'en-US' ? 'Document' : '文档')
  await expect(cards.nth(1)).toContainText(locale === 'en-US' ? 'Resource package' : '资源包')
  await expect(cards.nth(0).getByRole('link', { name: locale === 'en-US' ? 'Use in Create' : '用于创作', exact: true })).toBeVisible()
  await expect(cards.nth(1).getByRole('link', { name: locale === 'en-US' ? 'Use in Create' : '用于创作', exact: true })).toHaveCount(0)
  await expect(cards.nth(2).locator('img, video, audio')).toHaveCount(0)
  await expect(cards.nth(2)).toContainText(locale === 'en-US' ? 'Download unavailable' : '暂不可下载')
  await expect(cards.nth(3)).toContainText(locale === 'en-US' ? 'Refund in progress' : '退款处理中')
  await page.getByRole('button', { name: locale === 'en-US' ? 'Grid view' : '网格视图', exact: true }).click()
  const dimensions = await page.locator('#main-content').evaluate(el => ({ width: el.clientWidth, scrollWidth: el.scrollWidth }))
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.width+1)
  await info.attach('purchase-layout.png', { body: await page.screenshot(), contentType: 'image/png' })
})
test('purchase filters survive reload and back, always retain purchase source, clear empty state', async ({ page }) => {
  const fixture = await shell(page)
  await page.goto('/workspace/purchases?source=upload')
  const filters = page.locator('.asset-library-controls')
  const search = page.locator('#header-search').getByRole('searchbox', { name: 'Search titles', exact: true })
  await search.fill('Purchase 0')
  await search.press('Enter')
  await expect(page).toHaveURL(/q=Purchase/)
  await chooseOption(filters.locator('select').nth(0),'document')
  await search.press('Enter')
  await expect(page.locator('.asset-row')).toHaveCount(1)
  await page.reload()
  await expect(search).toHaveValue('Purchase 0')
  await search.fill('missing')
  await search.press('Enter')
  await expect(page.getByText('No matching results',{exact:true})).toBeVisible()
  await page.goBack()
  await expect(page.locator('.asset-row')).toHaveCount(1)
  await filters.getByRole('button',{name:'Clear filters',exact:true}).click()
  await expect(page.locator('.asset-row')).toHaveCount(5)
  expect(fixture.requests.every(url => url.searchParams.get('source') === 'purchase')).toBe(true)
})
for (const status of [403,404,500]) test(`download check ${status} offers recovery without a native download`, async ({ page }) => {
  const fixture = await shell(page)
  await page.goto(`/workspace/assets/${fixture.items[0]!.id}`)
  let downloads = 0
  page.on('download', () => downloads++)
  fixture.deny(status)
  await page.getByRole('link',{name:'Download licensed file',exact:true}).click()
  await expect(page.locator('.purchase-access-feedback [role="alert"]')).toBeVisible()
  await expect(page.getByRole('button',{name:'Refresh access',exact:true})).toBeVisible()
  await expect(page.getByRole('link',{name:'Contact support',exact:true})).toBeVisible()
  expect(downloads).toBe(0)
  fixture.revoke()
  await page.getByRole('button',{name:'Refresh access',exact:true}).click()
  await expect(page.getByRole('link',{name:'Download licensed file',exact:true})).toHaveCount(0)
  await expect(page.locator('.purchase-access-feedback')).toContainText('no longer has active access')
  await page.goto('/workspace/purchases')
  await expect(page.locator('.asset-row')).toHaveCount(0)
  await expect(page.getByText('No licensed purchases',{exact:true})).toBeVisible()
})

test('leaving during a download check cancels it and cannot start a late download', async ({ page }) => {
  const fixture = await shell(page)
  await page.goto('/workspace/purchases')
  await page.locator('.asset-row').first().getByRole('link',{name:'View details',exact:true}).click()
  let release!: () => void
  let settled = false
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route(`**/api/v1/assets/${fixture.items[0]!.id}/content`, async route => {
    await gate
    await route.fulfill({status:206,body:'x'}).catch(() => {})
    settled = true
  })
  let downloads = 0
  page.on('download', () => downloads++)
  const request = page.waitForRequest(r => r.url().endsWith('/content'))
  await page.getByRole('link',{name:'Download licensed file',exact:true}).click()
  await request
  await page.locator('.asset-back').click()
  await expect(page).toHaveURL(/\/workspace\/purchases$/)
  release()
  await expect.poll(() => settled).toBe(true)
  await expect(page.locator('.asset-row')).toHaveCount(5)
  expect(downloads).toBe(0)
  await expect(page.getByText('Checking access and file availability…',{exact:true})).toHaveCount(0)
})

test('download check times out and exposes recovery instead of staying busy', async ({ page }) => {
  const fixture = await shell(page)
  await page.goto(`/workspace/assets/${fixture.items[0]!.id}`)
  await expect(page.getByRole('link',{name:'Download licensed file',exact:true})).toBeVisible()
  await page.clock.install()
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route(`**/api/v1/assets/${fixture.items[0]!.id}/content`, async route => {
    await gate
    await route.fulfill({status:206,body:'x'}).catch(() => {})
  })
  const request = page.waitForRequest(r => r.url().endsWith('/content'))
  await page.getByRole('link',{name:'Download licensed file',exact:true}).click()
  await request
  await expect(page.getByRole('link',{name:'Download licensed file',exact:true})).toHaveAttribute('aria-disabled','true')
  await page.clock.fastForward(120_001)
  await expect(page.locator('.purchase-access-feedback [role="alert"]')).toContainText('failed or timed out')
  await expect(page.getByRole('link',{name:'Download licensed file',exact:true})).not.toHaveAttribute('aria-disabled','true')
  release()
})


test('purchase toolbar stays anchored during search loading and permits clearing the keyword', async ({ page }) => {
  await shell(page, 'en-US', 30)
  await page.goto('/workspace/purchases')
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
  await page.locator('#main-content').evaluate(el => { el.scrollTop = 480 })
  const toolbar = page.locator('.catalog-toolbar')
  await expect(toolbar).toHaveClass(/is-stuck/)
  const before = await toolbar.boundingBox()
  let release: () => void = () => {}
  const held = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/v1/assets?*', async route => {
    if (new URL(route.request().url()).searchParams.get('q')) await held
    await route.fallback()
  })
  const search = page.locator('#header-search').getByRole('searchbox', { name: 'Search titles', exact: true })
  await search.fill('Purchase 2'); await search.press('Enter')
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'true')
  await expect(toolbar).toHaveClass(/is-stuck/)
  const during = await toolbar.boundingBox()
  expect(Math.abs(during!.y - before!.y)).toBeLessThan(1)
  release()
  await expect(page.locator('.asset-row')).toHaveCount(11)
  await page.getByRole('button', { name: 'Clear search keyword', exact: true }).click()
  await expect(page).not.toHaveURL(/q=/)
  await expect(page.locator('.asset-row')).toHaveCount(30)
  await expect(search).toHaveValue('')
})
