import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'

const firstId = '00000000-0000-4000-8000-000000009811'
const secondId = '00000000-0000-4000-8000-000000009812'
const productId = '00000000-0000-4000-8000-000000000501'
const first = { orderId: firstId, productId, title: 'Accepted first resource', amountCents: 1900, currency: 'USD', status: 'fulfilled', paymentStatus: 'paid', environment: 'live', hasContract: true, needsReview: false, createdAt: '2026-09-01T08:00:00Z', updatedAt: '2026-09-01T08:01:00Z', licenseAcceptedAt: '2026-09-01T08:00:00Z', paidAt: '2026-09-01T08:01:00Z' }
const second = { ...first, orderId: secondId, title: 'Accepted second resource', status: 'refunded', paymentStatus: 'refunded' }

for (const width of [390, 1308]) {
  test(`sales pagination, private details and shared controls at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    // This smoke request is not intercepted: the authenticated API route must
    // exist and return only this seller's data. Populated UI cases follow below.
    const real = await page.request.get('/api/v1/seller/sales?limit=1')
    expect(real.ok()).toBeTruthy()
    expect(real.headers()['cache-control']).toBe('private, no-store')
    let continuations = 0
    await page.route(/\/api\/v1\/seller\/sales\?.*$/, async route => {
      const params = new URL(route.request().url()).searchParams
      if (params.has('cursor')) {
        expect(params.get('cursor')).toBe('sales-next')
        continuations++
        if (continuations === 1) return route.abort()
        return route.fulfill({ json: { items: [first, second], total: 2 } })
      }
      return route.fulfill({ json: params.get('status') === 'refunded' ? { items: [second], total: 1 } : { items: [first], total: 2, nextCursor: 'sales-next' } })
    })
    await page.route(`**/api/v1/seller/sales/${firstId}`, route => route.fulfill({ json: { ...first, licenseName: 'Accepted commercial license', licenseVersion: '1.0', licenseTerms: 'The exact terms accepted by the buyer.' } }))
    await page.route(`**/api/v1/seller/sales/${firstId}/events?*`, route => {
      const more = new URL(route.request().url()).searchParams.has('cursor')
      return route.fulfill({ json: more ? { items: [{ sequence: 2, fromStatus: 'payment_pending', toStatus: 'fulfilled', createdAt: first.paidAt }] } : { items: [{ sequence: 1, toStatus: 'payment_pending', createdAt: first.createdAt }], nextCursor: 'event-next' } })
    })
    await page.goto('/workspace/products')
    await page.getByRole('link', { name: 'Sales history', exact: true }).click()
    await expect(page.locator('.task-results-meta')).toContainText('2 orders')
    await expect(page.getByText('Order and settlement amounts are recorded separately. A confirmed provider transfer does not confirm bank arrival.', { exact: true })).toBeVisible()
    expect(await page.locator('.ui-filter-bar .ui-select__trigger').count()).toBe(2)
    await page.getByRole('button', { name: 'Load more', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.locator('.ui-content-card')).toHaveCount(1)
    await page.getByRole('button', { name: 'Reload', exact: true }).click()
    await expect(page.locator('.ui-content-card')).toHaveCount(2)
    await expect(page.locator('.task-results-meta')).toContainText('2 orders')
    expect(continuations).toBe(2)
    await page.getByRole('link').filter({ hasText: first.title }).click()
    await expect(page).toHaveURL(`/workspace/sales/${firstId}`)
    await expect(page.locator('.seller-sale-detail')).toContainText(firstId)
    await page.getByRole('button', { name: 'License accepted at purchase', exact: true }).click()
    await expect(page.getByText('The exact terms accepted by the buyer.', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Load more', exact: true }).click()
    await expect(page.locator('.seller-sale-events li')).toHaveCount(2)
    await expect(page.locator('.seller-sale-events')).toContainText('Pending payment → Delivered')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/hcai-seller-sale-${width}.png`, fullPage: true })
    await page.getByRole('link', { name: 'Back to sales', exact: true }).click()
    await chooseOption(page.locator('.ui-filter-bar select').first(), 'refunded')
    await expect(page).toHaveURL(/status=refunded/)
    await expect(page.locator('.ui-content-card')).toHaveCount(1)
    await expect(page.locator('.task-results-meta')).toContainText('1 order')
    await expect(page.locator('.ui-content-card')).toContainText(second.title)
    await page.reload()
    await expect(page.locator('.ui-content-card')).toContainText(second.title)
    await page.screenshot({ path: `/tmp/hcai-seller-sales-${width}.png`, fullPage: true })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

test('sales ignores a late continuation after a filter change', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route(/\/api\/v1\/seller\/sales\?.*$/, async route => {
    const params = new URL(route.request().url()).searchParams
    if (params.has('cursor')) { await gate; return route.fulfill({ json: { items: [second], total: 2 } }) }
    return route.fulfill({ json: params.get('status') === 'refunded' ? { items: [], total: 0 } : { items: [first], total: 2, nextCursor: 'late' } })
  })
  try {
    await page.goto('/workspace/sales')
    await expect(page.locator('.ui-content-card')).toHaveCount(1)
    const pending = page.waitForRequest(r => r.url().includes('cursor=late'))
    await page.getByRole('button', { name: 'Load more', exact: true }).click()
    await pending
    await chooseOption(page.locator('.ui-filter-bar select').first(), 'refunded')
    await expect(page.getByText('No matching sales', { exact: true })).toBeVisible()
    const finished = page.waitForResponse(r => r.url().includes('cursor=late'))
    release()
    await (await finished).finished()
    await page.waitForLoadState('networkidle')
    await expect(page.locator('.ui-content-card')).toHaveCount(0)
    await expect(page.locator('.task-results-meta')).toContainText('0 orders')
  } finally { release() }
})

test('guests must log in and unavailable sales have no detail content', async ({ page }) => {
  await page.goto('/workspace/sales')
  await expect(page.locator('.seller-sale-detail')).toHaveCount(0)
  expect((await page.request.get('/api/v1/seller/sales')).status()).toBe(401)
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.goto(`/workspace/sales/${firstId}`)
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.locator('.seller-sale-detail')).toHaveCount(0)
})
