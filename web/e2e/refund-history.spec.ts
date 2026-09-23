import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

const paymentID = '00000000-0000-4000-8000-000000009801'
const operationID = '00000000-0000-4000-8000-000000009802'
const olderID = '00000000-0000-4000-8000-000000009803'
const createdAt = '2026-09-19T10:00:00Z'
const payment = {
  id: paymentID, purpose: 'product', status: 'refunded', amountCents: 1900, currency: 'USD', liveMode: false,
  payerId: '00000000-0000-4000-8000-000000000002', payerEmail: 'buyer@example.test', payerHandle: 'buyer', payerDisplayName: 'Buyer',
  resourceId: '00000000-0000-4000-8000-000000000501', resourceTitle: 'Refund history product', targetPath: '/workspace/orders',
  attentionCode: 'refund_reconciliation_required', version: 12, createdAt, updatedAt: createdAt,
}
const attempt = { operationId: operationID, provider: 'stripe', providerRefundId: 're_historypending', amountCents: 1900, currency: 'USD', status: 'pending', reconciliationRequired: true, requestedAt: createdAt }

for (const width of [390, 1308]) {
  test(`refund history uses shared controls and queues only a query at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    await page.route(/\/api\/v1\/admin\/payments(?:\?|$)/, route => route.fulfill({ json: { items: [payment] } }))
    let completed = false
    let queryCount = 0
    const otherMutations: string[] = []
    page.on('request', request => {
      if (request.method() === 'POST' && request.url().includes('/payments/') && !request.url().endsWith('/refund-checks')) otherMutations.push(request.url())
    })
    await page.route(`**/admin/payments/${paymentID}/refund-history*`, route => {
      const cursor = new URL(route.request().url()).searchParams.get('cursor')
      return route.fulfill({ json: {
        items: cursor ? [{ ...attempt, operationId: olderID, status: 'failed', providerRefundId: 're_historyold' }] : [attempt],
        ...(cursor ? {} : { nextCursor: olderID }), canCheck: true, paymentVersion: 12,
        ...(completed ? { latestCheck: { id: olderID, status: 'completed', unresolvedCount: 1, createdAt, observedAt: createdAt, completedAt: createdAt,
          observations: [{ providerId: 're_historypending', providerPaymentId: 'pi_historyoriginal', amountCents: 1900, currency: 'USD', status: 'pending' }] } } : {}),
      } })
    })
    await page.route(`**/admin/payments/${paymentID}/refund-checks`, async route => {
      expect(route.request().method()).toBe('POST')
      expect(route.request().postDataJSON()).toEqual({ expectedVersion: 12 })
      queryCount++
      await route.fulfill({ json: { items: [attempt], canCheck: false, paymentVersion: 13, latestCheck: { id: olderID, status: 'requested', unresolvedCount: 0, createdAt, observations: [] } } })
    })
    await page.route(`**/admin/payments/${paymentID}/refund-checks?*`, route => route.fulfill({ json: { items: queryCount ? [{ id: olderID, origin: 'operator', status: completed ? 'completed' : 'requested', unresolvedCount: completed ? 1 : 0, requiresReview: completed, createdAt }] : [] } }))
    await page.route(`**/admin/payments/${paymentID}/refund-checks/${olderID}`, route => route.fulfill({ json: { id: olderID, origin: 'operator', status: 'completed', unresolvedCount: 1, createdAt, observedAt: createdAt, completedAt: createdAt,
      unresolvedProviderRefundIds: ['re_historypending'], observations: [{ providerId: 're_historypending', providerPaymentId: 'pi_historyoriginal', amountCents: 1900, currency: 'USD', status: 'pending' }] } }))
    await page.goto('/admin?tab=finance')
    await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'Refund history and checks', exact: true })
    await expect(drawer.getByText(operationID, { exact: true })).toBeVisible()
    await drawer.getByRole('button', { name: 'Load more', exact: true }).click()
    await expect(drawer.getByText(olderID, { exact: true })).toBeVisible()
    await drawer.getByRole('button', { name: 'Query payment provider', exact: true }).click()
    await expect(drawer.getByRole('button', { name: 'Query payment provider', exact: true })).toBeDisabled()
    await expect(drawer.getByText('Queued', { exact: false })).toBeVisible()
    expect(queryCount).toBe(1)
    completed = true
    await drawer.getByRole('button', { name: 'Refresh history', exact: true }).click()
    await drawer.getByRole('button', { name: /Query complete/ }).click()
    await expect(drawer.getByText('Follow-up items when checked: 1', { exact: true })).toBeVisible()
    await expect(drawer.getByText('Provider refund records', { exact: true })).toBeVisible()
    await expect(drawer.getByText('Source: authenticated provider API query', { exact: true })).toBeVisible()
    const queryPanel = drawer.locator('.ui-collapsible[data-open="true"] .ui-collapsible__inner')
    // Visible DOM alone does not prove that asynchronous evidence is not
    // clipped by an expanding shared panel, especially on a narrow screen.
    await expect.poll(() => queryPanel.evaluate(el => el.scrollHeight - el.clientHeight)).toBeLessThanOrEqual(1)
    await drawer.getByText('pi_historyoriginal', { exact: true }).scrollIntoViewIfNeeded()
    await expect(drawer.getByText('pi_historyoriginal', { exact: true })).toBeInViewport()
    expect(otherMutations).toEqual([])
    const overflow = await drawer.evaluate(el => el.scrollWidth > el.clientWidth)
    expect(overflow).toBe(false)
    await page.screenshot({ path: testInfo.outputPath('refund-history.png'), animations: 'disabled' })
    await drawer.getByRole('button', { name: 'Close', exact: true }).click()
    await expect(drawer).toHaveCount(0)
  })
}

test('refund history handles unsupported providers, failed reads, and reopening', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  await page.route(/\/api\/v1\/admin\/payments(?:\?|$)/, route => route.fulfill({ json: { items: [payment] } }))
  let failed = true
  await page.route(`**/admin/payments/${paymentID}/refund-history*`, route => failed
    ? route.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'History temporarily unavailable' } } })
    : route.fulfill({ json: { items: [{ ...attempt, provider: 'waffo_pancake' }], canCheck: false, paymentVersion: 12 } }))
  await page.route(`**/admin/payments/${paymentID}/refund-checks?*`, route => route.fulfill({ json: { items: [] } }))
  await page.goto('/admin?tab=finance')
  await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Refund history and checks', exact: true })
  await expect(drawer.getByRole('alert')).toBeVisible()
  failed = false
  await drawer.getByRole('button', { name: 'Refresh history', exact: true }).click()
  await expect(drawer.getByText(/waffo_pancake/)).toBeVisible()
  await expect(drawer.getByRole('button', { name: 'Query payment provider', exact: true })).toBeDisabled()
  await drawer.getByRole('button', { name: 'Close', exact: true }).click()
  await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
  await expect(drawer.getByText(/waffo_pancake/)).toBeVisible()
  await expect(drawer.getByRole('alert')).toHaveCount(0)
})

test('older unbound refund evidence remains reachable without local attempts and ignores stale detail replies', async ({ page }, testInfo) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  await page.route(/\/api\/v1\/admin\/payments(?:\?|$)/, route => route.fulfill({ json: { items: [payment] } }))
  await page.route(`**/admin/payments/${paymentID}/refund-history*`, route => route.fulfill({ json: { items: [], canCheck: true, paymentVersion: 12 } }))
  const latest = { id: operationID, origin: 'operator', status: 'failed', unresolvedCount: 0, requiresReview: false, createdAt }
  const old = { id: olderID, origin: 'automatic', status: 'completed', unresolvedCount: 1, requiresReview: true, createdAt: '2026-09-18T10:00:00Z' }
  await page.route(`**/admin/payments/${paymentID}/refund-checks?*`, route => {
    const query = new URL(route.request().url()).searchParams
    return route.fulfill({ json: query.get('review') === 'unresolved' || query.has('cursor') ? { items: [old] } : { items: [latest], nextCursor: 'opaque-history-cursor' } })
  })
  let failDetail = true
  let holdReply = false
  let release: (() => void) | undefined
  let delayed = false
  const mutations: string[] = []
  page.on('request', request => { if (request.method() === 'POST' && request.url().includes('/payments/')) mutations.push(request.url()) })
  await page.route(`**/admin/payments/${paymentID}/refund-checks/${olderID}`, async route => {
    if (failDetail) return route.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'Evidence temporarily unavailable' } } })
    if (holdReply) {
      delayed = true
      await new Promise<void>(resolve => { release = resolve })
    }
    await route.fulfill({ json: { ...old, observedAt: old.createdAt, completedAt: old.createdAt, unresolvedProviderRefundIds: ['re_historical_unbound'], observations: [{ providerId: 're_historical_unbound', providerPaymentId: 'pi_original_history', amountCents: 100, currency: 'USD', status: 'succeeded' }] } })
  })
  await page.goto('/admin?tab=finance')
  await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Refund history and checks', exact: true })
  await expect(drawer.getByText('No refund attempts', { exact: true })).toBeVisible()
  await drawer.getByRole('button', { name: 'Load more queries', exact: true }).click()
  await drawer.getByRole('button', { name: /Query complete/ }).click()
  await expect(drawer.getByRole('alert')).toBeVisible()
  failDetail = false
  await drawer.getByRole('button', { name: 'Retry loading', exact: true }).click()
  await expect(drawer.getByText('re_historical_unbound', { exact: true })).toBeVisible()
  await expect(drawer.getByText('pi_original_history', { exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('historical-refund-evidence.png') })
  await drawer.getByRole('tab', { name: 'Still needs review', exact: true }).click()
  await expect(drawer.getByRole('button', { name: /Not completed/ })).toHaveCount(0)
  holdReply = true
  await drawer.getByRole('button', { name: /Query complete/ }).click()
  await expect.poll(() => delayed).toBe(true)
  await drawer.getByRole('tab', { name: 'All queries', exact: true }).click()
  release?.()
  await expect(drawer.getByRole('button', { name: /Not completed/ })).toBeVisible()
  await expect(drawer.getByText('re_historical_unbound', { exact: true })).toHaveCount(0)
  expect(mutations).toEqual([])
})

for (const width of [390, 1308]) {
  test(`late refund receipts remain separate, paginated and discard closed replies at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    await page.route(/\/api\/v1\/admin\/payments(?:\?|$)/, route => route.fulfill({ json: { items: [payment] } }))
    await page.route(`**/admin/payments/${paymentID}/refund-history*`, route => route.fulfill({ json: { items: [], canCheck: true, paymentVersion: 12 } }))
    const check = { id: operationID, origin: 'operator', status: 'completed', unresolvedCount: 0, requiresReview: true, createdAt }
    await page.route(`**/admin/payments/${paymentID}/refund-checks?*`, route => route.fulfill({ json: { items: [check] } }))
    await page.route(`**/admin/payments/${paymentID}/refund-checks/${operationID}`, route => route.fulfill({ json: {
      ...check, observedAt: createdAt, completedAt: createdAt, lateReceiptCount: 2, observations: [], unresolvedProviderRefundIds: [],
    } }))
    let fail = true
    let hold = false
    let delayed = false
    let release: (() => void) | undefined
    const mutations: string[] = []
    page.on('request', request => { if (request.method() === 'POST' && request.url().includes('/payments/')) mutations.push(request.url()) })
    await page.route(`**/admin/payments/${paymentID}/refund-checks/${operationID}/receipts*`, async route => {
      const query = new URL(route.request().url()).searchParams
      expect([...query.keys()].every(key => key === 'cursor')).toBe(true)
      if (fail) return route.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'Receipts temporarily unavailable' } } })
      const earlier = query.has('cursor')
      if (earlier) expect(query.get('cursor')).toBe(operationID)
      if (hold) {
        delayed = true
        await new Promise<void>(resolve => { release = resolve })
      }
      await route.fulfill({ json: {
        items: [{ id: earlier ? olderID : operationID, checkId: operationID, attemptNumber: earlier ? 1 : 2,
          complete: !earlier, ...(earlier ? { errorCode: 'payment_timeout' } : {}), createdAt,
          observations: [{ providerId: earlier ? 're_late_partial' : 're_late_complete', providerPaymentId: 'pi_late_original', amountCents: 100, currency: 'USD', status: 'succeeded' }],
          unresolvedProviderRefundIds: [earlier ? 're_late_partial' : 're_late_complete'],
        }], ...(earlier ? {} : { nextCursor: operationID }),
      } })
    })
    await page.goto('/admin?tab=finance')
    await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'Refund history and checks', exact: true })
    await drawer.getByRole('button', { name: /Query complete/ }).click()
    const receipts = drawer.getByRole('region', { name: 'Late read receipts (2)', exact: true })
    await expect(receipts.getByRole('alert')).toBeVisible()
    fail = false
    await receipts.getByRole('button', { name: 'Retry loading', exact: true }).click()
    await expect(receipts.getByText('re_late_complete', { exact: true })).toBeVisible()
    await expect(receipts.getByText('Complete response', { exact: true })).toBeVisible()
    await expect(receipts.getByText('Still needs review', { exact: true })).toBeVisible()
    await expect(drawer.getByText('Follow-up items when checked: 0', { exact: true })).toBeVisible()
    await receipts.getByRole('button', { name: 'Earlier receipt', exact: true }).click()
    await expect(receipts.getByText('re_late_partial', { exact: true })).toBeVisible()
    await expect(receipts.getByText('re_late_complete', { exact: true })).toHaveCount(0)
    await expect(receipts.getByText('Incomplete response', { exact: true })).toBeVisible()
    await receipts.getByText('pi_late_original', { exact: true }).scrollIntoViewIfNeeded()
    await expect(receipts.getByText('pi_late_original', { exact: true })).toBeInViewport()
    const panel = drawer.locator('.ui-collapsible[data-open="true"] .ui-collapsible__inner')
    await expect.poll(() => panel.evaluate(el => el.scrollHeight - el.clientHeight)).toBeLessThanOrEqual(1)
    expect(await drawer.evaluate(el => el.scrollWidth > el.clientWidth)).toBe(false)
    await page.screenshot({ path: testInfo.outputPath('late-refund-receipt.png'), animations: 'disabled' })
    await receipts.getByRole('button', { name: 'Latest receipt', exact: true }).click()
    await expect(receipts.getByText('re_late_complete', { exact: true })).toBeVisible()
    hold = true
    await receipts.getByRole('button', { name: 'Earlier receipt', exact: true }).click()
    await expect.poll(() => delayed).toBe(true)
    await drawer.getByRole('button', { name: 'Close', exact: true }).click()
    release?.()
    hold = false
    await expect(drawer).toHaveCount(0)
    await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
    await drawer.getByRole('button', { name: /Query complete/ }).click()
    await expect(receipts.getByText('re_late_complete', { exact: true })).toBeVisible()
    await expect(receipts.getByText('re_late_partial', { exact: true })).toHaveCount(0)
    expect(mutations).toEqual([])
  })
}

test('interrupted refund reads show current recovery needs and preserve the original result', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  await page.route(/\/api\/v1\/admin\/payments(?:\?|$)/, route => route.fulfill({ json: { items: [payment] } }))
  await page.route(`**/admin/payments/${paymentID}/refund-history*`, route => route.fulfill({ json: { items: [], canCheck: true, paymentVersion: 12 } }))
  const check = { id: operationID, origin: 'operator', status: 'completed', unresolvedCount: 0, requiresReview: true, createdAt }
  await page.route(`**/admin/payments/${paymentID}/refund-checks?*`, route => route.fulfill({ json: { items: [check] } }))
  let recovered = false
  const mutations: string[] = []
  page.on('request', request => { if (request.method() === 'POST' && request.url().includes('/payments/')) mutations.push(request.url()) })
  await page.route(`**/admin/payments/${paymentID}/refund-checks/${operationID}`, route => route.fulfill({ json: {
    ...check, observedAt: createdAt, completedAt: createdAt, lateReceiptCount: 0, observations: [], unresolvedProviderRefundIds: [],
    unrecordedReadCount: recovered ? 0 : 1, recoveredReadCount: recovered ? 1 : 0,
  } }))
  await page.goto('/admin?tab=finance')
  await page.getByRole('button', { name: 'Refund history and checks', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Refund history and checks', exact: true })
  await drawer.getByRole('button', { name: /Query complete/ }).click()
  await expect(drawer.getByText(/1 reads have no saved response/)).toBeVisible()
  await expect(drawer.getByText('Follow-up items when checked: 0', { exact: true })).toBeVisible()
  recovered = true
  await drawer.getByRole('button', { name: 'Refresh history', exact: true }).click()
  await drawer.getByRole('button', { name: /Query complete/ }).click()
  await expect(drawer.getByText(/1 interrupted reads were reconciled/)).toBeVisible()
  await expect(drawer.getByText(/1 reads have no saved response/)).toHaveCount(0)
  await expect(drawer.getByText('Follow-up items when checked: 0', { exact: true })).toBeVisible()
  expect(mutations).toEqual([])
})
