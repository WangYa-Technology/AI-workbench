import { expect, test, type Page, type Route } from '@playwright/test'

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
  canRequestRefund: true, paymentMode: 'stripe', realCharge: false,
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


async function setup(page: Page, locale = 'en-US') {
  const unexpected = await mockShell(page, locale)
  await page.route('**/api/v1/orders?*', r => r.fulfill({ json: { items: [baseOrder] } }))
  await page.route('**/api/v1/orders/' + orderID, r => r.fulfill({ json: baseOrder }))
  return unexpected
}
async function refund(page: Page) {
  await page.locator('.order-more-trigger').click()
  await page.locator('.order-refund-toggle').click()
  await page.locator('.refund-form textarea').fill('The delivered resource is unsuitable for the agreed project.')
  await page.locator('.refund-form button[type="submit"]').click()
}
const returnURL = '/workspace/orders?payment=success&orderId=' + orderID + '&paymentId=' + paymentID

for (const locale of ['en-US', 'zh-CN']) {
  const zh = locale === 'zh-CN'
  test('read failure can retry without a false empty state: ' + locale, async ({ page }) => {
    const unexpected = await setup(page, locale)
    let failed = true
    await page.route('**/api/v1/orders?*', r => failed
      ? r.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'Fixture unavailable', retryable: true } } })
      : r.fulfill({ json: { items: [] } }))
    await page.goto('/workspace/orders')
    await expect(page.locator('.task-feedback.error')).toBeVisible()
    await expect(page.locator('.task-market-empty')).toHaveCount(0)
    await expect(page.locator('.order-loaded-count')).toHaveText('—')
    failed = false
    await page.getByRole('button', { name: zh ? '重试' : 'Try again', exact: true }).click()
    await expect(page.locator('.task-feedback.error')).toHaveCount(0)
    await expect(page.locator('.task-market-empty')).toBeVisible()
    expect(unexpected).toEqual([])
  })
  for (const state of ['refunded', 'fulfilled', 'refund_failed', 'test_refunded'] as const) {
    test('refund feedback matches server result ' + state + ': ' + locale, async ({ page }) => {
      const unexpected = await setup(page, locale)
      const source = state === 'test_refunded' ? { ...baseOrder, paymentId: undefined, paymentVersion: undefined, paymentMode: 'test' } : baseOrder
      await page.route('**/api/v1/orders?*', r => r.fulfill({ json: { items: [source] } }))
      await page.route('**/api/v1/orders/' + orderID + '/refund', r => r.fulfill({ json: {
        ...source, status: state === 'refund_failed' ? 'fulfilled' : state,
        paymentStatus: state === 'refund_failed' ? 'refund_failed' : state === 'refunded' ? 'refunded' : 'paid',
        canRequestRefund: false, paymentVersion: source.paymentId ? 5 : undefined,
      } }))
      await page.goto('/workspace/orders'); await refund(page)
      const expected = state === 'refunded' ? (zh ? '支付网关已确认退款' : 'Payment gateway refund confirmed')
        : state === 'refund_failed' ? (zh ? '退款未完成' : 'The refund did not complete')
        : state === 'fulfilled' ? (zh ? '尚未确认退款完成' : 'Refund completion is not confirmed')
        : (zh ? '历史' : 'Historical Local Test refund recorded')
      await expect(page.locator('.orders-workspace .task-feedback')).toContainText(expected)
      await expect(page.locator('.task-feedback.success')).toHaveCount(['fulfilled', 'refund_failed'].includes(state) ? 0 : 1)
      expect(unexpected).toEqual([])
    })
  }
  test('loaded counts and failed pagination keep records: ' + locale, async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 900 })
    const unexpected = await setup(page, locale)
    let failMore = true
    await page.route('**/api/v1/orders?*', r => {
      const more = new URL(r.request().url()).searchParams.has('cursor')
      if (more && failMore) return r.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'Fixture unavailable' } } })
      return r.fulfill({ json: more ? { items: [{ ...baseOrder, id: '00000000-0000-4000-8000-000000009799' }] }
        : { items: [{ ...baseOrder, status: 'refund_requested', canRequestRefund: false }], nextCursor: 'more' } })
    })
    await page.goto('/workspace/orders')
    const stats = page.locator('.order-loaded-count')
    await expect(stats).toHaveText(zh ? '1 笔订单' : '1 order')
    const help = page.getByRole('button', { name: zh ? '订单数量说明' : 'About the order count', exact: true })
    const note = page.getByRole('tooltip')
    await expect(note).toBeHidden()
    await help.click()
    await expect(note).toContainText(zh ? '仅覆盖已加载订单' : 'loaded orders only')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await help.press('Escape')
    await expect(note).toBeHidden()
    await page.getByRole('heading', { name: zh ? '订单' : 'Orders', exact: true }).click()
    await help.hover()
    await expect(note).toBeVisible()
    await page.mouse.move(0, 0)
    await expect(note).toBeHidden()
    await expect(page.locator('.product-order-delivery-link')).toBeVisible()
    const more = page.getByRole('button', { name: zh ? '加载更多' : 'Load more', exact: true })
    await more.click()
    await expect(page.locator('.task-feedback.error')).toBeVisible()
    await expect(page.locator('.order-row')).toHaveCount(1)
    failMore = false; await more.click()
    await expect(page.locator('.task-feedback.error')).toHaveCount(0)
    await expect(stats).toHaveText(zh ? '2 笔订单' : '2 orders')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    expect(unexpected).toEqual([])
  })
}

test('refund starts polling again and both cards follow terminal state', async ({ page }) => {
  const unexpected = await setup(page)
  await page.clock.install()
  let refunded = false
  await page.route('**/api/v1/orders/' + orderID, r => r.fulfill({ json: refunded
    ? { ...baseOrder, status: 'refunded', paymentStatus: 'refunded', paymentVersion: 6, canRequestRefund: false } : baseOrder }))
  await page.route('**/api/v1/orders/' + orderID + '/refund', r => r.fulfill({ json: { ...baseOrder, status: 'refund_requested', paymentStatus: 'refund_pending', paymentVersion: 5, canRequestRefund: false } }))
  await page.goto(returnURL)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'fulfilled')
  await refund(page)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refundPending')
  refunded = true; await page.clock.runFor(2600)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refunded')
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'refunded')
  await expect(page.locator('.task-feedback.success')).toContainText('Payment gateway refund confirmed')
  await expect(page.locator('.product-order-delivery-link')).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('late refund response cannot undo a newer confirmed refund', async ({ page }) => {
  const unexpected = await setup(page)
  let pending: Route | undefined
  await page.route('**/api/v1/orders/' + orderID + '/refund', r => { pending = r })
  await page.goto(returnURL)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'fulfilled')
  await refund(page); await expect.poll(() => Boolean(pending)).toBe(true)
  await page.route('**/api/v1/orders/' + orderID, r => r.fulfill({ json: { ...baseOrder, status: 'refunded', paymentVersion: 6, canRequestRefund: false } }))
  await page.getByRole('button', { name: 'Check again', exact: true }).click()
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'refunded')
  await pending!.fulfill({ json: { ...baseOrder, status: 'refund_requested', paymentVersion: 5, canRequestRefund: false } })
  await expect(page.locator('.task-feedback.success')).toContainText('Payment gateway refund confirmed')
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'refunded')
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refunded')
  expect(unexpected).toEqual([])
})

test('refund response with a different payment is rejected', async ({ page }) => {
  const unexpected = await setup(page)
  await page.route('**/api/v1/orders/' + orderID + '/refund', r => r.fulfill({ json: { ...baseOrder, paymentId: productID, status: 'refunded', paymentVersion: 8 } }))
  await page.goto('/workspace/orders'); await refund(page)
  await expect(page.locator('.task-feedback.error')).toBeVisible()
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'fulfilled')
  await expect(page.locator('.task-feedback.success')).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('ordinary orders can query refund completion without returning through checkout', async ({ page }) => {
  const unexpected = await setup(page)
  await page.route('**/api/v1/orders?*', r => r.fulfill({ json: { items: [{ ...baseOrder, status: 'refund_requested', paymentVersion: 5, canRequestRefund: false }] } }))
  await page.route('**/api/v1/orders/' + orderID, r => r.fulfill({ json: { ...baseOrder, status: 'refunded', paymentVersion: 6, canRequestRefund: false } }))
  await page.goto('/workspace/orders')
  await page.getByRole('button', { name: 'Refresh order status', exact: true }).click()
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'refunded')
  await expect(page.locator('.product-order-delivery-link')).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('late checkout closure cannot overwrite a newer paid order', async ({ page }) => {
  const unexpected = await setup(page)
  const pendingOrder = { ...baseOrder, status: 'payment_pending', paymentStatus: 'checkout_pending', canRequestRefund: false, canCloseCheckout: true }
  await page.route('**/api/v1/orders?*', r => r.fulfill({ json: { items: [pendingOrder] } }))
  await page.route('**/api/v1/orders/' + orderID, r => r.fulfill({ json: pendingOrder }))
  let command: Route | undefined
  await page.route('**/api/v1/orders/' + orderID + '/close-checkout', r => { command = r })
  await page.goto(returnURL)
  await page.locator('.product-checkout-closure').getByRole('button', { name: 'Close order', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Close order', exact: true }).click()
  await expect.poll(() => Boolean(command)).toBe(true)
  await page.route('**/api/v1/orders/' + orderID, r => r.fulfill({ json: { ...baseOrder, paymentVersion: 6 } }))
  await page.getByRole('button', { name: 'Check again', exact: true }).click()
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'fulfilled')
  await command!.fulfill({ json: { ...pendingOrder, status: 'cancelled', paymentVersion: 5, canCloseCheckout: false, checkoutClosedBeforePayment: true } })
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  await expect(page.locator('.product-checkout-closure')).toHaveCount(0)
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'fulfilled')
  expect(unexpected).toEqual([])
})

test('a stale payment query cannot replace a newer refund command', async ({ page }) => {
  const unexpected = await setup(page)
  await page.goto(returnURL)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'fulfilled')
  let query: Route | undefined
  await page.route('**/api/v1/orders/' + orderID, r => { query = r })
  await page.getByRole('button', { name: 'Check again', exact: true }).click()
  await expect.poll(() => Boolean(query)).toBe(true)
  await page.route('**/api/v1/orders/' + orderID + '/refund', r => r.fulfill({ json: { ...baseOrder, status: 'refunded', paymentVersion: 6, canRequestRefund: false } }))
  await refund(page)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refunded')
  await query!.fulfill({ json: baseOrder })
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'refunded')
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refunded')
  await expect(page.locator('.product-order-delivery-link')).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('refund polling stops on failure and follows page lifecycle', async ({ page }) => {
  const unexpected = await setup(page)
  await page.clock.install()
  let failure = false
  let reads = 0
  await page.route('**/api/v1/orders/' + orderID, r => {
    reads++
    return r.fulfill({ json: failure ? { ...baseOrder, paymentVersion: 6, paymentStatus: 'refund_failed' } : baseOrder })
  })
  await page.route('**/api/v1/orders/' + orderID + '/refund', r => r.fulfill({ json: { ...baseOrder, status: 'refund_requested', paymentVersion: 5, canRequestRefund: false } }))
  await page.goto(returnURL)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'fulfilled')
  await refund(page)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refundPending')
  failure = true; await page.clock.runFor(2600)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refundFailed')
  await expect(page.locator('.task-feedback.error')).toContainText('The refund did not complete')
  const stopped = reads
  await page.clock.runFor(10000)
  expect(reads).toBe(stopped)
  expect(unexpected).toEqual([])
})

test('pending refund polling is bounded and stops after leaving its route', async ({ page }) => {
  const unexpected = await setup(page)
  await page.clock.install()
  let pending = false
  let reads = 0
  const pendingOrder = { ...baseOrder, status: 'refund_requested', paymentVersion: 5, canRequestRefund: false }
  await page.route('**/api/v1/orders/' + orderID, r => {
    reads++
    return r.fulfill({ json: pending ? pendingOrder : baseOrder })
  })
  await page.route('**/api/v1/orders/' + orderID + '/refund', r => { pending = true; return r.fulfill({ json: pendingOrder }) })
  await page.goto(returnURL)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'fulfilled')
  await refund(page)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refundPending')
  await page.clock.runFor(61000)
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'delayed')
  const stopped = reads
  await page.clock.runFor(10000)
  expect(reads).toBe(stopped)
  await page.getByRole('button', { name: 'Check again', exact: true }).click()
  await expect(page.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'refundPending')
  await page.locator('#primary-navigation a[href="/workspace/orders"]').click()
  await expect(page.locator('.product-payment-status')).toHaveCount(0)
  const left = reads
  await page.clock.runFor(10000)
  expect(reads).toBe(left)
  expect(unexpected).toEqual([])
})

for (const locale of ['en-US', 'zh-CN']) {
  test('loaded filters, history and order reference are usable: ' + locale, async ({ page, context }) => {
    const zh = locale === 'zh-CN'
    const unexpected = await setup(page, locale)
    await page.setViewportSize({ width: 320, height: 900 })
    const second = { ...baseOrder, id: '00000000-0000-4000-8000-000000009799', productTitle: 'Older refunded order', status: 'refunded', canRequestRefund: false, createdAt: '2026-09-18T10:00:00Z' }
    await page.route('**/api/v1/orders?*', r => r.fulfill({ json: { items: [{ ...baseOrder, events: [{ toStatus: 'fulfilled', reason: '', createdAt: baseOrder.createdAt }] }, second] } }))
    await page.goto('/workspace/orders')
    await page.locator('#mobile-search-toggle').click()
    const search = page.getByRole('searchbox', { name: zh ? '搜索已加载订单的标题或订单号' : 'Search loaded orders by title or reference' })
    await search.fill('missing')
    await search.press('Enter')
    await expect(page.locator('.order-row')).toHaveCount(0)
    await expect(page.locator('.order-filter-empty')).toBeVisible()
    await search.fill(second.id)
    await search.press('Enter')
    await expect(page.locator('.order-row')).toHaveCount(1)
    await expect(page.locator('.order-row')).toContainText(second.productTitle)
    await search.press('Escape')
    await page.locator('.catalog-keyword').click()
    const status = page.getByRole('combobox', { name: zh ? '订单状态' : 'Order status', exact: true })
    await status.click()
    await page.getByRole('option', { name: zh ? '已退款' : 'Refunded', exact: true }).click()
    await expect(page.locator('.order-row')).toHaveCount(1)
    await page.locator('.order-catalog-toolbar').getByRole('button', { name: zh ? '清除筛选' : 'Clear filters', exact: true }).click()
    await page.getByLabel(zh ? '下单起始日（本地日期）' : 'Placed from (local date)').fill('2026-09-19')
    await expect(page.locator('.order-row')).toHaveCount(1)
    await expect(page.locator('.order-row')).toContainText(baseOrder.productTitle)
    await page.locator('.order-details-toggle').click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await page.getByRole('tab', { name: zh ? '订单记录' : 'Order history', exact: true }).click()
    await expect(page.locator('.order-history-panel li')).toHaveCount(1)
    await expect(page.locator('.order-history-panel')).toBeVisible()
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await page.getByRole('button', { name: zh ? '复制订单号' : 'Copy order reference', exact: true }).click()
    await expect(page.locator('.order-support-actions [role="status"]')).toBeVisible()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(orderID)
    await expect(page.locator('.order-support-actions a[href="/support?orderId=' + orderID + '"]')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    expect(unexpected).toEqual([])
  })
}

for (const locale of ['en-US', 'zh-CN']) {
  test('order support context requires explicit submission and survives rejection: ' + locale, async ({ page }) => {
    const zh = locale === 'zh-CN'
    const unexpected = await setup(page, locale)
    const submissions: Record<string, unknown>[] = []
    let reject = true
    const caseID = '00000000-0000-4000-8000-000000009780'
    const created = { id: caseID, category: 'task_or_order', subject: 'Help with my order', status: 'open', version: 1,
      relatedResourceType: 'order', relatedResourceId: orderID, requesterHandle: 'buyer', messages: [],
      createdAt: baseOrder.createdAt, updatedAt: baseOrder.createdAt }
    await page.route('**/api/v1/support/cases**', r => {
      if (r.request().method() === 'POST') {
        submissions.push(r.request().postDataJSON())
        return reject ? r.fulfill({ status: 422, json: { error: { code: 'validation_failed', message: 'The related order is unavailable.' } } })
          : r.fulfill({ status: 201, json: created })
      }
      return r.fulfill({ json: new URL(r.request().url()).pathname.endsWith(caseID) ? created : { items: [] } })
    })
    await page.goto(returnURL)
    await expect(page.locator('.product-payment-status a[href="/support?orderId=' + orderID + '"]')).toBeVisible()
    await page.locator('.order-details-toggle').click()
    await page.locator('.order-support-actions a[href="/support?orderId=' + orderID + '"]').click()
    const form = page.locator('.support-form')
    await expect(form).toBeVisible()
    await expect(form.getByRole('combobox', { name: zh ? '工单类别' : 'Case category', exact: true })).toContainText(zh ? '任务或订单' : 'Task or order')
    await expect(form.getByRole('combobox', { name: zh ? '关联资源类型' : 'Related resource type', exact: true })).toContainText(zh ? '订单' : 'Order')
    await expect(form.getByRole('textbox').nth(2)).toHaveValue(orderID)
    expect(submissions).toEqual([])
    await form.getByRole('textbox').nth(0).fill(created.subject)
    await form.getByRole('textbox').nth(1).fill('Please check the delivery status of this order.')
    await form.getByRole('button', { name: zh ? '提交工单' : 'Submit case', exact: true }).click()
    await expect(page.locator('.task-feedback.error')).toBeVisible()
    await expect(form.getByRole('textbox').nth(2)).toHaveValue(orderID)
    expect(submissions).toHaveLength(1)
    expect(submissions[0]).toMatchObject({ category: 'task_or_order', relatedResourceType: 'order', relatedResourceId: orderID, locale })
    reject = false
    await form.getByRole('button', { name: zh ? '提交工单' : 'Submit case', exact: true }).click()
    await expect(page).toHaveURL(new RegExp('/support/' + caseID + '$'))
    await expect(page.locator('.support-evidence')).toContainText(orderID)
    expect(submissions).toHaveLength(2)
    expect(unexpected).toEqual([])
  })
}

for (const query of ['orderId=invalid', 'orderId=' + orderID + '&orderId=' + paymentID]) {
  test('support ignores invalid or ambiguous order context: ' + query, async ({ page }) => {
    const unexpected = await mockShell(page)
    await page.route('**/api/v1/support/cases?*', r => r.fulfill({ json: { items: [] } }))
    await page.goto('/support?' + query)
    await expect(page.locator('.support-welcome')).toBeVisible()
    await page.getByRole('button', { name: 'New case', exact: true }).first().click()
    await expect(page.locator('.support-form').getByRole('textbox').nth(2)).toBeEmpty()
    expect(unexpected).toEqual([])
  })
}

test('cancelling order support clears context for a new case', async ({ page }) => {
  const unexpected = await mockShell(page)
  await page.route('**/api/v1/support/cases?*', r => r.fulfill({ json: { items: [] } }))
  await page.goto('/support?orderId=' + orderID)
  await expect(page.locator('.support-form').getByRole('textbox').nth(2)).toHaveValue(orderID)
  await page.getByRole('button', { name: 'Back to support', exact: true }).click()
  await expect(page).toHaveURL(/\/support$/)
  await page.getByRole('button', { name: 'New case', exact: true }).first().click()
  await expect(page.locator('.support-form').getByRole('textbox').nth(2)).toBeEmpty()
  expect(unexpected).toEqual([])
})

test('catalog filters retain loaded pages and restore via history without refetching', async ({ page }) => {
  await setup(page)
  let reads = 0
  const second = { ...baseOrder, id: '00000000-0000-4000-8000-000000009799', productTitle: 'Earlier resource', status: 'refunded', canRequestRefund: false }
  await page.route('**/api/v1/orders?*', route => {
    reads++
    return route.fulfill({ json: new URL(route.request().url()).searchParams.has('cursor') ? { items: [second] } : { items: [baseOrder], nextCursor: 'more' } })
  })
  await page.goto('/workspace/orders')
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.locator('.order-row')).toHaveCount(2)
  const search = page.getByRole('searchbox', { name: 'Search loaded orders by title or reference', exact: true })
  await search.fill('Earlier')
  await search.press('Enter')
  await expect(page.locator('.order-row')).toHaveCount(1)
  await expect(page.locator('.order-row')).toContainText(second.productTitle)
  await page.getByRole('combobox', { name: 'Order status', exact: true }).click()
  await page.getByRole('option', { name: 'Fulfilled', exact: true }).click()
  await expect(page.locator('.order-filter-empty')).toBeVisible()
  await page.goBack()
  await expect(page.locator('.order-row')).toHaveCount(1)
  await page.locator('.order-catalog-toolbar').getByRole('button', { name: 'Clear filters', exact: true }).click()
  await expect(page.locator('.order-row')).toHaveCount(2)
  expect(reads).toBe(2)
  await expect(page.locator('.page-hero-stats, main [role=tablist], .workspace-switcher')).toHaveCount(0)
  await search.fill('Frozen')
  await search.press('Enter')
  await page.reload()
  await expect(search).toHaveValue('Frozen')
  await expect(page.locator('.order-row')).toHaveCount(1)
})

test('changing a loaded-order filter cannot discard an in-flight refund result', async ({ page }) => {
  await setup(page)
  let pending: Route | undefined
  await page.route('**/api/v1/orders/' + orderID + '/refund', route => { pending = route })
  await page.goto('/workspace/orders')
  await refund(page)
  await expect.poll(() => Boolean(pending)).toBe(true)
  const search = page.getByRole('searchbox', { name: 'Search loaded orders by title or reference', exact: true })
  await search.fill('Frozen')
  await search.press('Enter')
  await expect(page).toHaveURL(/q=Frozen/)
  await pending!.fulfill({ json: { ...baseOrder, status: 'refunded', paymentStatus: 'refunded', paymentVersion: 5, canRequestRefund: false } })
  await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'refunded')
  await expect(page.locator('.orders-workspace .task-feedback.success')).toContainText('Payment gateway refund confirmed')
  await expect(page.locator('.product-order-delivery-link, .refund-form')).toHaveCount(0)
})

test('orders keep a compact sticky toolbar and usable details while scrolling', async ({ page }, info) => {
  await setup(page)
  await page.setViewportSize({ width: 1289, height: 891 })
  await page.route('**/api/v1/orders?*', route => route.fulfill({ json: { items: Array.from({ length: 12 }, (_, i) => ({ ...baseOrder, id: `00000000-0000-4000-8000-${String(9701 + i).padStart(12, '0')}` })) } }))
  await page.goto('/workspace/orders')
  await expect(page.locator('.order-row')).toHaveCount(12)
  await page.locator('#main-content').evaluate(el => { el.scrollTop = 500 })
  const toolbar = page.locator('.catalog-toolbar')
  await expect(toolbar).toHaveClass(/is-stuck/)
  const rect = await toolbar.boundingBox()
  expect(rect!.height).toBeLessThanOrEqual(60)
  await page.getByRole('combobox', { name: 'Order status', exact: true }).click()
  await expect(page.getByRole('option', { name: 'Refunded', exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await page.locator('#main-content').evaluate(el => { el.scrollTop = 0 })
  await expect(toolbar).not.toHaveClass(/is-stuck/)
  await page.locator('.order-details-toggle').first().click()
  await page.getByRole('tab', { name: 'License terms', exact: true }).click()
  await expect(page.locator('.order-license-panel').first()).toBeVisible()
  await expect(page.locator('.order-license-panel').first()).toContainText('Original licensed terms.')
  await info.attach('orders-desktop.png', { body: await page.screenshot(), contentType: 'image/png' })
})

for (const locale of ['en-US', 'zh-CN']) {
  test('order summary discloses evidence and refund actions on demand: ' + locale, async ({ page }, info) => {
    const zh = locale === 'zh-CN'
    await setup(page, locale)
    await page.addInitScript(theme => localStorage.setItem('hcai-theme', theme), zh ? 'light' : 'dark')
    await page.goto('/workspace/orders')
    const row = page.locator('.order-record')
    await expect(row.locator('h2')).toHaveText(baseOrder.productTitle)
    await expect(row.locator('.order-card-meta')).toContainText(baseOrder.licenseName)
    await expect(row.locator('.order-payment-badge')).toHaveText(zh ? '测试订单' : 'Test order')
    await expect(row.locator('.order-card-amount')).toContainText(zh ? '订单金额' : 'Order amount')
    await expect(row).not.toContainText(orderID)
    await expect(row).not.toContainText(baseOrder.licenseTerms)
    await expect(row.locator('.product-order-delivery-link')).toHaveAttribute('data-variant', 'secondary')
    await page.screenshot({ path: info.outputPath('order-summary-theme.png') })
    const before = await row.boundingBox()
    await row.locator('.order-details-toggle').click()
    const dialog = page.getByRole('dialog', { name: zh ? '订单详情' : 'Order details', exact: true })
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText(orderID)
    await expect(dialog.locator('.order-license-panel')).toBeHidden()
    expect((await row.boundingBox())!.height).toBe(before!.height)
    const close = dialog.getByRole('button', { name: zh ? '关闭' : 'Close', exact: true })
    await expect(close).toBeFocused()
    await close.press('Shift+Tab')
    await expect(dialog.getByRole('link')).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(close).toBeFocused()
    const transaction = dialog.getByRole('tab', { name: zh ? '交易信息' : 'Transaction details', exact: true })
    await transaction.press('ArrowRight')
    await expect(dialog.locator('.order-license-panel')).toBeVisible()
    await expect(dialog.locator('.order-license-panel')).toContainText(baseOrder.licenseTerms)
    await page.keyboard.press('ArrowRight')
    await expect(dialog.locator('.order-history-panel')).toBeVisible()
    await page.keyboard.press('Home')
    await expect(dialog.locator('.order-detail-facts')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    await expect(row.locator('.order-details-toggle')).toBeFocused()
    const more = row.locator('.order-more-trigger')
    await more.press('ArrowDown')
    await expect(row.getByRole('menuitem').first()).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(more).toBeFocused()
    await expect(row.getByRole('menu')).toBeHidden()
    await more.press('ArrowUp')
    await expect(row.locator('.order-refund-toggle')).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(row.locator('.refund-form textarea')).toBeFocused()
    await expect(row.getByRole('menu')).toBeHidden()
    await row.locator('.order-details-toggle').click()
    for (const width of [320, 768, 1280, 1600]) {
      await page.setViewportSize({ width, height: 900 })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      const bounds = await dialog.boundingBox()
      expect(bounds!.x).toBeGreaterThanOrEqual(0)
      expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
      for (const tab of await dialog.getByRole('tab').all()) {
        await tab.click()
        expect(await dialog.locator('.ui-dialog__body').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
      }
    }
    await page.screenshot({ path: info.outputPath('order-details-dialog.png') })
    await close.click()
    await expect(dialog).toBeHidden()
    await expect(row.locator('.order-details-toggle')).toBeFocused()
  })
}

test('order status determines emphasis and availability of direct actions', async ({ page }) => {
  await setup(page)
  const statuses = ['refunded', 'test_refunded', 'cancelled', 'payment_pending', 'payment_failed']
  await page.route('**/api/v1/orders?*', r => r.fulfill({ json: { items: statuses.map((status, i) => ({
    ...baseOrder, id: `00000000-0000-4000-8000-${String(9800 + i).padStart(12, '0')}`,
    status, canRequestRefund: false, paymentMode: 'unverified', realCharge: false,
  })) } }))
  await page.goto('/workspace/orders')
  for (const status of statuses) {
    const row = page.locator(`[data-order-status="${status}"]`)
    await expect(row.locator('.order-payment-badge')).toHaveText('Payment unverified')
    await expect(row.locator('.product-order-delivery-link')).toHaveCount(0)
    if (['refunded', 'test_refunded', 'cancelled'].includes(status)) {
      await expect(row.locator('.task-status')).toHaveAttribute('data-status', 'neutral')
      await expect(row.locator('.order-refresh')).toHaveCount(0)
    } else {
      await expect(row.locator('.order-refresh')).toBeVisible()
    }
  }
})
