import { expect, test, type Page, type Route } from '@playwright/test'
import { topupSettingsFixture } from './helpers/topup'

const adminPath = '/api/v1/admin/wallet-topup-settings'
const buyerPath = '/api/v1/billing/topup-settings'
const checkoutPath = '/api/v1/billing/topups/checkout'
const failure = (status = 503) => ({ status, json: { error: { code: status === 403 ? 'permission_required' : status === 409 ? 'admin_state_conflict' : 'provider_unavailable', message: 'Fixture failure', retryable: status === 503 } } })

async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined = () => undefined) {
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
      if (url.pathname === adminPath || url.pathname === buyerPath) return route.fulfill({ json: topupSettingsFixture })
      if (url.pathname === '/api/v1/billing/statement') return route.fulfill({ json: { account: { balanceCents: 0, reservedCents: 0, availableCents: 0, currency: 'USD' }, entries: [] } })
      if (url.pathname === '/api/v1/billing/points') return route.fulfill({ json: { account: { balancePoints: 0, availablePoints: 0, lifetimeSpentPoints: 0, heldPoints: 0 }, entries: [], plans: [] } })
      if (['/api/v1/admin/finance/accounts', '/api/v1/admin/payments', '/api/v1/admin/payment-destinations', '/api/v1/admin/subscription-plans', '/api/v1/admin/subscription-models', '/api/v1/admin/payment-providers', '/api/v1/admin/payments/webhook-quarantines'].includes(url.pathname)) return route.fulfill({ json: { items: [] } })
    }
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill(failure())
  })
  return { unexpected, errors }
}

const editor = (page: Page) => page.locator('.topup-settings-form')
const minimum = (page: Page) => editor(page).getByLabel('Minimum top-up (USD)', { exact: true })
const presets = (page: Page) => editor(page).getByLabel('Suggested amounts (USD)', { exact: true })
const reload = (page: Page) => page.locator('.payment-gateway-common').getByRole('button', { name: 'Reload records', exact: true })
const buyerForm = (page: Page) => page.locator('.billing-topup-form')

test('finance saves real cents and version, reloads persisted values and can remove suggestions', async ({ page }) => {
  let settings = structuredClone(topupSettingsFixture)
  const writes: unknown[] = []
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname !== adminPath) return
    if (route.request().method() === 'PUT') {
      const input = route.request().postDataJSON()
      writes.push(input)
      settings = { ...settings, ...input, version: settings.version + 1 }
    }
    return route.fulfill({ json: settings })
  })
  await page.goto('/admin?tab=finance')
  await minimum(page).fill('10.29')
  await presets(page).fill('20,10.29')
  await editor(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Top-up settings saved. New checkouts will use the updated minimum.', { exact: true })).toBeVisible()
  expect(writes).toEqual([{ expectedVersion: 1, minimumAmountCents: 1029, presetAmountsCents: [1029, 2000] }])
  await page.reload()
  await expect(minimum(page)).toHaveValue('10.29')
  await expect(presets(page)).toHaveValue('10.29, 20.00')
  await presets(page).fill('')
  await editor(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect.poll(() => writes.length).toBe(2)
  expect(writes[1]).toEqual({ expectedVersion: 2, minimumAmountCents: 1029, presetAmountsCents: [] })
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

test('finance rejects invalid minimum and duplicate suggestions without sending writes', async ({ page }) => {
  const { unexpected } = await mockApp(page)
  await page.goto('/admin?tab=finance')
  for (const [amount, suggestions] of [['0.49', '10'], ['1.001', '10'], ['1e2', '100'], ['1000000', ''], ['10', '5'], ['10', '10,10.00']]) {
    await minimum(page).fill(amount!)
    await presets(page).fill(suggestions!)
    await editor(page).getByRole('button', { name: 'Save', exact: true }).click()
    await expect(page.locator('.payment-gateway-common').getByRole('alert')).toContainText('at most two decimal places')
  }
  expect(unexpected).toEqual([])
})

test('finance load failure disables editing and explicit reload recovers', async ({ page }) => {
  let failed = true
  const { unexpected } = await mockApp(page, (route, url) => {
    if (url.pathname === adminPath && failed) return route.fulfill(failure())
  })
  await page.goto('/admin?tab=finance')
  await expect(page.getByText('Top-up settings are unavailable. Reload the records before editing.', { exact: true })).toBeVisible()
  await expect(editor(page)).toHaveCount(0)
  failed = false
  await reload(page).click()
  await expect(minimum(page)).toHaveValue('0.50')
  expect(unexpected).toEqual([])
})

test('a stale finance version preserves the draft until an explicit reload', async ({ page }) => {
  let reads = 0, writes = 0
  const { unexpected } = await mockApp(page, (route, url) => {
    if (url.pathname !== adminPath) return
    if (route.request().method() === 'PUT') { ++writes; return route.fulfill(failure(409)) }
    ++reads
    return route.fulfill({ json: reads === 1 ? topupSettingsFixture : { ...topupSettingsFixture, version: 3, minimumAmountCents: 1500, presetAmountsCents: [2000] } })
  })
  await page.goto('/admin?tab=finance')
  await minimum(page).fill('10')
  await editor(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.locator('.payment-gateway-common').getByRole('alert')).toBeVisible()
  await expect(minimum(page)).toHaveValue('10')
  expect(reads).toBe(1)
  expect(writes).toBe(1)
  await reload(page).click()
  await expect(minimum(page)).toHaveValue('15.00')
  expect(writes).toBe(1)
  expect(unexpected).toEqual([])
})

test('duplicate finance submissions are blocked while saving and permission denial removes the editor', async ({ page }) => {
  const pending: Route[] = []
  const { unexpected } = await mockApp(page, (route, url) => {
    if (url.pathname === adminPath && route.request().method() === 'PUT') { pending.push(route); return Promise.resolve() }
  })
  await page.goto('/admin?tab=finance')
  await minimum(page).fill('10')
  await editor(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect.poll(() => pending.length).toBe(1)
  await editor(page).dispatchEvent('submit')
  await expect(minimum(page)).toBeDisabled()
  await expect(reload(page)).toBeDisabled()
  expect(pending.length).toBe(1)
  await pending[0]!.fulfill(failure(403))
  await expect(editor(page)).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Check access again', exact: true })).toBeVisible()
  expect(unexpected).toEqual([])
})

test('buyer presets use configured cents and custom amounts require exact decimal precision', async ({ page }) => {
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === buyerPath) return route.fulfill({ json: { ...topupSettingsFixture, minimumAmountCents: 1029, presetAmountsCents: [1029, 2050] } })
  })
  await page.goto('/workspace/billing')
  const input = buyerForm(page).locator('input')
  const submit = buyerForm(page).locator('button[type="submit"]')
  await page.getByRole('group', { name: 'Suggested top-up amounts' }).getByRole('button').first().click()
  await expect(input).toHaveValue('10.29')
  await expect(submit).toBeEnabled()
  for (const amount of ['10.28', '10.291', '1e2', '-20', '', '1000000']) {
    await input.fill(amount)
    await expect(submit).toBeDisabled()
    await buyerForm(page).dispatchEvent('submit')
  }
  await input.fill('19.99')
  await expect(submit).toBeEnabled()
  await expect(page.locator('#topup-limits')).toContainText('10.29')
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

for (const refreshFails of [false, true]) {
  test(`changed minimum refreshes limits without another checkout; refresh fails=${refreshFails}`, async ({ page }) => {
    let reads = 0, writes = 0, failRefresh = refreshFails
    const { unexpected } = await mockApp(page, (route, url) => {
      if (url.pathname === buyerPath) {
        ++reads
        if (reads > 1 && failRefresh) return route.fulfill(failure())
        return route.fulfill({ json: reads === 1 ? topupSettingsFixture : { ...topupSettingsFixture, version: 2, minimumAmountCents: 5000, presetAmountsCents: [5000] } })
      }
      if (url.pathname === checkoutPath) {
        ++writes
        expect(route.request().postDataJSON()).toEqual({ amountCents: 2000 })
        return route.fulfill({ status: 422, json: { error: { code: 'wallet_topup_amount_out_of_range', message: 'Minimum changed', retryable: false } } })
      }
    })
    await page.goto('/workspace/billing')
    await buyerForm(page).locator('button[type="submit"]').click()
    await expect.poll(() => reads).toBe(2)
    if (refreshFails) {
      await expect(buyerForm(page)).toHaveCount(0)
      failRefresh = false
      await page.getByRole('alert').getByRole('button', { name: 'Try again', exact: true }).click()
      await expect(buyerForm(page)).toBeVisible()
    }
    else {
      await expect(buyerForm(page).locator('button[type="submit"]')).toBeDisabled()
      await expect(page.locator('#topup-limits')).toContainText('50.00')
    }
    await buyerForm(page).locator('input').fill('50.01')
    await expect(buyerForm(page).locator('button[type="submit"]')).toBeEnabled()
    expect(writes).toBe(1)
    expect(unexpected).toEqual([])
  })
}

test('missing buyer settings prevents checkout and empty suggestions preserve custom entry', async ({ page }) => {
  let failed = true
  const { unexpected } = await mockApp(page, (route, url) => {
    if (url.pathname === buyerPath) return route.fulfill(failed ? failure() : { json: { ...topupSettingsFixture, presetAmountsCents: [] } })
  })
  await page.goto('/workspace/billing')
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(buyerForm(page)).toHaveCount(0)
  failed = false
  await page.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(buyerForm(page)).toBeVisible()
  await expect(page.getByRole('group', { name: 'Suggested top-up amounts' })).toHaveCount(0)
  await expect(buyerForm(page).locator('button[type="submit"]')).toBeEnabled()
  expect(unexpected).toEqual([])
})

for (const kind of ['topup', 'subscription']) {
  test(`${kind} response loss keeps the page usable and reload retries the original checkout`, async ({ page }) => {
    const keys: string[] = [], payloads: unknown[] = []
    const endpoint = kind === 'topup' ? checkoutPath : '/api/v1/billing/subscriptions/checkout'
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/billing/points') return route.fulfill({ json: {
        account: { balancePoints: 0, availablePoints: 0, lifetimeSpentPoints: 0, heldPoints: 0 }, entries: [],
        plans: [{ id: 'plan-one', name: 'Monthly plan', description: 'Monthly points', modelIds: [], includedPoints: 100, priceCents: 500, currency: 'USD', billingPeriodDays: 30 }],
      } })
      if (url.pathname === endpoint) {
        keys.push(route.request().headers()['idempotency-key']!)
        payloads.push(route.request().postDataJSON())
        if (keys.length === 1) return route.abort('failed')
        return route.fulfill({ json: { paymentId: 'original-payment', checkoutUrl: new URL('/market', route.request().url()).href } })
      }
    })
    const submit = () => kind === 'topup' ? buyerForm(page).locator('button[type="submit"]') : page.locator('.billing-plan-list').getByRole('button')
    await page.goto('/workspace/billing')
    await submit().click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(submit()).toBeEnabled()
    await expect(buyerForm(page)).toBeVisible()
    expect(keys).toHaveLength(1)
    await page.reload()
    await expect(submit()).toBeEnabled()
    expect(keys).toHaveLength(1)
    const popupPromise = page.waitForEvent('popup')
    await submit().click()
    const popup = await popupPromise
    await expect(popup).toHaveURL(/\/market$/)
    expect(keys).toHaveLength(2)
    expect(keys[0]).toBeTruthy()
    expect(keys[1]).toBe(keys[0])
    expect(payloads).toEqual(Array(2).fill(kind === 'topup' ? { amountCents: 2000 } : { planId: 'plan-one' }))
    await popup.close()
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

for (const width of [1440, 390]) {
  for (const view of ['buyer', 'finance']) {
    test(`${view} top-up controls fit a ${width}px viewport`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 })
      const { unexpected, errors } = await mockApp(page)
      await page.goto(view === 'buyer' ? '/workspace/billing' : '/admin?tab=finance')
      const form = view === 'buyer' ? buyerForm(page) : editor(page)
      await expect(form).toBeVisible()
      await form.scrollIntoViewIfNeeded()
      const bounds = await form.boundingBox()
      expect(bounds).not.toBeNull()
      expect(bounds!.x).toBeGreaterThanOrEqual(0)
      expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width + 1)
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true)
      await testInfo.attach(`${view}-${width}`, { body: await page.screenshot(), contentType: 'image/png' })
      expect(unexpected).toEqual([])
      expect(errors).toEqual([])
    })
  }
}
