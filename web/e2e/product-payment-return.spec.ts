import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

const orderID = '00000000-0000-4000-8000-000000009601'
const paymentID = '00000000-0000-4000-8000-000000009602'
const assetID = '00000000-0000-4000-8000-000000000102'
const productID = '00000000-0000-4000-8000-000000000501'
const target = `/workspace/orders?payment=success&orderId=${orderID}&paymentId=${paymentID}`
const pending = {
  id: orderID, paymentId: paymentID, productId: productID, productTitle: 'Tracked licensed content',
  amountCents: 1900, currency: 'USD', status: 'payment_pending', paymentStatus: 'checkout_open',
  licenseCode: 'hcai-commercial-standard-v1', licenseName: 'Commercial license', licenseVersion: '1',
  licenseTerms: 'Licensed content.', refundWindowDays: 7, canRequestRefund: false,
  paymentMode: 'stripe', realCharge: false, createdAt: new Date().toISOString(), events: [],
}

for (const width of [390, 1308]) {
  test(`uncertain checkout stays in reconciliation and only refreshes the original order at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    await page.clock.install()
    const uncertain = { ...pending, paymentMode: 'waffo_pancake', paymentStatus: 'checkout_pending',
      checkoutReconciliationRequired: true, canCloseCheckout: false, paymentVersion: 2 }
    await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [uncertain] } }))
    let delivered = false
    await page.route(`**/api/v1/orders/${orderID}`, route => route.fulfill({ json: delivered
      ? { ...uncertain, status: 'fulfilled', paymentStatus: 'paid', checkoutReconciliationRequired: false, assetId: assetID }
      : uncertain }))
    const commands: string[] = []
    page.on('request', request => {
      if (request.url().includes('/api/v1/') && request.method() === 'POST') commands.push(request.url())
    })
    await page.goto(target)
    const card = page.locator('.product-payment-status')
    const actions = page.locator('.product-checkout-closure')
    await expect(card).toHaveAttribute('data-payment-state', 'reconciliation')
    await expect(actions).toContainText('Do not pay again')
    await expect(actions.getByRole('link', { name: 'Contact support' })).toHaveAttribute('href', '/support')
    await expect(actions.getByRole('button', { name: 'Close order', exact: true })).toHaveCount(0)
    await page.clock.fastForward(61000)
    await expect(card).toHaveAttribute('data-payment-state', 'reconciliation')
    delivered = true
    await card.getByRole('button', { name: 'Check again', exact: true }).click()
    await expect(card).toHaveAttribute('data-payment-state', 'fulfilled')
    await expect(actions).toHaveCount(0)
    await expect(card.getByRole('link', { name: 'View purchased content' })).toHaveAttribute('href', `/workspace/assets/${assetID}`)
    expect(commands).toEqual([])
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })
}

for (const width of [390, 1308]) {
  test(`payment return waits for fulfillment and locates an order outside the first page at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    await page.clock.install()
    await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
    let delivered = false
    let reads = 0
    await page.route(`**/api/v1/orders/${orderID}`, route => {
      reads++
      return route.fulfill({ json: delivered ? { ...pending, status: 'fulfilled', paymentStatus: 'paid', assetId: assetID } : pending })
    })
    await page.goto(target)
    const card = page.locator('.product-payment-status')
    await expect(card).toHaveAttribute('data-payment-state', 'pending')
    await expect(card.getByRole('link', { name: 'View purchased content' })).toHaveCount(0)
    await expect(page.locator('.order-row')).toHaveCount(1)
    delivered = true
    await page.clock.runFor(2600)
    await expect(card).toHaveAttribute('data-payment-state', 'fulfilled')
    await expect(card.getByRole('link', { name: 'View purchased content' })).toHaveAttribute('href', `/workspace/assets/${assetID}`)
    await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'fulfilled')
    const terminalReads = reads
    await page.clock.runFor(10000)
    expect(reads).toBe(terminalReads)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('payment-return.png') })
  })
}

test('leaving checkout follows the order without cancelling or confirming it', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
  const commands: string[] = []
  page.on('request', request => {
    if (request.url().includes('/api/v1/orders') && request.method() !== 'GET') commands.push(request.url())
  })
  await page.route(`**/api/v1/orders/${orderID}`, route => route.fulfill({ json: pending }))
  await page.goto(`/market/assets/${productID}?payment=cancelled&orderId=${orderID}&paymentId=${paymentID}`)
  await expect(page).toHaveURL(url => url.pathname === '/workspace/orders' && url.searchParams.get('orderId') === orderID)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'closed')
  await expect(page.getByText('There is no confirmed result yet. Leaving checkout does not cancel the order. We will check for any payment confirmation.')).toBeVisible()
  expect(commands).toEqual([])
})

test('transient failures retry within a bound and timeout can be refreshed', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.clock.install()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
  let reads = 0
  let delivered = false
  await page.route(`**/api/v1/orders/${orderID}`, route => {
    reads++
    if (reads === 1) return route.fulfill({ status: 503, json: { error: { code: 'temporary', message: 'Temporary', retryable: true } } })
    return route.fulfill({ json: delivered ? { ...pending, status: 'fulfilled', paymentStatus: 'paid', assetId: assetID } : pending })
  })
  await page.goto(target)
  const card = page.locator('.product-payment-status')
  await expect(card).toHaveAttribute('data-payment-state', 'connection')
  await page.clock.runFor(2600)
  await expect(card).toHaveAttribute('data-payment-state', 'pending')
  await page.clock.fastForward(61000)
  await expect(card).toHaveAttribute('data-payment-state', 'delayed')
  const stoppedReads = reads
  await page.clock.runFor(10000)
  expect(reads).toBe(stoppedReads)
  delivered = true
  await card.getByRole('button', { name: 'Check again', exact: true }).click()
  await expect(card).toHaveAttribute('data-payment-state', 'fulfilled')
})

test('invalid, mismatched and inaccessible return links never expose delivery', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
  let reads = 0
  let missing = false
  await page.route(`**/api/v1/orders/${orderID}`, route => {
    reads++
    return missing
      ? route.fulfill({ status: 404, json: { error: { code: 'order_not_found', message: 'Not found', retryable: false } } })
      : route.fulfill({ json: { ...pending, paymentId: '00000000-0000-4000-8000-000000009699', status: 'fulfilled', assetId: assetID } })
  })
  await page.goto('/workspace/orders?payment=success&orderId=invalid&paymentId=invalid')
  const card = page.locator('.product-payment-status')
  await expect(card).toHaveAttribute('data-payment-state', 'unavailable')
  expect(reads).toBe(0)
  await page.goto(target)
  await expect(card).toHaveAttribute('data-payment-state', 'unavailable')
  await expect(card).not.toContainText(pending.productTitle)
  await expect(card.getByRole('link', { name: 'View purchased content' })).toHaveCount(0)
  missing = true
  await page.reload()
  await expect(card).toHaveAttribute('data-payment-state', 'unavailable')
  await expect(page.locator('.order-row')).toHaveCount(0)
})

test('guest return preserves the original order link for sign in', async ({ page }) => {
  let reads = 0
  page.on('request', request => { if (request.url().includes(`/api/v1/orders/${orderID}`)) reads++ })
  await page.goto(target)
  const signIn = page.locator('.auth-required-state').getByRole('link', { name: 'Sign in', exact: true })
  await expect(signIn).toBeVisible()
  const href = await signIn.getAttribute('href')
  expect(new URL(href!, 'http://localhost').searchParams.get('returnTo')).toBe(target)
  expect(reads).toBe(0)
})

test('refund, failure and expired checkout states follow server evidence', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
  let current = { ...pending, checkoutExpiresAt: '' }
  await page.route(`**/api/v1/orders/${orderID}`, route => route.fulfill({ json: current }))
  for (const state of [
    { status: 'refund_requested', paymentStatus: 'refund_pending', phase: 'refundPending' },
    { status: 'refund_requested', paymentStatus: 'refund_failed', phase: 'refundFailed' },
    { status: 'refunded', paymentStatus: 'refunded', phase: 'refunded' },
    { status: 'payment_failed', paymentStatus: 'payment_failed', phase: 'failed' },
    { status: 'cancelled', paymentStatus: 'cancelled', phase: 'cancelled' },
    { status: 'payment_pending', paymentStatus: 'checkout_open', phase: 'expired' },
  ]) {
    current = { ...pending, status: state.status, paymentStatus: state.paymentStatus, checkoutExpiresAt: new Date(Date.now() - 60000).toISOString() }
    await page.goto(target)
    const card = page.locator('.product-payment-status')
    await expect(card).toHaveAttribute('data-payment-state', state.phase)
    await expect(card.getByRole('link', { name: 'View purchased content' })).toHaveCount(0)
  }
})

test('leaving a tracked order aborts its request and ignores late results', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  let finished!: () => void
  const handled = new Promise<void>(resolve => { finished = resolve })
  await page.route(`**/api/v1/orders/${orderID}`, async route => {
    await gate
    try { await route.fulfill({ json: { ...pending, status: 'fulfilled', paymentStatus: 'paid', assetId: assetID } }) }
    finally { finished() }
  })
  const requested = page.waitForRequest(`**/api/v1/orders/${orderID}`)
  await page.goto(target)
  await requested
  const aborted = page.waitForEvent('requestfailed', { predicate: request => request.url().endsWith(`/orders/${orderID}`) })
  await page.evaluate(async () => {
    const root = document.querySelector('#app') as Element & {
      __vue_app__: { config: { globalProperties: { $router: { push: (path: string) => Promise<void> } } } }
    }
    await root.__vue_app__.config.globalProperties.$router.push('/workspace/orders')
  })
  await aborted
  release()
  await handled
  await expect(page.locator('.product-payment-status')).toHaveCount(0)
  await expect(page.locator('.order-row')).toHaveCount(0)
  await expect(page.getByText('Your purchase is ready', { exact: true })).toHaveCount(0)
})
