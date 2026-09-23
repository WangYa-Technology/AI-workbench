import { expect, test, type Page, type Route } from '@playwright/test'
import { topupSettingsFixture } from './helpers/topup'

const paymentID = '00000000-0000-4000-8000-000000009801'
const operationID = '00000000-0000-4000-8000-000000009802'
const createdAt = '2026-09-19T10:00:00Z'
const user = (id = 'finance-a', allowed = true) => ({ id, handle: id, email: `${id}@example.test`, displayName: id, role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: ['admin:access', ...(allowed ? ['admin:finance'] : [])] })
const payment = { id: paymentID, purpose: 'product', status: 'refunded', amountCents: 1900, currency: 'USD', liveMode: false, payerId: 'buyer', payerEmail: 'buyer@example.test', payerHandle: 'buyer', payerDisplayName: 'Buyer', resourceId: operationID, resourceTitle: 'Refund fixture', targetPath: '/workspace/orders', attentionCode: 'refund_reconciliation_required', version: 12, createdAt, updatedAt: createdAt }
const attempt = { operationId: operationID, provider: 'stripe', providerRefundId: 're_private_attempt', amountCents: 1900, currency: 'USD', status: 'pending', reconciliationRequired: true, requestedAt: createdAt }
const history = { items: [attempt], nextCursor: 'history-next', canCheck: true, paymentVersion: 12 }
const check = { id: operationID, origin: 'operator', status: 'completed', unresolvedCount: 1, requiresReview: true, createdAt }
const observation = (id: string) => ({ providerId: id, providerPaymentId: 'pi_private_original', amountCents: 1900, currency: 'USD', status: 'pending' })
const detail = { ...check, observedAt: createdAt, completedAt: createdAt, observations: [observation('re_private_detail')], unresolvedProviderRefundIds: ['re_private_detail'], lateReceiptCount: 2 }
const receipts = { items: [{ id: operationID, checkId: operationID, attemptNumber: 1, complete: true, createdAt, observations: [observation('re_private_receipt')], unresolvedProviderRefundIds: [] }], nextCursor: 'receipts-next' }
const base = `/api/v1/admin/payments/${paymentID}`
const failure = (status: number) => ({ status, json: { error: { code: status === 503 ? 'unavailable' : 'forbidden', message: `Fixture HTTP ${status}`, retryable: status >= 500 } } })
type MountedApp = HTMLElement & { __vue_app__: { config: { globalProperties: { $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> } } } } }
async function refreshSession(page: Page) {
  await page.evaluate(async () => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true) })
}
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined = () => undefined) {
  const unexpected: string[] = []
  const writes: string[] = []
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    if (route.request().method() !== 'GET') writes.push(`${route.request().method()} ${url.pathname}`)
    const handled = handler(route, url)
    if (handled) return handled
    if (route.request().method() !== 'GET') {
      unexpected.push(`${route.request().method()} ${url.pathname}`)
      return route.fulfill(failure(500))
    }
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user() } })
    if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
    if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
    if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (url.pathname === '/api/v1/admin/wallet-topup-settings') return route.fulfill({ json: topupSettingsFixture })
    if (['/api/v1/admin/finance/accounts', '/api/v1/admin/payment-destinations', '/api/v1/admin/subscription-plans', '/api/v1/admin/subscription-models', '/api/v1/admin/payment-providers', '/api/v1/admin/payments/webhook-quarantines'].includes(url.pathname)) return route.fulfill({ json: { items: [] } })
    if (url.pathname === '/api/v1/admin/payments') return route.fulfill({ json: { items: [payment] } })
    if (url.pathname === `${base}/refund-history`) return route.fulfill({ json: history })
    if (url.pathname === `${base}/refund-checks`) return route.fulfill({ json: { items: [check], nextCursor: 'checks-next' } })
    if (url.pathname === `${base}/refund-checks/${operationID}`) return route.fulfill({ json: detail })
    if (url.pathname === `${base}/refund-checks/${operationID}/receipts`) return route.fulfill({ json: receipts })
    unexpected.push(`GET ${url.pathname}`)
    return route.fulfill(failure(500))
  })
  return { unexpected, writes }
}
async function openHistory(page: Page, expand = true) {
  await page.goto('/admin?tab=finance')
  await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Refund history and checks', exact: true })
  await expect(drawer.getByText('re_private_attempt', { exact: true })).toBeVisible()
  if (expand) {
    await drawer.getByRole('button', { name: /Query complete/ }).click()
    await expect(drawer.getByText('re_private_receipt', { exact: true })).toBeVisible()
  }
  return drawer
}

for (const status of [401, 403, 404]) {
  for (const layer of ['history', 'checks', 'detail', 'receipts', 'command'] as const) {
    test(`refund ${layer} HTTP ${status} clears all evidence and blocks commands`, async ({ page }) => {
      let deny = false
      const { unexpected, writes } = await mockApp(page, (route, url) => {
        if (!deny) return
        const match = layer === 'history' ? url.pathname === `${base}/refund-history`
          : layer === 'checks' ? url.pathname === `${base}/refund-checks` && route.request().method() === 'GET'
            : layer === 'detail' ? url.pathname === `${base}/refund-checks/${operationID}`
              : layer === 'receipts' ? url.pathname.endsWith('/receipts')
                : url.pathname === `${base}/refund-checks` && route.request().method() === 'POST'
        if (match) return route.fulfill(failure(status))
      })
      const drawer = await openHistory(page)
      deny = true
      if (layer === 'history') await drawer.getByRole('button', { name: 'Refresh history', exact: true }).click()
      if (layer === 'checks') await drawer.getByRole('button', { name: 'Load more queries', exact: true }).click()
      if (layer === 'detail') {
        await drawer.getByRole('button', { name: /Query complete/ }).click()
        await drawer.getByRole('button', { name: /Query complete/ }).click()
      }
      if (layer === 'receipts') await drawer.getByRole('button', { name: 'Earlier receipt', exact: true }).click()
      if (layer === 'command') await drawer.getByRole('button', { name: 'Query payment provider', exact: true }).click()
      await expect(drawer.getByRole('alert')).toHaveText('You do not have access to this resource.')
      await expect(drawer.getByText('re_private_attempt', { exact: true })).toHaveCount(0)
      await expect(drawer.getByText('re_private_detail', { exact: true })).toHaveCount(0)
      await expect(drawer.getByText('re_private_receipt', { exact: true })).toHaveCount(0)
      await expect(drawer.getByRole('button', { name: 'Query payment provider', exact: true })).toBeDisabled()
      // Explicit successful refresh is required to enable a new provider query.
      deny = false
      await drawer.getByRole('button', { name: 'Refresh history', exact: true }).click()
      await expect(drawer.getByText('re_private_attempt', { exact: true })).toBeVisible()
      await expect(drawer.getByRole('button', { name: 'Query payment provider', exact: true })).toBeEnabled()
      expect(writes).toEqual(layer === 'command' ? [`POST ${base}/refund-checks`] : [])
      expect(unexpected).toEqual([])
    })
  }
}

for (const change of ['permission', 'account'] as const) {
  test(`refund selection closes on ${change} change and does not reopen with old context`, async ({ page }) => {
    let actor = user()
    let evidenceReads = 0
    const { unexpected, writes } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: actor } })
      if (url.pathname.startsWith(`${base}/`)) evidenceReads++
    })
    const drawer = await openHistory(page)
    const originalReads = evidenceReads
    actor = change === 'permission' ? user('finance-a', false) : user('finance-b')
    await refreshSession(page)
    await expect(drawer).toHaveCount(0)
    expect(evidenceReads).toBe(originalReads)
    actor = user()
    await refreshSession(page)
    await expect(drawer).toHaveCount(0)
    expect(evidenceReads).toBe(originalReads)
    await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
    await expect(drawer.getByText('re_private_attempt', { exact: true })).toBeVisible()
    expect(writes).toEqual([])
    expect(unexpected).toEqual([])
  })
}

test('temporary receipt failures preserve evidence and recover through retry', async ({ page }) => {
  let deny = false
  const { unexpected, writes } = await mockApp(page, (route, url) => {
    if (deny && url.pathname.endsWith('/receipts')) return route.fulfill(failure(503))
  })
  const drawer = await openHistory(page)
  deny = true
  await drawer.getByRole('button', { name: 'Earlier receipt', exact: true }).click()
  await expect(drawer.getByRole('alert')).toHaveText('The service is temporarily unavailable. Try again in a moment.')
  await expect(drawer.getByText('re_private_receipt', { exact: true })).toBeVisible()
  deny = false
  await drawer.getByRole('button', { name: 'Retry loading', exact: true }).click()
  await expect(drawer.getByRole('alert')).toHaveCount(0)
  expect(writes).toEqual([])
  expect(unexpected).toEqual([])
})

async function settle(page: Page, route: Route, status = 200) {
  const response = page.waitForResponse(r => r.url() === route.request().url() && r.request().method() === route.request().method())
  await route.fulfill(status === 200 ? { json: { ...history, items: [{ ...attempt, providerRefundId: 're_command_result' }], canCheck: false, paymentVersion: 13 } } : failure(status))
  await (await response).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

test('a current refund query uses the loaded payment version and refreshes its directory', async ({ page }) => {
  let pending: Route | undefined
  let reads = 0
  const { unexpected, writes } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/payments') { reads++; return route.fulfill({ json: { items: [payment] } }) }
    if (route.request().method() === 'POST' && url.pathname === `${base}/refund-checks`) {
      expect(route.request().postDataJSON()).toEqual({ expectedVersion: 12 })
      pending = route
      return Promise.resolve()
    }
  })
  const drawer = await openHistory(page, false)
  await expect.poll(() => reads).toBe(2)
  await drawer.getByRole('button', { name: 'Query payment provider', exact: true }).click()
  await expect.poll(() => Boolean(pending)).toBe(true)
  await expect(drawer.getByRole('button', { name: 'Query payment provider', exact: true })).toBeDisabled()
  await settle(page, pending!)
  await expect(drawer.getByText('re_command_result', { exact: true })).toBeVisible()
  await expect.poll(() => reads).toBe(3)
  expect(writes).toEqual([`POST ${base}/refund-checks`])
  expect(unexpected).toEqual([])
})

for (const status of [200, 403]) {
  test(`a previous drawer query HTTP ${status} cannot alter a reopened pending query`, async ({ page }) => {
    const pending: Route[] = []
    let reads = 0
    const { unexpected } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/admin/payments') { reads++; return route.fulfill({ json: { items: [payment] } }) }
      if (route.request().method() === 'POST' && url.pathname === `${base}/refund-checks`) { pending.push(route); return Promise.resolve() }
    })
    const drawer = await openHistory(page, false)
    await drawer.getByRole('button', { name: 'Query payment provider', exact: true }).click()
    await expect.poll(() => pending.length).toBe(1)
    await drawer.getByRole('button', { name: 'Close', exact: true }).click()
    await expect(drawer).toHaveCount(0)
    await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
    await drawer.getByRole('button', { name: 'Query payment provider', exact: true }).click()
    await expect.poll(() => pending.length).toBe(2)
    await expect.poll(() => reads).toBe(3)
    await settle(page, pending[0]!, status)
    await expect(drawer.getByRole('button', { name: 'Refresh history', exact: true })).toBeDisabled()
    await expect(drawer.getByText('re_private_attempt', { exact: true })).toBeVisible()
    await expect(drawer.getByRole('alert')).toHaveCount(0)
    expect(reads).toBe(3)
    await settle(page, pending[1]!)
    await expect(drawer.getByText('re_command_result', { exact: true })).toBeVisible()
    await expect.poll(() => reads).toBe(4)
    expect(unexpected).toEqual([])
  })
}

test('a receipt access denial invalidates an already pending provider query response', async ({ page }) => {
  let pending: Route | undefined
  let reads = 0
  const { unexpected } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/payments') { reads++; return route.fulfill({ json: { items: [payment] } }) }
    if (route.request().method() === 'POST' && url.pathname === `${base}/refund-checks`) { pending = route; return Promise.resolve() }
    if (pending && url.pathname.endsWith('/receipts')) return route.fulfill(failure(403))
  })
  const drawer = await openHistory(page)
  await drawer.getByRole('button', { name: 'Query payment provider', exact: true }).click()
  await expect.poll(() => Boolean(pending)).toBe(true)
  await drawer.getByRole('button', { name: 'Earlier receipt', exact: true }).click()
  await expect(drawer.getByText('re_private_attempt', { exact: true })).toHaveCount(0)
  await settle(page, pending!)
  await expect(drawer.getByText('re_command_result', { exact: true })).toHaveCount(0)
  await expect(drawer.getByRole('button', { name: 'Query payment provider', exact: true })).toBeDisabled()
  expect(reads).toBe(2)
  expect(unexpected).toEqual([])
})
