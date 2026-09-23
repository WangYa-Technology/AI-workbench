import { expect, test, type Page, type Route } from '@playwright/test'
import { chooseOption } from './helpers/select'
import type { components } from '../src/api/schema'

const settlementId = '00000000-0000-4000-8000-000000009881'
const requestId = '00000000-0000-4000-8000-000000009882'
const user = { id: 'seller-a', handle: 'seller-a', email: 'seller@example.test', displayName: 'Seller', role: 'creator', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: [] }
const option: components['schemas']['SellerPayoutOption'] = { settlementId, orderId: settlementId, title: 'Production workflow', amountCents: 1852, currency: 'USD', environment: 'test', availableAt: '2026-09-10T08:00:00Z' }
const item: components['schemas']['SellerPayoutItem'] = { id: requestId, sellerId: user.id, amountCents: 1852, currency: 'USD', environment: 'test', status: 'under_review', canCancel: true, canSelectBank: true, idempotencyKey: 'original-key', createdAt: '2026-09-20T08:00:00Z', updatedAt: '2026-09-20T08:00:00Z' }
const bankTarget: components['schemas']['SellerPayoutBankTarget'] = { payoutRequestId: requestId, destinationId: 'acct_original', bankDestinationId: 'ba_bank000002', bankName: 'Second Bank', last4: '9876', currency: 'USD', observedAt: '2026-09-20T09:00:00Z', createdAt: '2026-09-20T09:00:00Z' }
const directory = { items: [
  { bankDestinationId: 'ba_bank000001', bankName: 'First Bank', last4: '1234', currency: 'USD' },
  { bankDestinationId: 'ba_bank000002', bankName: 'Second Bank', last4: '9876', currency: 'USD' },
], observedAt: '2026-09-20T09:00:00Z' }

for (const locale of ['en-US', 'zh-CN']) {
  test(`notification opens an old request directly with current seller decision in ${locale}`, async ({ page }) => {
    const zh = locale === 'zh-CN'
    await page.setViewportSize({ width: zh ? 390 : 1308, height: 901 })
    const message = 'Please update your payout information before submitting another request.'
    const current = { ...item, status: 'cancelled', canCancel: false, canSelectBank: false, latestReview: { revision: 2, decision: 'rejected', sellerMessage: message, createdAt: '2026-09-22T08:00:00Z' } }
    let listReads = 0
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { unreadCount: 1, items: [{ id: settlementId, kind: 'marketplace.payout_reviewed', title: 'Payout review updated', body: 'A review decision is available. Open the request for its current status and explanation. Approval does not send a bank payout.', targetPath: `/workspace/payouts/${requestId}`, createdAt: '2026-09-22T08:00:00Z' }] } })
      if (url.pathname.includes('/notifications/') || url.pathname === '/api/v1/notification-preferences' || url.pathname === '/api/v1/notification-deliveries') return route.fulfill({ json: { items: [] } })
      if (url.pathname.endsWith('/payout-options') || url.pathname.endsWith('/payout-requests') || url.pathname.endsWith('/seller/funds')) { listReads++; return route.fulfill({ json: { items: [] } }) }
      if (url.pathname.endsWith(`/payout-requests/${requestId}`)) return route.fulfill({ json: current })
    }, locale)
    await page.goto('/notifications')
    await page.getByRole('link').filter({ has: page.getByRole('heading', { name: zh ? '提现复核更新' : 'Payout review updated', exact: true }) }).click()
    await expect(page).toHaveURL(new RegExp(`/workspace/payouts/${requestId}$`))
    const decision = page.getByRole('region', { name: zh ? '复核决定' : 'Review decision', exact: true })
    await expect(decision).toContainText(message)
    await expect(decision).toContainText(zh ? '已驳回' : 'Rejected')
    await expect(decision).toContainText(zh ? '申请当前状态：已取消' : 'Current request status: Cancelled')
    await expect(page.getByRole('button', { name: zh ? '提交申请' : 'Submit request', exact: true })).toHaveCount(0)
    expect(listReads).toBe(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/hcai-seller-payout-decision-${locale}.png`, animations: 'disabled' })
    expect(unexpected).toEqual([])
  })
}

test('seller detail shows historical approval separately from current cancellation and hides missing explanation', async ({ page }) => {
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith(`/payout-requests/${requestId}`)) return route.fulfill({ json: { ...item, status: 'cancelled', canCancel: false, canSelectBank: false, latestReview: { revision: 1, decision: 'approved', createdAt: '2026-09-22T08:00:00Z' } } })
  })
  await page.goto(`/workspace/payouts/${requestId}`)
  const decision = page.getByRole('region', { name: 'Review decision', exact: true })
  await expect(decision).toContainText('Approved')
  await expect(decision).toContainText('Current request status: Cancelled')
  await expect(decision).toContainText('No seller-facing explanation was recorded')
  await expect(page.getByRole('region', { name: 'Bank result', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

for (const locale of ['en-US', 'zh-CN']) {
  for (const kind of ['paid', 'failed', 'returned']) {
    test(`bank ${kind} notification opens current returned result in ${locale}`, async ({ page }) => {
      const zh = locale === 'zh-CN'
      await page.setViewportSize({ width: zh ? 390 : 1308, height: 901 })
      const unexpected = await mockApp(page, (route, url) => {
        if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { unreadCount: 1, items: [{ id: settlementId, kind: `marketplace.payout_${kind}`, title: 'Historic bank event', body: 'Open the current request.', targetPath: `/workspace/payouts/${requestId}`, createdAt: '2026-09-22T08:00:00Z' }] } })
        if (url.pathname.includes('/notifications/') || url.pathname === '/api/v1/notification-preferences' || url.pathname === '/api/v1/notification-deliveries') return route.fulfill({ json: { items: [] } })
        if (url.pathname.endsWith(`/payout-requests/${requestId}`)) return route.fulfill({ json: { ...item, status: 'reconciliation_required', canCancel: false, canSelectBank: false, bankTarget, bankPayout: { status: 'failed', requiresReview: true, observedAt: '2026-09-23T00:00:00Z', checkedAt: '2026-09-23T00:05:00Z' } } })
      }, locale)
      await page.goto('/notifications')
      await page.locator(`a[href="/workspace/payouts/${requestId}"]`).filter({ has: page.getByRole('heading') }).click()
      const result = page.getByRole('region', { name: zh ? '银行结果' : 'Bank result', exact: true })
      await expect(result).toContainText(zh ? '银行失败或退回' : 'Bank failed or returned')
      await expect(result.getByRole('status')).toContainText(zh ? '本次出款需要核对' : 'This payout needs review')
      await expect(result).toContainText(zh ? '最近核对' : 'Last checked')
      await expect(page.getByText(zh ? '银行出款尚未开放' : 'Bank payouts are not open yet', { exact: false })).toHaveCount(0)
      await expect(page.getByRole('button', { name: zh ? '取消申请' : 'Cancel request', exact: true })).toHaveCount(0)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (kind === 'returned') await page.screenshot({ path: `/tmp/hcai-seller-bank-result-${locale}.png`, animations: 'disabled' })
      expect(unexpected).toEqual([])
    })
  }
}

for (const denial of [401, 403, 404]) {
  test(`seller bank history refresh follows current evidence and clears on ${denial}`, async ({ page }) => {
    let reads = 0
    const states = ['unconfirmed', 'pending', 'in_transit', 'paid', 'failed', 'canceled'] as const
    const labels = ['Bank arrival unconfirmed', 'Bank processing pending', 'In transit to bank', 'Bank payment confirmed', 'Bank failed or returned', 'Bank payout canceled']
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/payout-requests')) {
        const status = states[reads++]
        if (!status) return route.fulfill({ status: denial, json: { error: { code: 'seller_payout_not_found', message: 'Not available', retryable: false } } })
        return route.fulfill({ json: { items: [{ ...item, canCancel: false, canSelectBank: false, bankPayout: { status, requiresReview: ['failed', 'canceled'].includes(status) } }] } })
      }
    })
    await page.goto('/workspace/payouts')
    const result = page.getByRole('region', { name: 'Bank result', exact: true })
    for (const label of labels) {
      await expect(result).toContainText(label)
      if (label === labels[0]) await expect(result).toContainText('Source funding and queued jobs are separate from bank payment.')
      await page.getByRole('button', { name: 'Reload', exact: true }).click()
    }
    await expect(result).toHaveCount(0)
    expect(unexpected).toEqual([])
  })
}

test('a direct payout request denial clears seller decision content', async ({ page }) => {
  let reads = 0
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith(`/payout-requests/${requestId}`)) {
      if (++reads === 1) return route.fulfill({ json: { ...item, latestReview: { revision: 1, decision: 'approved', sellerMessage: 'Private seller explanation only.', createdAt: '2026-09-22T08:00:00Z' } } })
      return route.fulfill({ status: 404, json: { error: { code: 'seller_payout_not_found', message: 'Not found', retryable: false } } })
    }
  })
  await page.goto(`/workspace/payouts/${requestId}`)
  await expect(page.getByText('Private seller explanation only.', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Reload', exact: true }).click()
  await expect(page.getByText('Private seller explanation only.', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Cancel request', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined = () => undefined, locale = 'en-US') {
  const unexpected: string[] = []
  await page.addInitScript(locale => localStorage.setItem('hcai-locale', locale), locale)
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    const handled = handler(route, url)
    if (handled) return handled
    if (route.request().method() === 'GET') {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: { ...user, locale } } })
      if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
      if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
      if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (url.pathname === '/api/v1/seller/funds') return route.fulfill({ json: { sellerId: user.id, accounts: [], unresolvedRecords: 0, asOf: '2026-09-22T08:00:00Z' } })
      if (url.pathname === '/api/v1/seller/payout-options') return route.fulfill({ json: { items: [option], availability: 'available' } })
      if (url.pathname === '/api/v1/seller/payout-requests') return route.fulfill({ json: { items: [] } })
      if (url.pathname.endsWith('/banks')) return route.fulfill({ json: directory })
    }
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 500, json: { error: { code: 'unavailable', message: 'Unexpected API', retryable: false } } })
  })
  return unexpected
}

for (const width of [390, 1308]) {
  test(`explicit settlement and bank selection, immutable display and cancellation at ${width}px`, async ({ page }) => {
    const zh = width === 390
    await page.setViewportSize({ width, height: 901 })
    let current: components['schemas']['SellerPayoutItem'] | undefined
    const requests: unknown[] = []
    const unexpected = await mockApp(page, (route, url) => {
      const method = route.request().method()
      if (url.pathname.endsWith('/payout-options')) return route.fulfill({ json: { items: current && current.status !== 'cancelled' ? [] : [option], availability: 'available' } })
      if (url.pathname.endsWith('/payout-requests')) {
        if (method === 'GET') return route.fulfill({ json: { items: current ? [current] : [] } })
        requests.push(route.request().postDataJSON())
        expect(route.request().headers()['idempotency-key']).toBeTruthy()
        current = { ...item }
        return route.fulfill({ status: 201, json: current })
      }
      if (url.pathname.endsWith('/bank-destination') && method === 'PUT') {
        requests.push(route.request().postDataJSON())
        current = { ...current!, bankTarget, canSelectBank: false }
        return route.fulfill({ json: bankTarget })
      }
      if (url.pathname.endsWith(`/${requestId}`) && method === 'DELETE') {
        current = { ...current!, status: 'cancelled', canCancel: false, canSelectBank: false }
        return route.fulfill({ json: current })
      }
    }, zh ? 'zh-CN' : 'en-US')
    await page.goto('/workspace/payouts')
    await expect(page.getByRole('heading', { name: zh ? '提现申请' : 'Payout requests', exact: true })).toBeVisible()
    const headingBox = await page.getByRole('heading', { name: zh ? '提现申请' : 'Payout requests', exact: true }).boundingBox()
    const headerBox = await page.locator('.site-header').boundingBox()
    expect(headingBox!.y).toBeGreaterThanOrEqual(headerBox!.y + headerBox!.height)
    await page.screenshot({ path: `/tmp/hcai-seller-payouts-initial-${width}.png`, animations: 'disabled' })
    const submit = page.getByRole('button', { name: zh ? '提交申请' : 'Submit request', exact: true })
    await expect(submit).toBeDisabled()
    await chooseOption(page.getByRole('combobox', { name: zh ? '选择结算' : 'Settlement', exact: true }), settlementId)
    await submit.click()
    await expect(page.getByText(zh ? '申请已记录，等待审核。可在申请记录中选择收款银行。' : 'Request recorded for review. Choose a bank in the request history.', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: zh ? '选择银行' : 'Choose bank', exact: true }).click()
    const save = page.getByRole('button', { name: zh ? '确认并保存银行' : 'Confirm and save bank', exact: true })
    await expect(save).toBeDisabled()
    await chooseOption(page.getByRole('combobox', { name: zh ? '收款银行' : 'Receiving bank', exact: true }), 'ba_bank000002')
    await save.click()
    await expect(page.getByText(zh ? '银行已绑定' : 'Bank bound', { exact: true })).toBeVisible()
    await expect(page.locator('.payout-bank-summary')).toContainText('Second Bank · •••• 9876')
    await expect(page.getByRole('button', { name: zh ? '选择银行' : 'Choose bank', exact: true })).toHaveCount(0)
    expect(requests).toEqual([{ settlementId, amountCents: 1852 }, { bankDestinationId: 'ba_bank000002' }])
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.evaluate(() => {
      (document.activeElement as HTMLElement)?.blur()
      window.scrollTo({ top: 0, behavior: 'instant' })
      if (innerWidth > 767) document.querySelector('#main-content')?.scrollTo({ top: 0, behavior: 'instant' })
    })
    await expect(page.getByRole('heading', { name: zh ? '提现申请' : 'Payout requests', exact: true })).toBeInViewport()
    await expect.poll(() => page.locator('.skip-link').evaluate(el => el.getBoundingClientRect().bottom)).toBeLessThanOrEqual(0)
    await page.screenshot({ path: `/tmp/hcai-seller-payouts-${width}.png`, animations: 'disabled' })
    await page.getByRole('button', { name: zh ? '取消申请' : 'Cancel request', exact: true }).click()
    await page.getByRole('button', { name: zh ? '确认取消申请' : 'Confirm cancellation', exact: true }).click()
    await expect(page.locator('.seller-payout-row')).toContainText(zh ? '已取消' : 'Cancelled')
    await expect(page.locator('.seller-payout-row')).toContainText(zh ? '银行已绑定' : 'Bank bound')
    await expect(page.locator('.payout-bank-summary')).toContainText('Second Bank · •••• 9876')
    await expect(page.getByRole('combobox', { name: zh ? '选择结算' : 'Settlement', exact: true })).toBeVisible()
    expect(unexpected).toEqual([])
  })
}

test('lost creation response retries the same key and exact selected settlement', async ({ page }) => {
  const keys: string[] = []
  const bodies: unknown[] = []
  let current: components['schemas']['SellerPayoutItem'] | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/payout-requests') && route.request().method() === 'POST') {
      keys.push(route.request().headers()['idempotency-key']); bodies.push(route.request().postDataJSON()); current = { ...item }
      if (keys.length === 1) return route.abort('failed')
      return route.fulfill({ status: 201, json: current })
    }
    if (url.pathname.endsWith('/payout-requests')) return route.fulfill({ json: { items: current ? [current] : [] } })
  })
  await page.goto('/workspace/payouts')
  await chooseOption(page.getByRole('combobox', { name: 'Settlement', exact: true }), settlementId)
  await page.getByRole('button', { name: 'Submit request', exact: true }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Settlement', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Retry original request', exact: true }).click()
  await expect(page.locator('.seller-payout-row')).toHaveCount(1)
  expect(keys).toHaveLength(2); expect(keys[0]).toBeTruthy(); expect(keys[1]).toBe(keys[0])
  expect(bodies).toEqual([{ settlementId, amountCents: 1852 }, { settlementId, amountCents: 1852 }])
  expect(unexpected).toEqual([])
})

test('bank read failure has a retry and never selects or binds a bank automatically', async ({ page }) => {
  let reads = 0
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/payout-requests')) return route.fulfill({ json: { items: [item] } })
    if (url.pathname.endsWith('/banks')) {
      reads++
      return route.fulfill(reads === 1 ? { status: 503, json: { error: { code: 'seller_payout_bank_unavailable', message: 'Unavailable', retryable: true } } } : { json: directory })
    }
  })
  await page.goto('/workspace/payouts')
  await page.getByRole('button', { name: 'Choose bank', exact: true }).click()
  await expect(page.locator('.seller-payout-row').getByRole('alert')).toBeVisible()
  await page.locator('.seller-payout-row').getByRole('button', { name: 'Reload', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Confirm and save bank', exact: true })).toBeDisabled()
  expect(reads).toBe(2); expect(unexpected).toEqual([])
})

test('history pagination is private and an access denial clears all actionable data', async ({ page }) => {
  let next = false
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/payout-requests')) {
      if (url.searchParams.has('cursor')) {
        next = true
        return route.fulfill({ status: 403, json: { error: { code: 'forbidden', message: 'Denied', retryable: false } } })
      }
      return route.fulfill({ json: { items: [item], nextCursor: requestId } })
    }
  })
  await page.goto('/workspace/payouts')
  await expect(page.locator('.seller-payout-row')).toHaveCount(1)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.locator('.seller-payout-row')).toHaveCount(0)
  await expect(page.getByRole('combobox', { name: 'Settlement', exact: true })).toHaveCount(0)
  expect(next).toBe(true); expect(unexpected).toEqual([])
})

test('same-account session replacement suppresses an old bank response', async ({ page }) => {
  let pending: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/payout-requests')) return route.fulfill({ json: { items: [item] } })
    if (url.pathname.endsWith('/banks')) { pending = route; return Promise.resolve() }
  })
  await page.goto('/workspace/payouts')
  await page.getByRole('button', { name: 'Choose bank', exact: true }).click()
  await expect.poll(() => Boolean(pending)).toBe(true)
  await page.evaluate(async () => {
    const app = document.querySelector('#app') as HTMLElement & { __vue_app__: { config: { globalProperties: { $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> } } } } }
    await app.__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true)
  })
  const response = page.waitForResponse(r => r.url() === pending!.request().url())
  await pending!.fulfill({ json: directory }); await (await response).finished()
  await expect(page.getByRole('combobox', { name: 'Receiving bank', exact: true })).toHaveCount(0)
  await expect(page.getByText('First Bank', { exact: false })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('acknowledged creation survives a directory refresh failure without offering another creation', async ({ page }) => {
  let created = false
  let fail = true
  let posts = 0
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/payout-requests') && route.request().method() === 'POST') {
      posts++; created = true
      return route.fulfill({ status: 201, json: item })
    }
    if (url.pathname.endsWith('/payout-options') && created) return route.fulfill(fail
      ? { status: 503, json: { error: { code: 'payment_provider_unavailable', message: 'Unavailable', retryable: true } } }
      : { json: { items: [], availability: 'available' } })
    if (url.pathname.endsWith('/payout-requests')) return route.fulfill({ json: { items: created ? [item] : [] } })
  })
  await page.goto('/workspace/payouts')
  await expect(page.getByRole('region', { name: 'Settlement funds', exact: true })).toBeVisible()
  await chooseOption(page.getByRole('combobox', { name: 'Settlement', exact: true }), settlementId)
  await page.getByRole('button', { name: 'Submit request', exact: true }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.getByText('Request recorded for review. Choose a bank in the request history.', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Retry original request', exact: true })).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Settlement funds', exact: true })).toHaveCount(0)
  fail = false
  await page.getByRole('alert').getByRole('button', { name: 'Reload', exact: true }).click()
  await expect(page.locator('.seller-payout-row')).toHaveCount(1)
  expect(posts).toBe(1); expect(unexpected).toEqual([])
})

test('unknown bank save retains the original selection for retry', async ({ page }) => {
  const bodies: unknown[] = []
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/payout-requests')) return route.fulfill({ json: { items: [item] } })
    if (url.pathname.endsWith('/bank-destination')) {
      bodies.push(route.request().postDataJSON())
      if (bodies.length === 1) return route.abort('failed')
      return route.fulfill({ json: bankTarget })
    }
  })
  await page.goto('/workspace/payouts')
  await page.getByRole('button', { name: 'Choose bank', exact: true }).click()
  await chooseOption(page.getByRole('combobox', { name: 'Receiving bank', exact: true }), 'ba_bank000002')
  await page.getByRole('button', { name: 'Confirm and save bank', exact: true }).click()
  await expect(page.locator('.seller-payout-row').getByRole('alert')).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Receiving bank', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Retry original bank selection', exact: true }).click()
  await expect(page.getByText('Bank bound', { exact: true })).toBeVisible()
  expect(bodies).toEqual([{ bankDestinationId: 'ba_bank000002' }, { bankDestinationId: 'ba_bank000002' }])
  expect(unexpected).toEqual([])
})

test('settlement continuation preserves distinct equal amount choices and submits the chosen second order', async ({ page }) => {
  const other = { ...option, settlementId: requestId, orderId: requestId }
  let input: unknown
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/payout-options')) return route.fulfill({ json: url.searchParams.has('cursor')
      ? { items: [other], availability: 'available' }
      : { items: [option], nextCursor: settlementId, availability: 'available' } })
    if (url.pathname.endsWith('/payout-requests') && route.request().method() === 'POST') {
      input = route.request().postDataJSON()
      return route.fulfill({ status: 201, json: item })
    }
  })
  await page.goto('/workspace/payouts')
  await page.getByRole('button', { name: 'Load more settlements', exact: true }).click()
  await chooseOption(page.getByRole('combobox', { name: 'Settlement', exact: true }), requestId)
  await page.getByRole('button', { name: 'Submit request', exact: true }).click()
  await expect(page.getByText('Request recorded for review. Choose a bank in the request history.', { exact: true })).toBeVisible()
  expect(input).toEqual({ settlementId: requestId, amountCents: 1852 }); expect(unexpected).toEqual([])
})

test('recovering a cancelled request never labels it as awaiting review', async ({ page }) => {
  let calls = 0
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/payout-requests') && route.request().method() === 'POST') {
      if (++calls === 1) return route.abort('failed')
      return route.fulfill({ status: 201, json: { ...item, status: 'cancelled' } })
    }
  })
  await page.goto('/workspace/payouts')
  await chooseOption(page.getByRole('combobox', { name: 'Settlement', exact: true }), settlementId)
  await page.getByRole('button', { name: 'Submit request', exact: true }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await page.getByRole('button', { name: 'Retry original request', exact: true }).click()
  await expect(page.getByText('Original request found: Cancelled. No new request was created.', { exact: true })).toBeVisible()
  await expect(page.getByText('Request recorded for review. Choose a bank in the request history.', { exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

for (const legacy of [false, true]) {
  test(`direct seller request displays ${legacy ? 'legacy missing' : 'frozen'} bank summary without directory refresh`, async ({ page }) => {
    const target = { ...bankTarget, ...(legacy ? { bankName: undefined, last4: undefined } : {}) }
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith(`/payout-requests/${requestId}`)) return route.fulfill({ json: { ...item, status: 'cancelled', canCancel: false, canSelectBank: false, bankTarget: target } })
    })
    await page.goto(`/workspace/payouts/${requestId}`)
    const summary = page.locator('.payout-bank-summary')
    await expect(summary).toContainText(legacy ? 'Bank name and last four digits were not recorded' : 'Second Bank · •••• 9876')
    await expect(summary).toContainText(bankTarget.bankDestinationId)
    expect(unexpected).toEqual([])
  })
}

for (const width of [390, 1308]) {
  test(`financial scopes stay separate and clear on access denial at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 901 })
    let denied = false
    const account = { accountId: 'a'.repeat(64), provider: 'stripe', environment: 'live', currency: 'USD', pendingCents: 500, availableCents: 1852, reservedCents: 200, recoveryDueCents: 0, withdrawableCents: 1652 }
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/seller/funds')) return route.fulfill(denied
        ? { status: 403, json: { error: { code: 'forbidden', message: 'Denied', retryable: false } } }
        : { json: { sellerId: user.id, accounts: [account, { ...account, accountId: 'b'.repeat(64), environment: 'test', availableCents: 9900, recoveryDueCents: 123, withdrawableCents: 0 }], unresolvedRecords: 0, asOf: '2026-09-22T08:00:00Z' } })
    })
    await page.goto('/workspace/payouts')
    const funds = page.getByRole('region', { name: 'Settlement funds', exact: true })
    const cards = funds.locator('.seller-funds-account')
    await expect(cards).toHaveCount(2)
    await expect(cards.nth(0)).toContainText('aaaaaaaaaaaa')
    await expect(cards.nth(0)).toContainText('18.52')
    await expect(cards.nth(0)).not.toContainText('99.00')
    await expect(cards.nth(1)).toContainText('bbbbbbbbbbbb')
    await expect(cards.nth(1)).toContainText('99.00')
    await expect(cards.nth(1)).toContainText('1.23')
    await expect(funds).not.toContainText('117.52')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/hcai-seller-funds-scopes-${width}.png`, animations: 'disabled' })
    denied = true
    await page.getByRole('button', { name: 'Reload', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(funds).toHaveCount(0)
    await expect(page.getByRole('combobox', { name: 'Settlement', exact: true })).toHaveCount(0)
    expect(unexpected).toEqual([])
  })
}

test('unclassified funds explain the block without contributing to balances', async ({ page }) => {
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/seller/funds')) return route.fulfill({ json: { sellerId: user.id, accounts: [], unresolvedRecords: 3, asOf: '2026-09-22T08:00:00Z' } })
    if (url.pathname.endsWith('/payout-options')) return route.fulfill({ json: { items: [], availability: 'available' } })
  })
  await page.goto('/workspace/payouts')
  const funds = page.getByRole('region', { name: 'Settlement funds', exact: true })
  await expect(funds).toContainText('3 financial records need reconciliation')
  await expect(funds.locator('.seller-funds-account')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Submit request', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('an obsolete funds response cannot repopulate a replaced session', async ({ page }) => {
  let pending: Route | undefined
  let reads = 0
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/seller/funds') && ++reads === 1) { pending = route; return Promise.resolve() }
  })
  await page.goto('/workspace/payouts')
  await expect.poll(() => Boolean(pending)).toBe(true)
  await page.evaluate(async () => {
    const app = document.querySelector('#app') as HTMLElement & { __vue_app__: { config: { globalProperties: { $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> } } } } }
    await app.__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true)
  })
  await expect(page.getByRole('region', { name: 'Settlement funds', exact: true })).toBeVisible()
  const response = page.waitForResponse(r => r.url() === pending!.request().url())
  await pending!.fulfill({ json: { sellerId: user.id, accounts: [{ accountId: 'c'.repeat(64), provider: 'stripe', environment: 'test', currency: 'USD', pendingCents: 0, availableCents: 998899, reservedCents: 0, recoveryDueCents: 0, withdrawableCents: 998899 }], unresolvedRecords: 0, asOf: '2026-09-22T08:00:00Z' } })
  await (await response).finished()
  await expect(page.locator('.seller-funds-account')).toHaveCount(0)
  await expect(page.getByText('cccccccccccc', { exact: false })).toHaveCount(0)
  expect(unexpected).toEqual([])
})
