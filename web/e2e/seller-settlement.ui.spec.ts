import { expect, test, type Page, type Route } from '@playwright/test'
import type { components } from '../src/api/schema'

const orderId = '00000000-0000-4000-8000-000000009871'
const user = { id: 'seller-a', handle: 'seller-a', email: 'seller@example.test', displayName: 'Seller', role: 'creator', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: [] }
const sale: components['schemas']['SellerSaleDetail'] = {
  orderId, productId: orderId, title: 'Licensed production workflow', amountCents: 1900, currency: 'USD', status: 'refunded', paymentStatus: 'refunded', environment: 'live', hasContract: true, needsReview: true,
  createdAt: '2026-09-01T08:00:00Z', updatedAt: '2026-09-15T08:00:00Z', licenseName: 'Commercial license', licenseVersion: '1', licenseTerms: 'Retained accepted terms',
  settlement: { status: 'recovery_required', environment: 'live', grossAmountCents: 1900, feeBps: 250, feeCents: 48, netAmountCents: 1852, recoveryAmountCents: 1852, currency: 'USD', availableAt: '2026-09-08T08:00:00Z', transferredAt: '2026-09-08T08:01:00Z' },
}
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined = () => undefined) {
  const unexpected: string[] = []
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    const handled = handler(route, url)
    if (handled) return handled
    if (route.request().method() === 'GET') {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user } })
      if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
      if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
      if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (url.pathname === '/api/v1/seller/sales') return route.fulfill({ json: { items: [sale], total: 1 } })
      if (url.pathname === `/api/v1/seller/sales/${orderId}`) return route.fulfill({ json: sale })
      if (url.pathname.endsWith('/events')) return route.fulfill({ json: { items: [] } })
    }
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 500, json: { error: { code: 'unavailable', message: 'Unexpected API', retryable: false } } })
  })
  return unexpected
}

for (const width of [390, 1308]) {
  for (const locale of ['zh-CN', 'en-US']) {
    test(`seller sees frozen settlement and refund recovery at ${width}px in ${locale}`, async ({ page }) => {
      const chinese = locale === 'zh-CN'
      await page.setViewportSize({ width, height: 901 })
      await page.addInitScript(locale => localStorage.setItem('hcai-locale', locale), locale)
      const unexpected = await mockApp(page, (route, url) => url.pathname === '/api/v1/auth/session' ? route.fulfill({ json: { user: { ...user, locale } } }) : undefined)
      await page.goto('/workspace/sales')
      await expect(page.locator('.ui-content-card')).toContainText(chinese ? '结算待核对' : 'Settlement needs review')
      await page.getByRole('link').filter({ hasText: sale.title }).click()
      const settlement = page.getByRole('region', { name: chinese ? '商品结算' : 'Product settlement', exact: true })
      await expect(settlement).toBeVisible()
      await expect(settlement).toContainText('2.5%')
      await expect(settlement.locator('dd')).toHaveCount(7)
      await expect(settlement.locator('dd').nth(2)).toContainText('0.48')
      await expect(settlement.locator('dd').nth(3)).toContainText('18.52')
      await expect(settlement.locator('dd').nth(4)).toContainText('18.52')
      await expect(settlement).toContainText(chinese ? '通道转账确认时间' : 'Provider transfer confirmed')
      await expect(page.locator('.seller-sales-note')).toContainText(chinese ? '不代表银行账户已到账' : 'does not confirm bank arrival')
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await settlement.scrollIntoViewIfNeeded()
      await page.screenshot({ path: `/tmp/hcai-seller-settlement-${locale}-${width}.png`, fullPage: true })
      expect(unexpected).toEqual([])
    })
  }
}

test('missing settlement does not invent amounts and a denied refresh clears private data', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  let denied = false
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === `/api/v1/seller/sales/${orderId}`) return route.fulfill(denied
      ? { status: 403, json: { error: { code: 'forbidden', message: 'Access denied', retryable: false } } }
      : { json: { ...sale, settlement: undefined } })
  })
  await page.goto(`/workspace/sales/${orderId}`)
  await expect(page.getByText('No verifiable settlement record', { exact: true })).toBeVisible()
  await expect(page.locator('.seller-settlement dd')).toHaveCount(0)
  denied = true
  await page.getByRole('button', { name: 'Reload', exact: true }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.locator('.seller-sale-detail')).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('an event access denial clears the previously loaded sale and settlement', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === `/api/v1/seller/sales/${orderId}/events`) {
      return route.fulfill({ status: 403, json: { error: { code: 'forbidden', message: 'Access denied', retryable: false } } })
    }
  })
  await page.goto(`/workspace/sales/${orderId}`)
  await expect(page.getByRole('alert')).toContainText('You do not have access to this resource.')
  await expect(page.locator('.seller-sale-detail')).toHaveCount(0)
  await expect(page.getByText('US$18.52', { exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('a sales continuation access denial clears the previously loaded list', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/seller/sales' && url.searchParams.has('cursor')) {
      return route.fulfill({ status: 403, json: { error: { code: 'forbidden', message: 'Access denied', retryable: false } } })
    }
    if (url.pathname === '/api/v1/seller/sales') {
      return route.fulfill({ json: { items: [sale], total: 2, nextCursor: 'next-page' } })
    }
  })
  await page.goto('/workspace/sales')
  await expect(page.getByRole('link').filter({ hasText: sale.title })).toHaveCount(1)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('You do not have access to this resource.')
  await expect(page.locator('.ui-content-card')).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('a settlement response from the previous account cannot restore private details', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  let actor = user.id
  let pending: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: { ...user, id: actor } } })
    if (url.pathname === `/api/v1/seller/sales/${orderId}`) {
      if (actor === user.id) { pending = route; return Promise.resolve() }
      return route.fulfill({ status: 404, json: { error: { code: 'not_found', message: 'Not found', retryable: false } } })
    }
  })
  await page.goto(`/workspace/sales/${orderId}`)
  await expect.poll(() => Boolean(pending)).toBe(true)
  actor = 'seller-b'
  await page.evaluate(async () => {
    const app = document.querySelector('#app') as HTMLElement & { __vue_app__: { config: { globalProperties: { $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> } } } } }
    await app.__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true)
  })
  const response = page.waitForResponse(r => r.url() === pending!.request().url() && r.status() === 200)
  await pending!.fulfill({ json: sale })
  await (await response).finished()
  await page.waitForLoadState('networkidle')
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.locator('.seller-sale-detail')).toHaveCount(0)
  expect(unexpected).toEqual([])
})
