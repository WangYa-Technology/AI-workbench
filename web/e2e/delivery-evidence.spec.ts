import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'

const first = { orderId: '00000000-0000-4000-8000-000000011101', title: 'Original contract missing', orderStatus: 'fulfilled', environment: 'unknown', gap: 'contract_missing', hasActiveRights: true, hasPendingPayment: false, hasUnsettledFunds: false, hasSnapshot: false, createdAt: '2020-01-01T00:00:00Z' }
const last = { ...first, orderId: '00000000-0000-4000-8000-000000011102', title: 'Unbound copy to inspect', environment: 'test', gap: 'legacy_snapshot_unbound', hasSnapshot: true }
const refund = { ...first, orderId: '00000000-0000-4000-8000-000000011103', title: 'Deleted buyer refund evidence', orderStatus: 'refund_requested', environment: 'test', hasActiveRights: false, hasUnsettledFunds: true }

for (const width of [390, 1308]) {
  test(`delivery evidence pagination, filters and shared controls at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    const actual = await page.request.get('/api/v1/admin/product-deliveries/evidence-gaps?limit=2')
    expect(actual.status()).toBe(200)
    expect(actual.headers()['cache-control']).toBe('private, no-store')
    expect((await actual.json()).scanned).toBeLessThanOrEqual(500)
    let failed = false
    await page.route('**/api/v1/admin/product-deliveries/evidence-gaps?*', route => {
      expect(route.request().method()).toBe('GET')
      const query = new URL(route.request().url()).searchParams
      if (query.get('scope') === 'unsettled') {
        expect(query.get('environment')).toBe('test')
        expect(query.has('cursor')).toBe(false)
        return route.fulfill({ json: { items: [refund], scanned: 5 } })
      }
      expect(query.get('scope')).toBe('active')
      if (query.has('environment')) {
        expect(query.has('cursor')).toBe(false)
        return route.fulfill({ json: { items: [last], scanned: 5 } })
      }
      if (query.get('cursor') === 'older') {
        if (!failed) { failed = true; return route.abort() }
        return route.fulfill({ json: { items: [first], scanned: 10 } })
      }
      return route.fulfill({ json: { items: [], scanned: 500, nextCursor: 'older' } })
    })
    await page.goto('/admin/deliveries')
    const section = page.getByRole('region', { name: 'Delivery evidence to review' })
    await expect(section).toContainText('No matches in this batch')
    await expect(section).toContainText('500 orders checked, 0 records found')
    await expect(section.locator('.ui-select__trigger')).toHaveCount(3)
    await section.getByRole('button', { name: 'Continue review', exact: true }).click()
    await expect(section.getByRole('alert')).toBeVisible()
    await section.getByRole('button', { name: 'Continue review', exact: true }).click()
    await expect(section).toContainText(first.title)
    await expect(section).toContainText('510 orders checked, 1 records found')
    await expect(section.getByRole('link', { name: 'Inspect delivery' })).toHaveCount(0)
    await chooseOption(section.locator('select').nth(1), 'test')
    await expect(section).toContainText(last.title)
    await expect(section).not.toContainText(first.title)
    await expect(section.getByRole('link', { name: 'Inspect delivery' })).toHaveAttribute('href', `/admin/deliveries/${last.orderId}`)
    await chooseOption(section.locator('select').nth(2), 'unsettled')
    await expect(section).toContainText(refund.title)
    await expect(section).not.toContainText(last.title)
    await expect(section).toContainText('Unresolved funds — retain delivery evidence')
    await expect(section.locator('.ui-card-tag').filter({ hasText: 'Active purchase rights' })).toHaveCount(0)
    await expect(section.locator('.ui-card-tag').filter({ hasText: 'Payment pending' })).toHaveCount(0)
    await expect(section.getByRole('link', { name: 'Inspect delivery' })).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.evaluate(() => {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
      window.scrollTo({ top: 0, behavior: 'instant' })
      document.querySelector('#main-content')?.scrollTo({ top: 0, behavior: 'instant' })
    })
    await expect.poll(() => page.locator('.skip-link').evaluate(el => el.getBoundingClientRect().bottom)).toBeLessThan(0)
    await page.screenshot({ path: `/tmp/hcai-delivery-evidence-${width}.png`, fullPage: true, animations: 'disabled' })
  })
}

test('delivery inventory ignores stale filter results and protects non-operators', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  expect((await page.request.get('/api/v1/admin/product-deliveries/evidence-gaps')).status()).toBe(403)
  await page.goto('/admin/deliveries')
  await expect(page.getByRole('region', { name: 'Delivery evidence to review' })).toHaveCount(0)
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  let release!: () => void
  const held = new Promise<void>(resolve => { release = resolve })
  let arrived!: () => void
  const requested = new Promise<void>(resolve => { arrived = resolve })
  await page.route('**/api/v1/admin/product-deliveries/evidence-gaps?*', async route => {
    if (new URL(route.request().url()).searchParams.get('scope') !== 'unsettled') {
      arrived()
      await held
      return route.fulfill({ json: { items: [first], scanned: 3 } })
    }
    return route.fulfill({ json: { items: [refund], scanned: 4 } })
  })
  await page.goto('/admin/deliveries')
  await requested
  const section = page.getByRole('region', { name: 'Delivery evidence to review' })
  await chooseOption(section.locator('select').nth(2), 'unsettled')
  await expect(section).toContainText(refund.title)
  release()
  await expect(section).not.toContainText(first.title)
  await expect(section).toContainText('4 orders checked, 1 records found')
})
