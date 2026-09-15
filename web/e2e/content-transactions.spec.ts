import { expect, test } from '@playwright/test'

// Response fixtures exercise content rendering; payment execution is covered by
// the provider service tests and is intentionally not simulated by these tests.
const statuses = ['test_pending', 'test_paid', 'payment_pending', 'payment_paid', 'payment_failed', 'fulfilled', 'refund_requested', 'test_refunded', 'refunded', 'cancelled']
const assetId = '00000000-0000-4000-8000-000000000102'
const productId = '00000000-0000-4000-8000-000000000501'
const date = '2026-09-15T00:00:00Z'

for (const theme of ['light', 'dark']) for (const width of [390, 768, 1308, 1551]) {
  test(`transaction content ${theme} ${width}: nonempty orders and purchases`, async ({ page }) => {
    await page.setViewportSize({ width, height: 901 })
    await page.addInitScript(theme => localStorage.setItem('hcai-theme', theme), theme)
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
    const orders = statuses.map((status, index) => ({
      id: `00000000-0000-4000-8000-${String(900 + index).padStart(12, '0')}`, productId,
      productTitle: 'A detailed licensed resource '.repeat(12), amountCents: 12345678, currency: 'USD', status,
      licenseCode: 'hcai-commercial-v1', licenseName: 'Commercial license', licenseVersion: '1.0',
      licenseTerms: 'You may use this resource commercially with attribution. '.repeat(12), refundWindowDays: 7,
      paymentMode: 'stripe', realCharge: false, createdAt: date,
      events: [{ toStatus: status, createdAt: date, reason: 'A recorded order state.' }],
    }))
    await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: orders } }))
    await page.goto('/workspace/orders')
    await expect(page.locator('.order-row')).toHaveCount(statuses.length)
    await expect(page.locator('.refund-form')).toHaveCount(1)
    await expect(page.locator('.order-list')).not.toContainText('marketplace.orderStatus.')
    const overflow = await page.locator('.order-list').evaluate(root => [root, ...root.querySelectorAll('article, header, dl, section, form')].filter(n => n.scrollWidth > n.clientWidth + 2).map(n => n.className))
    expect(overflow).toEqual([])

    const original = await (await page.request.get(`/api/v1/assets/${assetId}`)).json()
    const purchased = { ...original, sourceType: 'purchase', title: 'Purchased resource with a descriptive title', licenseCode: 'hcai-commercial-v1', provenance: { purchase: { orderId: orders[5]!.id, productId, productTitle: 'Licensed resource', sellerId: '00000000-0000-4000-8000-000000000001', sellerName: 'Example Creator', sellerHandle: 'example', licenseCode: 'hcai-commercial-v1', licenseName: 'Commercial license', orderStatus: 'fulfilled', grantedAt: date, paymentMode: 'stripe', realCharge: false } } }
    await page.route('**/api/v1/assets', route => route.fulfill({ json: { items: [purchased] } }))
    await page.route(`**/api/v1/assets/${assetId}`, route => route.fulfill({ json: purchased }))
    await page.goto('/workspace/purchases')
    await expect(page.locator('.asset-row')).toHaveCount(1)
    await expect(page.locator('.asset-row')).toContainText(purchased.title)
    await page.goto(`/workspace/assets/${assetId}`)
    await expect(page.getByRole('heading', { name: purchased.title, exact: true })).toBeVisible()
    const action = page.locator('.asset-content-actions').getByRole('link', { name: 'Use in Create', exact: true })
    await expect(action).toBeVisible()
    expect((await action.boundingBox())!.y).toBeLessThan(600)
    await expect(page.locator('.asset-detail-layout')).toContainText('Commercial license')
    const assetOverflow = await page.locator('.asset-detail-layout').evaluate(root => root.scrollWidth > root.clientWidth + 2)
    expect(assetOverflow).toBe(false)
    await page.locator('.asset-back').click()
    await expect(page).toHaveURL(/\/workspace\/purchases$/)
  })
}
