import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

for (const width of [390, 1308]) {
  test(`refund eligibility, Unicode reasons and pending purchase access at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 901 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    const assetID = '00000000-0000-4000-8000-000000000102'
    const productID = '00000000-0000-4000-8000-000000000501'
    const original = await (await page.request.get(`/api/v1/assets/${assetID}`)).json()
    const deadline = new Date(Date.now() + 86400000).toISOString()
    const base = {
      id: '00000000-0000-4000-8000-000000009001', productId: productID, productTitle: 'Licensed production resource',
      assetId: assetID, amountCents: 1900, currency: 'USD', status: 'fulfilled', licenseCode: 'hcai-commercial-standard-v1',
      licenseName: 'Commercial license', licenseVersion: '1', licenseTerms: 'Licensed derivative use permitted.',
      refundWindowDays: 7, refundDeadlineAt: deadline, canRequestRefund: true, paymentMode: 'stripe', realCharge: false,
      createdAt: new Date().toISOString(), events: [],
    }
    const orders = [base,
      { ...base, id: '00000000-0000-4000-8000-000000009002', canRequestRefund: false, refundUnavailableReason: 'window_expired' },
      { ...base, id: '00000000-0000-4000-8000-000000009003', canRequestRefund: false, refundUnavailableReason: 'provider_unavailable' },
      { ...base, id: '00000000-0000-4000-8000-000000009004', productTitle: 'Unverified historical payment', paymentMode: 'unverified', canRequestRefund: false, refundUnavailableReason: 'reconciliation_required' },
    ]
    let permissions = true
    let paymentMode = 'stripe'
    const purchased = () => ({ ...original, sourceType: 'purchase', title: 'Purchased refund reference', provenance: { purchase: {
      orderId: base.id, productId: productID, productTitle: base.productTitle, sellerId: paymentMode === 'unverified' ? undefined : '00000000-0000-4000-8000-000000000001',
      sellerName: paymentMode === 'unverified' ? undefined : 'Seller', sellerHandle: paymentMode === 'unverified' ? undefined : 'seller', licenseCode: base.licenseCode, licenseName: base.licenseName,
      orderStatus: permissions ? 'refund_requested' : 'refunded', grantedAt: base.createdAt, paymentMode, realCharge: false,
      canDownload: permissions, canReuse: permissions,
    } } })
    await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: orders } }))
    await page.route('**/api/v1/assets?*', route => route.fulfill({ json: { items: [purchased()], total: 1 } }))
    await page.route(`**/api/v1/assets/${assetID}`, route => route.fulfill({ json: purchased() }))
    let submitted = 0
    await page.route(`**/api/v1/orders/${base.id}/refund`, async route => {
      submitted++
      expect(route.request().postDataJSON().reason).toBe('😀'.repeat(500))
      await route.fulfill({ json: { ...base, status: 'refund_requested', canRequestRefund: false, refundUnavailableReason: 'order_state' } })
    })
    await page.goto('/workspace/orders')
    await expect(page.locator('.refund-form')).toHaveCount(1)
    await expect(page.getByText('The refund window for this order has ended.', { exact: true })).toBeVisible()
    await expect(page.getByText('Refund requests are unavailable for this payment provider. Please contact support.', { exact: true })).toBeVisible()
    await expect(page.getByText('Refund deadline', { exact: true })).toHaveCount(4)
    await expect(page.getByText('Payment evidence needs reconciliation', { exact: true }).first()).toBeVisible()
    const reason = page.locator('.refund-form textarea')
    const submit = page.locator('.refund-form button[type="submit"]')
    await reason.fill('😀'.repeat(9))
    await expect(submit).toBeDisabled()
    await reason.fill('界'.repeat(10))
    await expect(submit).toBeEnabled()
    await reason.fill('😀'.repeat(501))
    await expect(submit).toBeDisabled()
    await reason.fill('😀'.repeat(500))
    await expect(submit).toBeEnabled()
    await submit.click()
    await expect(page.locator('.refund-form')).toHaveCount(0)
    expect(submitted).toBe(1)
    await page.goto(`/workspace/assets/${assetID}`)
    await expect(page.locator('.asset-content-actions').getByRole('link', { name: 'Use in Create', exact: true })).toBeVisible()
    await expect(page.locator('a[download]')).toHaveCount(1)
    paymentMode = 'unverified'
    await page.reload()
    await expect(page.getByText(/Payment evidence needs reconciliation/).first()).toBeVisible()
    await expect(page.getByText('Original seller needs reconciliation', { exact: true })).toBeVisible()
    await expect(page.locator('a[download]')).toHaveCount(1)
    permissions = false
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Purchased refund reference', exact: true })).toBeVisible()
    await expect(page.locator('.asset-content-actions').getByRole('link', { name: 'Use in Create', exact: true })).toHaveCount(0)
    await expect(page.locator('a[download]')).toHaveCount(0)
  })
}
