import { expect, test, type Page } from '@playwright/test'

const orderID = '00000000-0000-4000-8000-000000009701'
const paymentID = '00000000-0000-4000-8000-000000009702'
const productID = '00000000-0000-4000-8000-000000009703'
const assetID = '00000000-0000-4000-8000-000000009704'
const buyerID = '00000000-0000-4000-8000-000000009705'
const baseOrder = {
  id: orderID, productId: productID, productTitle: 'Frozen purchased resource',
  paymentId: paymentID, paymentVersion: 4, assetId: assetID,
  status: 'fulfilled', paymentStatus: 'paid', amountCents: 1900, currency: 'USD',
  licenseCode: 'hcai-commercial-standard-v1', licenseName: 'Commercial license',
  licenseVersion: '1', licenseTerms: 'Original licensed terms.', refundWindowDays: 7,
  canRequestRefund: false, paymentMode: 'stripe', realCharge: false,
  createdAt: '2026-09-20T10:00:00Z', events: [],
}

async function mockShell(page: Page, locale = 'en-US') {
  const unexpected: string[] = []
  await page.addInitScript(value => localStorage.setItem('hcai-locale', value), locale)
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/v1/auth/session') return route.fulfill({ json: { user: {
      id: buyerID, email: 'buyer@fixture.test', handle: 'buyer', displayName: 'Buyer',
      role: 'member', status: 'active', locale, timezone: 'UTC', permissions: [],
    } } })
    if (path === '/api/v1/site-config') return route.fulfill({ json: {
      siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png',
      footerText: { enUS: '', zhCN: '' },
      policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])),
    } })
    if (path === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    unexpected.push(`${route.request().method()} ${path}`)
    return route.fulfill({ status: 500, json: { error: { code: 'unexpected_response', message: 'Unmocked API request', retryable: false } } })
  })
  return unexpected
}

for (const locale of ['en-US', 'zh-CN']) {
  for (const width of [320, 1308]) {
    test(`ordinary orders reach their own delivery without the public product at ${width}px in ${locale}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 })
      const unexpected = await mockShell(page, locale)
      const badStates = ['payment_pending', 'payment_paid', 'cancelled', 'payment_failed', 'refunded', 'test_refunded']
      const unavailable = badStates.map((status, index) => ({ ...baseOrder, id: `${orderID.slice(0, -3)}${800 + index}`, productTitle: `Unavailable ${status}`, status }))
      await page.route('**/api/v1/orders?*', route => {
        const more = new URL(route.request().url()).searchParams.has('cursor')
        return route.fulfill({ json: more
          ? { items: [{ ...baseOrder, id: '00000000-0000-4000-8000-000000009706', productTitle: 'Ordinary refund in progress', status: 'refund_requested', paymentStatus: 'refund_pending' }] }
          : { items: [baseOrder, ...unavailable,
            { ...baseOrder, id: 'missing-delivery', productTitle: 'No delivery yet', assetId: undefined },
            { ...baseOrder, id: 'invalid-delivery', productTitle: 'Invalid delivery identifier', assetId: '../../admin' }], nextCursor: 'next-order' },
        })
      })
      let assetReads = 0
      let orderReads = 0
      await page.route(`**/api/v1/orders/${orderID}`, route => {
        orderReads++
        return route.fulfill({ json: baseOrder })
      })
      await page.route(`**/api/v1/assets/${assetID}`, route => {
        assetReads++
        return route.fulfill({ json: {
          id: assetID, ownerId: buyerID, kind: 'image', title: 'Independent purchased copy', sourceType: 'purchase',
          mediaUrl: '/brand/logo.png', mimeType: 'image/png', width: 128, height: 128, scanStatus: 'clean',
          licenseCode: baseOrder.licenseCode, version: 1, currentVersion: 1, versions: [],
          createdAt: baseOrder.createdAt,
          provenance: { purchase: {
            orderId: orderID, productId: productID, productTitle: baseOrder.productTitle,
            licenseName: baseOrder.licenseName, licenseCode: baseOrder.licenseCode,
            orderStatus: 'fulfilled', paymentMode: 'stripe', realCharge: false,
            grantedAt: baseOrder.createdAt, canDownload: false, canReuse: false,
          } },
        } })
      })
      await page.goto('/workspace/orders')
      const links = page.locator('.order-row .product-order-delivery-link')
      await expect(links).toHaveCount(1)
      await expect(links.first()).toHaveAttribute('href', `/workspace/assets/${assetID}`)
      await expect(links.first()).toHaveAttribute('data-variant', 'primary')
      await expect(links.first()).toHaveText(locale === 'zh-CN' ? '查看已购内容' : 'View purchased content')
      await page.getByRole('button', { name: locale === 'zh-CN' ? '加载更多' : 'Load more', exact: true }).click()
      await expect(links).toHaveCount(2)
      const pendingRow = page.locator('.order-row').filter({ has: page.getByRole('heading', { name: 'Ordinary refund in progress', exact: true }) })
      await expect(pendingRow.locator('.product-order-delivery-link')).toHaveAttribute('href', `/workspace/assets/${assetID}`)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      const firstRow = page.locator('.order-row').first()
      expect(await firstRow.locator('.task-status').evaluate(element => element.getBoundingClientRect().height)).toBeLessThanOrEqual(36)
      expect(await firstRow.locator('.order-card-header').evaluate(element => element.getBoundingClientRect().height)).toBeLessThan(260)
      await firstRow.screenshot({ path: testInfo.outputPath('orders-with-delivery.png') })
      await links.first().click()
      await expect(page).toHaveURL(new RegExp(`/workspace/assets/${assetID}$`))
      await expect(page.getByRole('heading', { name: 'Independent purchased copy', exact: true })).toBeVisible()
      expect(assetReads).toBe(1)
      await expect(page.locator('a[download]')).toHaveCount(0)
      await expect(page.locator('.asset-content-actions a[href^="/create/"]')).toHaveCount(0)
      const originalOrder = page.locator('.asset-detail-card').getByRole('link', { name: locale === 'zh-CN' ? '查看订单证据' : 'View order evidence', exact: true })
      await expect(originalOrder).toHaveAttribute('href', `/workspace/orders?orderId=${orderID}`)
      await originalOrder.click()
      await expect(page.locator('.order-row').first()).toContainText(baseOrder.productTitle)
      await expect(page.locator('.order-row').filter({ has: page.getByRole('heading', { name: baseOrder.productTitle, exact: true }) })).toHaveCount(1)
      expect(orderReads).toBe(1)
      expect(unexpected).toEqual([])
    })
  }
}

test('an order linked by a purchase is located outside the first page and preserves pagination', async ({ page }) => {
  const unexpected = await mockShell(page)
  const unrelated = { ...baseOrder, id: '00000000-0000-4000-8000-000000009710', productTitle: 'Another purchase' }
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: new URL(route.request().url()).searchParams.has('cursor')
    ? { items: [baseOrder] }
    : { items: [unrelated], nextCursor: unrelated.id } }))
  await page.route(`**/api/v1/orders/${orderID}`, route => route.fulfill({ json: baseOrder }))
  await page.goto(`/workspace/orders?orderId=${orderID}`)
  await expect(page.locator('.order-row')).toHaveCount(2)
  await expect(page.locator('.order-row').first()).toContainText(baseOrder.productTitle)
  await expect(page.locator('.product-payment-status')).toHaveCount(0)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)
  await expect(page.locator('.order-row')).toHaveCount(2)
  expect(unexpected).toEqual([])
})

for (const failure of ['forbidden', 'mismatched_order', 'invalid_id']) {
  test(`a linked order that is ${failure} cannot silently show another order`, async ({ page }) => {
    const unexpected = await mockShell(page)
    await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [baseOrder] } }))
    await page.route(`**/api/v1/orders/${orderID}`, route => failure === 'forbidden'
      ? route.fulfill({ status: 403, json: { error: { code: 'forbidden', message: 'Not this buyer', retryable: false } } })
      : route.fulfill({ json: { ...baseOrder, id: productID } }))
    await page.goto(`/workspace/orders?orderId=${failure === 'invalid_id' ? 'not-an-order' : orderID}`)
    await expect(page.locator('.orders-workspace [role="alert"]')).toBeVisible()
    await expect(page.locator('.order-row')).toHaveCount(0)
    expect(unexpected).toEqual([])
  })
}

test('refund confirmation removes delivery entry from both payment status and the ordinary order', async ({ page }) => {
  const unexpected = await mockShell(page)
  await page.clock.install()
  let refunded = false
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
  await page.route(`**/api/v1/orders/${orderID}`, route => route.fulfill({ json: {
    ...baseOrder, status: refunded ? 'refunded' : 'refund_requested', paymentStatus: refunded ? 'refunded' : 'refund_pending',
  } }))
  await page.goto(`/workspace/orders?payment=success&orderId=${orderID}&paymentId=${paymentID}`)
  await expect(page.locator('.product-payment-status .product-order-delivery-link')).toBeVisible()
  await expect(page.locator('.order-row .product-order-delivery-link')).toBeVisible()
  refunded = true
  await page.clock.runFor(2600)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refunded')
  await expect(page.locator('.product-order-delivery-link')).toHaveCount(0)
  expect(unexpected).toEqual([])
})

for (const status of [401, 403, 404, 422, 'mismatched_payment'] as const) {
  test(`payment status discards cached delivery context after ${status}`, async ({ page }) => {
    const unexpected = await mockShell(page)
    await page.clock.install()
    await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: [] } }))
    let unavailable = false
    await page.route(`**/api/v1/orders/${orderID}`, route => unavailable
      ? typeof status === 'number'
        ? route.fulfill({ status, json: { error: { code: 'order_not_found', message: 'Unavailable', retryable: false } } })
        : route.fulfill({ json: { ...baseOrder, paymentId: productID } })
      : route.fulfill({ json: { ...baseOrder, status: 'refund_requested', paymentStatus: 'refund_pending' } }))
    await page.goto(`/workspace/orders?payment=success&orderId=${orderID}&paymentId=${paymentID}`)
    const card = page.locator('.product-payment-status')
    await expect(card.locator('.product-order-delivery-link')).toBeVisible()
    unavailable = true
    await page.clock.runFor(2600)
    await expect(card).toHaveAttribute('data-payment-state', 'unavailable')
    await expect(card.locator('.product-order-delivery-link')).toHaveCount(0)
    await expect(card).not.toContainText(baseOrder.productTitle)
    expect(unexpected).toEqual([])
  })
}
