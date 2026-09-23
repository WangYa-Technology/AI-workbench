import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

const paymentID = '00000000-0000-4000-8000-000000009811'
const orderID = '00000000-0000-4000-8000-000000009812'
const productID = '00000000-0000-4000-8000-000000000501'
const createdAt = '2026-09-19T10:00:00Z'

for (const width of [390, 1308]) {
  for (const recovery of [
    { action: 'check_checkout', label: 'Check checkout payment', capability: 'canCheckCheckout', attention: 'none' },
    { action: 'verify_identity', label: 'Verify original merchant', capability: 'canVerifyIdentity', attention: 'identity_verification_required' },
    { action: 'locate_checkout', label: 'Locate original checkout', capability: 'canLocateCheckout', attention: 'checkout_reconciliation_required' },
  ]) {
    test(`finance queues ${recovery.action} without a money command at ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 })
      expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
      let queued = false
      // Saved authenticated payment evidence can qualify a check before expiry.
      const title = recovery.action === 'check_checkout' ? 'Recovered paid checkout' : 'Checkout recovery'
      const payment = () => ({
        id: paymentID, purpose: 'product', status: recovery.action === 'locate_checkout' ? 'checkout_pending' : 'checkout_open', amountCents: 1900, currency: 'USD', liveMode: false,
        payerId: '00000000-0000-4000-8000-000000000002', payerEmail: 'buyer@example.test', payerHandle: 'buyer', payerDisplayName: 'Buyer',
        resourceId: productID, resourceTitle: title, targetPath: '/workspace/orders',
        checkoutExpiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
        attentionCode: recovery.attention, [recovery.capability]: !queued, version: queued ? 13 : 12, createdAt, updatedAt: createdAt,
      })
      await page.route(/\/api\/v1\/admin\/payments(?:\?|$)/, route => route.fulfill({ json: { items: [payment()] } }))
      const commands: unknown[] = []
      await page.route(`**/api/v1/admin/payments/${paymentID}/recover`, async route => {
        commands.push(route.request().postDataJSON())
        expect(route.request().postDataJSON()).toEqual({ action: recovery.action, expectedVersion: 12 })
        queued = true
        await route.fulfill({ json: payment() })
      })
      await page.goto('/admin?tab=finance')
      await page.getByRole('button', { name: recovery.label, exact: true }).click()
      const drawer = page.getByRole('dialog').filter({ has: page.getByRole('heading', { name: title, exact: true }) })
      await expect(drawer).toBeVisible()
      if (recovery.action === 'verify_identity') await expect(drawer.getByText('Read the saved transaction to verify its merchant.', { exact: false })).toBeVisible()
      if (recovery.action === 'locate_checkout') await expect(drawer.getByText('Find an existing checkout in the original merchant account', { exact: false })).toBeVisible()
      await expect.poll(() => drawer.evaluate(el => getComputedStyle(el).filter)).toBe('none')
      await page.screenshot({ path: testInfo.outputPath('checkout-recovery.png'), animations: 'disabled' })
      await drawer.getByRole('button', { name: 'Apply', exact: true }).click()
      await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
      await expect(page.getByRole('button', { name: recovery.label, exact: true })).toHaveCount(0)
      expect(commands).toEqual([{ action: recovery.action, expectedVersion: 12 }])
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    })
  }
}

test('closed order returns to product review without automatically paying again', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
  await page.route(`**/api/v1/orders/${orderID}`, route => route.fulfill({ json: {
    id: orderID, productId: productID, paymentId: paymentID, productTitle: 'Expired purchase', status: 'cancelled', paymentStatus: 'cancelled',
    amountCents: 1900, currency: 'USD', licenseName: 'Commercial', licenseVersion: '1', licenseTerms: 'Terms', refundWindowDays: 7,
    canRequestRefund: false, paymentMode: 'stripe', realCharge: false, createdAt, events: [],
  } }))
  const mutations: string[] = []
  page.on('request', request => { if (request.url().includes('/api/v1/') && request.method() === 'POST') mutations.push(request.url()) })
  await page.goto(`/workspace/orders?payment=success&orderId=${orderID}&paymentId=${paymentID}`)
  const card = page.locator('.product-payment-status')
  await expect(card).toHaveAttribute('data-payment-state', 'cancelled')
  await expect(card.getByRole('link', { name: 'Review product', exact: true })).toHaveAttribute('href', `/market/assets/${productID}`)
  expect(mutations).toEqual([])
})
