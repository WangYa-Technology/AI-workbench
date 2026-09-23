import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

const orderId = '00000000-0000-4000-8000-000000009801'
const paymentId = '00000000-0000-4000-8000-000000009802'
const productId = '00000000-0000-4000-8000-000000000501'
const pending = {
  id: orderId, paymentId, productId, productTitle: 'Preparation interrupted',
  amountCents: 1900, currency: 'USD', status: 'payment_pending', paymentStatus: 'checkout_pending',
  licenseCode: 'hcai-commercial-standard-v1', licenseName: 'Commercial license', licenseVersion: '1',
  licenseTerms: 'Licensed content.', refundWindowDays: 7, canRequestRefund: false,
  paymentMode: 'stripe', realCharge: false, createdAt: new Date().toISOString(), events: [],
  paymentVersion: 1, canCloseCheckout: true, checkoutClosedBeforePayment: false,
}
const closed = { ...pending, status: 'cancelled', paymentStatus: 'cancelled', paymentVersion: 2, canCloseCheckout: false, checkoutClosedBeforePayment: true }

for (const width of [390, 1308]) {
  test(`confirms pre-payment closure without a follow-up refresh at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [pending] } }))
    // A refresh outage must not hide an accepted closure response.
    let refreshes = 0
    await page.route(`**/api/v1/orders/${orderId}`, route => { refreshes++; return route.abort() })
    let commands = 0
    await page.route(`**/api/v1/orders/${orderId}/close-checkout`, route => {
      commands++
      expect(route.request().postDataJSON()).toEqual({ expectedVersion: 1, confirmed: true })
      expect(route.request().headers()['idempotency-key']).toBeTruthy()
      return route.fulfill({ json: closed })
    })
    await page.goto('/workspace/orders')
    const control = page.locator('.product-checkout-closure')
    await control.getByRole('button', { name: 'Close order', exact: true }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('review the product and license')
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    expect(commands).toBe(0)
    await control.getByRole('button', { name: 'Close order', exact: true }).click()
    await dialog.getByRole('button', { name: 'Close order', exact: true }).click()
    await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'cancelled')
    await expect(control).toContainText('Order closed before payment started')
    await expect(control.getByRole('link', { name: 'Review product' })).toHaveAttribute('href', `/market/assets/${productId}`)
    await expect(control.getByRole('button', { name: 'Close order', exact: true })).toHaveCount(0)
    expect(commands).toBe(1)
    expect(refreshes).toBe(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('closed-order.png') })
  })
}

test('lost closure response retries the original command and observed version', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [pending] } }))
  const keys: string[] = []
  await page.route(`**/api/v1/orders/${orderId}/close-checkout`, route => {
    keys.push(route.request().headers()['idempotency-key'])
    expect(route.request().postDataJSON()).toEqual({ expectedVersion: 1, confirmed: true })
    return keys.length === 1 ? route.abort() : route.fulfill({ json: closed })
  })
  await page.goto('/workspace/orders')
  const control = page.locator('.product-checkout-closure')
  await control.getByRole('button', { name: 'Close order', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Close order', exact: true }).click()
  await expect(control.getByRole('alert')).toBeVisible()
  await control.getByRole('button', { name: 'Retry closing' }).click()
  await expect(control).toContainText('Order closed before payment started')
  expect(keys).toHaveLength(2)
  expect(keys[1]).toBe(keys[0])
})

test('stale closure requires refresh and does not claim an uncertain checkout is uncharged', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [pending] } }))
  await page.route(`**/api/v1/orders/${orderId}/close-checkout`, route => route.fulfill({ status: 409, json: { error: { code: 'checkout_closure_unavailable', retryable: false } } }))
  await page.route(`**/api/v1/orders/${orderId}`, route => route.fulfill({ json: { ...pending, canCloseCheckout: false, paymentVersion: 2, paymentStatus: 'checkout_open' } }))
  await page.goto('/workspace/orders')
  const control = page.locator('.product-checkout-closure')
  await control.getByRole('button', { name: 'Close order', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Close order', exact: true }).click()
  await expect(control.getByRole('alert')).toContainText('may have started payment')
  await expect(control).not.toContainText('No payment request has been sent')
  await expect(control.getByRole('button', { name: 'Retry closing' })).toHaveCount(0)
  await control.getByRole('button', { name: 'Check again' }).click()
  await expect(control).toHaveCount(0)
})

test('legacy and dispatched orders cannot offer local closure', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [{ ...pending, canCloseCheckout: false }] } }))
  await page.goto('/workspace/orders')
  await expect(page.locator('.order-row')).toHaveCount(1)
  await expect(page.locator('.product-checkout-closure')).toHaveCount(0)
})

test('payment return distinguishes a proven pre-dispatch closure', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
  await page.route(`**/api/v1/orders/${orderId}`, route => route.fulfill({ json: closed }))
  await page.goto(`/workspace/orders?payment=cancelled&orderId=${orderId}&paymentId=${paymentId}`)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'closedBeforePayment')
  await expect(page.locator('.product-payment-status')).toContainText('No payment request was sent for this order.')
})

test('a late closure response is ignored after leaving the order page', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [pending] } }))
  let release!: () => void
  let received!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  const started = new Promise<void>(resolve => { received = resolve })
  await page.route(`**/api/v1/orders/${orderId}/close-checkout`, async route => {
    received()
    await gate
    await route.fulfill({ json: closed }).catch(() => undefined)
  })
  await page.goto('/workspace/orders')
  await page.locator('.product-checkout-closure').getByRole('button', { name: 'Close order', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Close order', exact: true }).click()
  await started
  await page.goto('/discover')
  release()
  await expect(page).toHaveURL(/\/discover$/)
  await expect(page.locator('.product-checkout-closure')).toHaveCount(0)
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
})
