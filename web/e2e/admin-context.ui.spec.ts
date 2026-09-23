import { expect, test, type Page, type Route } from '@playwright/test'
import { topupSettingsFixture } from './helpers/topup'

const paymentID = '00000000-0000-4000-8000-000000008101'
const secondID = '00000000-0000-4000-8000-000000008102'
const actor = (id = 'finance-a', finance = true) => ({ id, handle: id, email: `${id}@example.test`, displayName: id, role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: ['admin:access', ...(finance ? ['admin:finance'] : [])] })
const payment = (title = 'Original payment', id = paymentID) => ({ id, purpose: 'product', status: 'refund_failed', amountCents: 1900, currency: 'USD', liveMode: false, payerId: 'buyer', payerEmail: 'buyer@example.test', payerHandle: 'buyer', payerDisplayName: 'Buyer', resourceId: id, resourceTitle: title, targetPath: '/workspace/orders', attentionCode: 'refund_failed', version: 12, createdAt: '2026-09-19T10:00:00Z', updatedAt: '2026-09-19T10:00:00Z' })
const denied = { error: { code: 'forbidden', message: 'Fixture denial', retryable: false } }
type MountedApp = HTMLElement & { __vue_app__: { config: { globalProperties: {
  $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> }
  $router: { push: (path: string) => Promise<unknown> }
} } } }
async function refreshSession(page: Page) {
  await page.evaluate(async () => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true) })
}
async function navigate(page: Page, path: string) {
  await page.evaluate(async path => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$router.push(path) }, path)
}
async function settle(page: Page, route: Route, json: unknown, status = 200) {
  const response = page.waitForResponse(r => r.url() === route.request().url() && r.request().method() === route.request().method())
  await route.fulfill({ json, status })
  await (await response).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined) {
  const unexpected: string[] = []
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    const handled = handler(route, url)
    if (handled) return handled
    if (route.request().method() === 'GET') {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: actor() } })
      if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
      if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
      if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (url.pathname === '/api/v1/admin/payments') return route.fulfill({ json: { items: [payment()] } })
      if (url.pathname === '/api/v1/admin/wallet-topup-settings') return route.fulfill({ json: topupSettingsFixture })
      if (['/api/v1/admin/finance/accounts', '/api/v1/admin/payment-destinations', '/api/v1/admin/subscription-plans', '/api/v1/admin/subscription-models', '/api/v1/admin/payment-providers', '/api/v1/admin/payments/webhook-quarantines'].includes(url.pathname)) return route.fulfill({ json: { items: [] } })
    }
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 403, json: denied })
  })
  return { unexpected, errors }
}
const list = (page: Page) => page.locator('.payment-operation-list')
test('finance-only plan editor uses minimal model catalog for create and update', async ({ page }) => {
  const model = { id: '00000000-0000-4000-8000-000000008111', displayName: 'Plan image model', mode: 'image', providerName: 'Model provider' }
  const writes: { method: string; modelIds: string[] }[] = []
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/subscription-models') return route.fulfill({ json: { items: [model] } })
    if (url.pathname.startsWith('/api/v1/admin/subscription-plans') && route.request().method() !== 'GET') {
      const payload = route.request().postDataJSON()
      writes.push({ method: route.request().method(), modelIds: payload.modelIds })
      return route.fulfill({ status: route.request().method() === 'POST' ? 201 : 200, json: { ...payload, id: secondID } })
    }
  })
  await page.goto('/admin?tab=finance')
  await expect(list(page).getByText('Original payment', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Add plan', exact: true }).click()
  let dialog = page.getByRole('dialog', { name: 'Add plan', exact: true })
  await dialog.getByLabel('Tier code', { exact: true }).fill('finance_plan')
  await dialog.getByLabel('Plan name', { exact: true }).fill('Finance plan')
  await dialog.getByLabel('Plan description', { exact: true }).fill('A finance-only plan')
  await dialog.getByRole('checkbox', { name: /Plan image model/ }).check()
  await dialog.getByRole('button', { name: 'Save plan', exact: true }).click()
  await expect(page.getByText('Subscription plan saved.', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Edit plan', exact: true }).click()
  dialog = page.getByRole('dialog', { name: 'Edit plan', exact: true })
  await expect(dialog.getByRole('checkbox', { name: /Plan image model/ })).toBeChecked()
  await dialog.getByLabel('Plan name', { exact: true }).fill('Updated finance plan')
  await dialog.getByRole('button', { name: 'Save plan', exact: true }).click()
  await expect(page.locator('#main-content').getByText('Updated finance plan', { exact: true })).toBeVisible()
  expect(writes).toEqual([{ method: 'POST', modelIds: [model.id] }, { method: 'PATCH', modelIds: [model.id] }])
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

async function openCommand(page: Page) {
  await list(page).getByRole('button', { name: 'Retry refund', exact: true }).click()
  return page.getByRole('dialog', { name: /CONTROLLED ACTION/ })
}

test('confirmed account changes replace finance data and discard the open command', async ({ page }) => {
  let current = actor()
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: current } })
    if (url.pathname === '/api/v1/admin/payments') return route.fulfill({ json: { items: [payment(current.id)] } })
  })
  await page.goto('/admin?tab=finance')
  await expect(list(page).getByText('finance-a', { exact: true })).toBeVisible()
  const dialog = await openCommand(page)
  current = actor('finance-b')
  await refreshSession(page)
  await expect(dialog).toHaveCount(0)
  await expect(list(page).getByText('finance-b', { exact: true })).toBeVisible()
  await expect(list(page).getByText('finance-a', { exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

test('finance reauthorization reloads data and never revives an old command', async ({ page }) => {
  let current = actor()
  let title = 'Original payment'
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: current } })
    if (url.pathname === '/api/v1/admin/payments') return route.fulfill({ json: { items: [payment(title)] } })
  })
  await page.goto('/admin?tab=finance')
  const dialog = await openCommand(page)
  current = actor('finance-a', false)
  await refreshSession(page)
  await expect(dialog).toHaveCount(0)
  title = 'Fresh authorization'
  current = actor()
  await refreshSession(page)
  await expect(dialog).toHaveCount(0)
  await expect(list(page).getByText(title, { exact: true })).toBeVisible()
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

for (const initial of ['first', 'more']) {
  test(`a delayed finance ${initial} page cannot populate another account`, async ({ page }) => {
    let current = actor()
    let pending: Route | undefined
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: current } })
      if (url.pathname === '/api/v1/admin/payments') {
        if (current.id === 'finance-a' && (initial === 'first' || url.searchParams.has('cursor'))) { pending = route; return Promise.resolve() }
        return route.fulfill({ json: { items: [payment(current.id)], nextCursor: 'next-page' } })
      }
    })
    await page.goto('/admin?tab=finance')
    if (initial === 'more') await page.locator('.payment-operations-admin').getByRole('button', { name: 'Load more', exact: true }).click()
    await expect.poll(() => Boolean(pending)).toBe(true)
    current = actor('finance-b')
    await refreshSession(page)
    await settle(page, pending!, { items: [payment('Stale private payment', secondID)], nextCursor: 'stale-cursor' })
    await expect(list(page).getByText('finance-b', { exact: true })).toBeVisible()
    await expect(list(page).getByText('Stale private payment', { exact: true })).toHaveCount(0)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

for (const destination of ['stay', 'account', 'filter', 'away-back'] as const) {
  test(`payment recovery completion belongs to its original finance view: ${destination}`, async ({ page }) => {
    let current = actor()
    let pending: Route | undefined
    let reads = 0
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: current } })
      if (url.pathname === '/api/v1/admin/payments') { reads++; return route.fulfill({ json: { items: [payment()] } }) }
      if (url.pathname === `/api/v1/admin/payments/${paymentID}/recover`) {
        expect(route.request().method()).toBe('POST')
        expect(route.request().postDataJSON()).toEqual({ action: 'retry_refund', expectedVersion: 12 })
        pending = route
        return Promise.resolve()
      }
    })
    await page.goto('/admin?tab=finance')
    const dialog = await openCommand(page)
    await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
    await expect.poll(() => Boolean(pending)).toBe(true)
    if (destination === 'account') { current = actor('finance-b'); await refreshSession(page) }
    if (destination === 'filter') await navigate(page, '/admin?tab=finance&paymentQ=current')
    if (destination === 'away-back') {
      await navigate(page, '/admin?tab=missing')
      await navigate(page, '/admin?tab=finance')
    }
    if (destination !== 'stay') {
      await expect(dialog).toHaveCount(0)
      await expect.poll(() => reads).toBeGreaterThan(1)
    }
    const before = reads
    await settle(page, pending!, payment('Operation receipt'))
    if (destination === 'stay') {
      await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
      await expect.poll(() => reads).toBe(before + 1)
    } else {
      await expect(page.getByText('Operation completed.', { exact: true })).toHaveCount(0)
      expect(reads).toBe(before)
    }
    await expect(dialog).toHaveCount(0)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

for (const status of [200, 403]) {
  test(`old recovery HTTP ${status} cannot unlock or replace a new recovery command`, async ({ page }) => {
    const pending: Route[] = []
    let reads = 0
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/admin/payments') { reads++; return route.fulfill({ json: { items: [payment()] } }) }
      if (url.pathname === `/api/v1/admin/payments/${paymentID}/recover`) { pending.push(route); return Promise.resolve() }
    })
    await page.goto('/admin?tab=finance')
    const dialog = await openCommand(page)
    await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
    await expect.poll(() => pending.length).toBe(1)
    await navigate(page, '/admin?tab=finance&paymentQ=current')
    await expect(dialog).toHaveCount(0)
    const current = await openCommand(page)
    await current.getByRole('button', { name: 'Apply', exact: true }).click()
    await expect.poll(() => pending.length).toBe(2)
    await expect.poll(() => reads).toBe(2)
    await settle(page, pending[0], status === 200 ? payment() : denied, status)
    await expect(current.getByRole('button', { name: 'Apply', exact: true })).toBeDisabled()
    expect(reads).toBe(2)
    await expect(page.getByText('Operation completed.', { exact: true })).toHaveCount(0)
    await settle(page, pending[1], payment())
    await expect(current).toHaveCount(0)
    await expect.poll(() => reads).toBe(3)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

for (const status of [401, 403]) {
  test(`parent command HTTP ${status} clears the workspace before cached session changes`, async ({ page }) => {
    let commands = 0
    let reads = 0
    let sessionReads = 0
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session') { sessionReads++; return route.fulfill({ json: { user: actor() } }) }
      if (url.pathname === '/api/v1/admin/payments') { reads++; return route.fulfill({ json: { items: [payment(reads === 1 ? 'Original payment' : 'Verified payment')] } }) }
      if (url.pathname === `/api/v1/admin/payments/${paymentID}/recover`) { commands++; return route.fulfill({ status, json: denied }) }
    })
    await page.goto('/admin?tab=finance')
    const dialog = await openCommand(page)
    await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Operations access needs verification' })).toBeVisible()
    await expect(dialog).toHaveCount(0)
    await expect(list(page)).toHaveCount(0)
    const before = sessionReads
    await page.getByRole('button', { name: 'Check access again', exact: true }).click()
    await expect(list(page).getByText('Verified payment', { exact: true })).toBeVisible()
    expect(sessionReads).toBe(before + 1)
    expect(commands).toBe(1)
    expect(reads).toBe(2)
    await expect(dialog).toHaveCount(0)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

for (const status of [404, 409, 503]) {
  test(`resource or temporary parent HTTP ${status} does not revoke the whole workspace`, async ({ page }) => {
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === `/api/v1/admin/payments/${paymentID}/recover`) return route.fulfill({ status, json: denied })
    })
    await page.goto('/admin?tab=finance')
    const dialog = await openCommand(page)
    await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
    await expect(dialog.getByRole('button', { name: 'Apply', exact: true })).toBeEnabled()
    await expect(list(page).getByText('Original payment', { exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Operations access needs verification' })).toHaveCount(0)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

test('parent denial discards late sibling data and failed session revalidation stays closed', async ({ page }) => {
  let pending: Route | undefined
  let sessionFailed = false
  let deniedRead = true
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session' && sessionFailed) return route.fulfill({ status: 503, json: denied })
    if (url.pathname === '/api/v1/admin/finance/accounts' && deniedRead) return route.fulfill({ status: 403, json: denied })
    if (url.pathname === '/api/v1/admin/payments' && deniedRead) { pending = route; return Promise.resolve() }
  })
  await page.goto('/admin?tab=finance')
  await expect.poll(() => Boolean(pending)).toBe(true)
  await expect(page.getByRole('heading', { name: 'Operations access needs verification' })).toBeVisible()
  await settle(page, pending!, { items: [payment('Late private payment')] })
  await expect(page.getByText('Late private payment', { exact: true })).toHaveCount(0)
  sessionFailed = true
  await page.getByRole('button', { name: 'Check access again', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Check access again', exact: true })).toBeEnabled()
  await expect(list(page)).toHaveCount(0)
  sessionFailed = false
  deniedRead = false
  await page.getByRole('button', { name: 'Check access again', exact: true }).click()
  await expect(list(page).getByText('Original payment', { exact: true })).toBeVisible()
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

test('late access verification cannot reload a different finance visit', async ({ page }) => {
  let sessionReads = 0
  let paymentReads = 0
  let pending: Route | undefined
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session' && ++sessionReads > 1) { pending = route; return Promise.resolve() }
    if (url.pathname === '/api/v1/admin/payments') { paymentReads++; return route.fulfill({ json: { items: [payment()] } }) }
    if (url.pathname === `/api/v1/admin/payments/${paymentID}/recover`) return route.fulfill({ status: 403, json: denied })
  })
  await page.goto('/admin?tab=finance')
  const dialog = await openCommand(page)
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
  await page.getByRole('button', { name: 'Check access again', exact: true }).click()
  await expect.poll(() => Boolean(pending)).toBe(true)
  await navigate(page, '/admin?tab=finance&paymentQ=current')
  const current = await openCommand(page)
  expect(paymentReads).toBe(2)
  await settle(page, pending!, { user: actor() })
  await expect(current).toBeVisible()
  expect(paymentReads).toBe(2)
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

for (const identity of ['signed-out', 'revoked'] as const) {
  test(`access verification respects the current ${identity} session without replaying commands`, async ({ page }) => {
    let blocked = false
    let reads = 0
    let commands = 0
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session' && blocked) return route.fulfill({ json: { user: identity === 'signed-out' ? null : actor('finance-a', false) } })
      if (url.pathname === '/api/v1/admin/payments') { reads++; return route.fulfill({ json: { items: [payment()] } }) }
      if (url.pathname === `/api/v1/admin/payments/${paymentID}/recover`) { blocked = true; commands++; return route.fulfill({ status: 403, json: denied }) }
    })
    await page.goto('/admin?tab=finance')
    const dialog = await openCommand(page)
    await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
    await page.getByRole('button', { name: 'Check access again', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Operations access needs verification' })).toHaveCount(0)
    await expect(list(page)).toHaveCount(0)
    await expect(dialog).toHaveCount(0)
    expect(reads).toBe(1)
    expect(commands).toBe(1)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

test('applying payment filters performs a single fresh directory read', async ({ page }) => {
  const queries: string[] = []
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/payments') {
      queries.push(url.searchParams.get('q') || '')
      return route.fulfill({ json: { items: [payment(queries.at(-1) || 'Original payment')] } })
    }
  })
  await page.goto('/admin?tab=finance')
  await expect(list(page).getByText('Original payment', { exact: true })).toBeVisible()
  const section = page.locator('.payment-operations-admin')
  await section.getByRole('searchbox', { name: 'Search payments', exact: true }).fill('Filtered payment')
  await section.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(list(page).getByText('Filtered payment', { exact: true })).toBeVisible()
  expect(queries).toEqual(['', 'Filtered payment'])
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

test('an anchor-only navigation preserves the current finance draft', async ({ page }) => {
  let reads = 0
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/payments') { reads++; return route.fulfill({ json: { items: [payment()] } }) }
  })
  await page.goto('/admin?tab=finance')
  const dialog = await openCommand(page)
  await navigate(page, '/admin?tab=finance#payment')
  await expect(dialog).toBeVisible()
  expect(reads).toBe(1)
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})
