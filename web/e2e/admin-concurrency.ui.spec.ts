import { expect, test, type Page, type Route } from '@playwright/test'
import { topupSettingsFixture } from './helpers/topup'

const firstID = '00000000-0000-4000-8000-000000008201'
const secondID = '00000000-0000-4000-8000-000000008202'
const plan = (id = firstID, name = 'First plan', version = 1) => ({ id, name, version, tierCode: id === firstID ? 'first' : 'second', description: 'Configured plan', priceCents: 1000, currency: 'USD', includedPoints: 10000, billingPeriodDays: 30, sortOrder: 0, active: true, modelIds: [] })
const provider = (merchantId = 'Original merchant') => ({ id: firstID, provider: 'stripe', enabled: false, environment: 'test', merchantId, storeId: '', productIdOnetime: '', productIdSubscription: '', secretConfigured: true, connectorConfigured: true, createdAt: '2026-09-21T00:00:00Z', updatedAt: '2026-09-21T00:00:00Z' })
const payment = (id = firstID) => ({ id, purpose: 'product', status: 'refund_failed', amountCents: 1900, currency: 'USD', liveMode: false, payerId: 'buyer', payerEmail: 'buyer@example.test', payerHandle: 'buyer', payerDisplayName: 'Buyer', resourceId: id, resourceTitle: id === firstID ? 'First payment' : 'Second payment', targetPath: '/workspace/orders', attentionCode: 'refund_failed', version: 12, createdAt: '2026-09-21T00:00:00Z', updatedAt: '2026-09-21T00:00:00Z' })
const failure = { error: { code: 'provider_unavailable', message: 'Temporarily unavailable', retryable: true } }

async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined) {
  const unexpected: string[] = [], errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    const handled = handler(route, url)
    if (handled) return handled
    if (route.request().method() === 'GET') {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: { id: 'finance', handle: 'finance', email: 'finance@example.test', displayName: 'Finance', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: ['admin:access', 'admin:finance'] } } })
      if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
      if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
      if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (url.pathname === '/api/v1/admin/subscription-plans') return route.fulfill({ json: { items: [plan(), plan(secondID, 'Second plan')] } })
      if (url.pathname === '/api/v1/admin/payment-providers') return route.fulfill({ json: { items: [provider()] } })
      if (url.pathname === '/api/v1/admin/wallet-topup-settings') return route.fulfill({ json: topupSettingsFixture })
      if (url.pathname === '/api/v1/admin/payments') return route.fulfill({ json: { items: [payment(), payment(secondID)] } })
      if (['/api/v1/admin/finance/accounts', '/api/v1/admin/payment-destinations', '/api/v1/admin/subscription-models', '/api/v1/admin/payments/webhook-quarantines'].includes(url.pathname)) return route.fulfill({ json: { items: [] } })
    }
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 503, json: failure })
  })
  return { unexpected, errors }
}

async function settle(page: Page, route: Route, json: unknown, status = 200) {
  const response = page.waitForResponse(r => r.url() === route.request().url() && r.request().method() === route.request().method())
  await route.fulfill({ status, json })
  await (await response).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

type Editor = 'plan' | 'provider' | 'command'
async function openEditor(page: Page, kind: Editor, second = false) {
  if (kind === 'plan') {
    await page.locator('.subscription-plan-admin-list article').filter({ hasText: second ? 'Second plan' : 'First plan' }).getByRole('button', { name: 'Edit plan', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Edit plan', exact: true })
    await dialog.getByLabel('Plan name', { exact: true }).fill(second ? 'New draft' : 'Saved first plan')
    return dialog
  }
  if (kind === 'provider') {
    await page.locator('.payment-method-table').first().getByRole('button', { name: 'Edit payment Provider', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Edit payment Provider', exact: true })
    await dialog.getByLabel('Merchant ID', { exact: true }).fill(second ? 'New merchant draft' : 'Saved merchant')
    return dialog
  }
  await page.locator('.payment-operation-list article').filter({ hasText: second ? 'Second payment' : 'First payment' }).getByRole('button', { name: 'Retry refund', exact: true }).click()
  return page.getByRole('dialog', { name: /CONTROLLED ACTION/ })
}
const submitName = (kind: Editor) => kind === 'plan' ? 'Save plan' : kind === 'provider' ? 'Save' : 'Apply'
const writePath = (kind: Editor) => kind === 'plan' ? `/api/v1/admin/subscription-plans/${firstID}` : kind === 'provider' ? '/api/v1/admin/payment-providers/stripe' : `/api/v1/admin/payments/${firstID}/recover`

test('plan conflict preserves the draft and requires reviewing a fresh version', async ({ page }) => {
  let reads = 0
  const submitted: Record<string, unknown>[] = []
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/subscription-plans') {
      return route.fulfill({ json: { items: [plan(firstID, 'First plan', ++reads === 1 ? 7 : 8)] } })
    }
    if (url.pathname === writePath('plan')) {
      const input = route.request().postDataJSON() as Record<string, unknown>
      submitted.push(input)
      return input.expectedVersion === 8
        ? route.fulfill({ json: plan(firstID, String(input.name), 9) })
        : route.fulfill({ status: 409, json: { error: { code: 'admin_state_conflict', message: 'Plan changed', retryable: false } } })
    }
  })
  await page.goto('/admin?tab=finance')
  const dialog = await openEditor(page, 'plan')
  await dialog.getByRole('button', { name: 'Save plan', exact: true }).click()
  await expect(page.getByText('The resource no longer allows this operation. Refresh and review its state.', { exact: true })).toBeVisible()
  await expect(dialog.getByLabel('Plan name', { exact: true })).toHaveValue('Saved first plan')
  expect(submitted.map(input => input.expectedVersion)).toEqual([7])
  await dialog.getByRole('button', { name: 'Save plan', exact: true }).click()
  await expect.poll(() => submitted.length).toBe(2)
  expect(submitted.map(input => input.expectedVersion)).toEqual([7, 7])
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await page.reload()
  const refreshed = await openEditor(page, 'plan')
  await refreshed.getByRole('button', { name: 'Save plan', exact: true }).click()
  await expect(refreshed).toHaveCount(0)
  expect(submitted.map(input => input.expectedVersion)).toEqual([7, 7, 8])
  await page.locator('.subscription-plan-admin-list article').getByRole('button', { name: 'Edit plan', exact: true }).click()
  const reopened = page.getByRole('dialog', { name: 'Edit plan', exact: true })
  await reopened.getByRole('button', { name: 'Save plan', exact: true }).click()
  await expect.poll(() => submitted.length).toBe(4)
  expect(submitted[3]?.expectedVersion).toBe(9)
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

for (const kind of ['plan', 'provider', 'command'] as const) {
  for (const status of [200, 503]) {
    test(`${kind} HTTP ${status} completion preserves a reopened editor`, async ({ page }) => {
      let pending: Route | undefined
      const { unexpected, errors } = await mockApp(page, (route, url) => {
        if (url.pathname === writePath(kind)) { pending = route; return Promise.resolve() }
      })
      await page.goto('/admin?tab=finance')
      const old = await openEditor(page, kind)
      await old.getByRole('button', { name: submitName(kind), exact: true }).click()
      await expect.poll(() => Boolean(pending)).toBe(true)
      await old.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(old).toHaveCount(0)
      // Opening and drafting stays available while a submitted operation finishes.
      // The pending operation must only update its original record, not this draft.
      const current = await openEditor(page, kind, true)
      const result = kind === 'plan' ? plan(firstID, 'Saved first plan') : kind === 'provider' ? provider('Saved merchant') : payment()
      await settle(page, pending!, status === 200 ? result : failure, status)
      await expect(current).toBeVisible()
      await expect(current.getByRole('button', { name: submitName(kind), exact: true })).toBeEnabled()
      if (kind === 'plan') {
        await expect(current.getByLabel('Plan name', { exact: true })).toHaveValue('New draft')
        if (status === 200) await expect(page.locator('.subscription-plan-admin-list article')).toContainText(['Saved first plan', 'Second plan'])
      }
      if (kind === 'provider') await expect(current.getByLabel('Merchant ID', { exact: true })).toHaveValue('New merchant draft')
      if (kind === 'command') await expect(current).toHaveAttribute('aria-label', /Second payment/)
      await expect(page.locator('.task-feedback.error')).toHaveCount(0)
      expect(unexpected).toEqual([])
      expect(errors).toEqual([])
    })
  }

  test(`${kind} duplicate form submission sends one command`, async ({ page }) => {
    const pending: Route[] = []
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === writePath(kind)) { pending.push(route); return Promise.resolve() }
    })
    await page.goto('/admin?tab=finance')
    const dialog = await openEditor(page, kind)
    await dialog.locator('form').evaluate(form => {
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
    await expect.poll(() => pending.length).toBeGreaterThan(0)
    await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    const count = pending.length
    for (const route of pending) await settle(page, route, failure, 503)
    expect(count).toBe(1)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

test('confirmed recovery followed by a failed refresh only retries reading', async ({ page }) => {
  let reads = 0, writes = 0
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/payments') {
      reads++
      return reads === 2 ? route.fulfill({ status: 503, json: failure }) : route.fulfill({ json: { items: [payment()] } })
    }
    if (url.pathname === writePath('command')) { writes++; return route.fulfill({ json: payment() }) }
  })
  await page.goto('/admin?tab=finance')
  const dialog = await openEditor(page, 'command')
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await expect(dialog).toHaveCount(0)
  await page.getByRole('alert').getByRole('button', { name: 'Reload records', exact: true }).click()
  await expect(page.locator('.payment-operation-list').getByText('First payment', { exact: true })).toBeVisible()
  expect(reads).toBe(3)
  expect(writes).toBe(1)
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

for (const [first, second] of [['command', 'plan'], ['plan', 'provider'], ['provider', 'command']] as const) {
  test(`a pending ${first} blocks a ${second} submit without losing its draft`, async ({ page }) => {
    let pending: Route | undefined
    let secondWrites = 0
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === writePath(first)) { pending = route; return Promise.resolve() }
      if (url.pathname === writePath(second)) { secondWrites++; return route.fulfill({ status: 503, json: failure }) }
    })
    await page.goto('/admin?tab=finance')
    const old = await openEditor(page, first)
    await old.getByRole('button', { name: submitName(first), exact: true }).click()
    await expect.poll(() => Boolean(pending)).toBe(true)
    await old.getByRole('button', { name: 'Cancel', exact: true }).click()
    await expect(old).toHaveCount(0)
    const current = await openEditor(page, second)
    await expect(current.getByRole('button', { name: submitName(second), exact: true })).toBeDisabled()
    await current.locator('form').dispatchEvent('submit')
    await settle(page, pending!, failure, 503)
    await expect(current.getByRole('button', { name: submitName(second), exact: true })).toBeEnabled()
    expect(secondWrites).toBe(0)
    await current.getByRole('button', { name: submitName(second), exact: true }).click()
    await expect.poll(() => secondWrites).toBe(1)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

for (const kind of ['plan', 'provider'] as const) {
  test(`${kind} edits made during saving are not discarded by the submitted receipt`, async ({ page }) => {
    let pending: Route | undefined
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === writePath(kind)) { pending = route; return Promise.resolve() }
    })
    await page.goto('/admin?tab=finance')
    const dialog = await openEditor(page, kind)
    await dialog.getByRole('button', { name: submitName(kind), exact: true }).click()
    await expect.poll(() => Boolean(pending)).toBe(true)
    await dialog.getByLabel(kind === 'plan' ? 'Plan name' : 'Merchant ID', { exact: true }).fill('Still editing')
    const submitted = pending!.request().postDataJSON()
    expect(submitted[kind === 'plan' ? 'name' : 'merchantId']).toBe(kind === 'plan' ? 'Saved first plan' : 'Saved merchant')
    await settle(page, pending!, kind === 'plan' ? plan(firstID, 'Saved first plan') : provider('Saved merchant'))
    await expect(dialog.getByLabel(kind === 'plan' ? 'Plan name' : 'Merchant ID', { exact: true })).toHaveValue('Still editing')
    await expect(dialog.getByRole('button', { name: submitName(kind), exact: true })).toBeEnabled()
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

for (const status of [200, 503]) {
  test(`an older aggregate refresh HTTP ${status} cannot replace a newer finance load`, async ({ page }) => {
    let reads = 0
    const pending: Route[] = []
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/admin/subscription-plans' && ++reads > 1) { pending.push(route); return Promise.resolve() }
    })
    await page.goto('/admin?tab=finance')
    await expect(page.locator('.subscription-plan-admin-list')).toBeVisible()
    // Both submits use the current URL; no workspace remount can hide the race.
    const form = await page.locator('.admin-finance-filters').first().elementHandle()
    await form!.dispatchEvent('submit')
    await expect.poll(() => pending.length).toBe(1)
    // Keep the event target for a queued submit while the loading state is
    // replacing the form. Both requests must actually be in flight.
    await form!.dispatchEvent('submit')
    await expect.poll(() => pending.length).toBe(2)
    await settle(page, pending[1], { items: [plan(firstID, 'Newest plan')] })
    await expect(page.locator('.subscription-plan-admin-list').getByText('Newest plan', { exact: true })).toBeVisible()
    await settle(page, pending[0], status === 200 ? { items: [plan(firstID, 'Stale plan')] } : failure, status)
    await expect(page.locator('.subscription-plan-admin-list').getByText('Newest plan', { exact: true })).toBeVisible()
    await expect(page.locator('.task-feedback.error')).toHaveCount(0)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

for (const creating of [false, true]) {
  test(`plan receipt uses its submitted identity across create/edit switch: creating=${creating}`, async ({ page }) => {
    let pending: Route | undefined
    const createdID = '00000000-0000-4000-8000-000000008203'
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname.startsWith('/api/v1/admin/subscription-plans') && route.request().method() !== 'GET') { pending = route; return Promise.resolve() }
    })
    await page.goto('/admin?tab=finance')
    const openNew = async () => {
      await page.getByRole('button', { name: 'Add plan', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: 'Add plan', exact: true })
      await dialog.getByLabel('Tier code', { exact: true }).fill('new_plan')
      await dialog.getByLabel('Plan name', { exact: true }).fill('New plan draft')
      await dialog.getByLabel('Plan description', { exact: true }).fill('New plan details')
      return dialog
    }
    const old = creating ? await openNew() : await openEditor(page, 'plan')
    await old.getByRole('button', { name: 'Save plan', exact: true }).click()
    await expect.poll(() => Boolean(pending)).toBe(true)
    const submittedPlan = pending!.request().postDataJSON() as Record<string, unknown>
    if (creating) expect(submittedPlan).not.toHaveProperty('expectedVersion')
    else expect(submittedPlan.expectedVersion).toBe(1)
    await old.getByRole('button', { name: 'Cancel', exact: true }).click()
    await expect(old).toHaveCount(0)
    const current = creating ? await openEditor(page, 'plan', true) : await openNew()
    await settle(page, pending!, plan(creating ? createdID : firstID, 'Saved receipt'))
    await expect(current).toBeVisible()
    const rows = page.locator('.subscription-plan-admin-list article')
    await expect(rows).toHaveCount(creating ? 3 : 2)
    await expect(rows.getByText('Saved receipt', { exact: true })).toHaveCount(1)
    await expect(current.getByLabel('Plan name', { exact: true })).toHaveValue(creating ? 'New draft' : 'New plan draft')
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

test('saving a previously absent provider inserts its confirmed configuration', async ({ page }) => {
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/payment-providers') return route.fulfill({ json: { items: [] } })
    if (url.pathname === writePath('provider')) return route.fulfill({ json: provider('Saved merchant') })
  })
  await page.goto('/admin?tab=finance')
  await page.getByRole('tab', { name: 'Stripe', exact: true }).click()
  await page.locator('.payment-gateway-channel-actions').getByRole('button', { name: 'Edit payment Provider', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Edit payment Provider', exact: true })
  await dialog.getByLabel('Merchant ID', { exact: true }).fill('Saved merchant')
  await dialog.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(page.locator('.payment-gateway-config-grid').getByText('Saved merchant', { exact: true })).toBeVisible()
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})
