import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'

const first = {
  id: '00000000-0000-4000-8000-000000011801', provider: 'stripe',
  providerEventId: `evt_${'original'.repeat(35)}`, eventType: 'checkout.session.completed', liveMode: false,
  claimedPaymentId: '00000000-0000-4000-8000-000000011811', candidatePaymentId: '00000000-0000-4000-8000-000000011811',
  amountCents: 1900, currency: 'USD', state: 'pending', rejectionCode: 'payment_binding_conflict',
  receivedAt: '2026-01-01T00:00:00Z', version: 1, hasReviewHold: true,
}
const second = { ...first, id: '00000000-0000-4000-8000-000000011802', providerEventId: 'evt_second_original' }

for (const width of [390, 1308]) {
  test(`payment evidence pagination, review and shared controls at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    const actual = await page.request.get('/api/v1/admin/payments/webhook-quarantines?limit=2')
    expect(actual.status()).toBe(200)
    expect(actual.headers()['cache-control']).toBe('private, no-store')
    let failed = false
    let checks = 0
    await page.route('**/api/v1/admin/payments/webhook-quarantines**', async route => {
      const request = route.request()
      const query = new URL(request.url()).searchParams
      if (request.method() === 'POST') {
        expect(request.postDataJSON()).toEqual({ expectedVersion: checks + 1, reason: 'Original provider evidence was reviewed.' })
        checks++
        return route.fulfill({ json: { ...first, state: checks === 1 ? 'pending' : 'admitted', version: checks + 1, hasReviewHold: checks === 1 } })
      }
      if (query.get('state') === 'admitted') return route.fulfill({ json: { items: [{ ...first, state: 'admitted', version: 3, hasReviewHold: false }] } })
      if (query.has('cursor')) {
        expect(query.get('cursor')).toBe('older')
        if (!failed) { failed = true; return route.abort() }
        return route.fulfill({ json: { items: [first, second] } })
      }
      return route.fulfill({ json: { items: checks > 1 ? [] : [{ ...first, version: checks + 1 }], nextCursor: checks === 0 ? 'older' : undefined } })
    })
    await page.goto('/admin?tab=finance')
    const section = page.getByRole('region', { name: 'Signed payment evidence to review' })
    await expect(section).toContainText(first.providerEventId)
    await expect(section.locator('.ui-select__trigger')).toHaveCount(3)
    await section.getByRole('button', { name: 'Load more evidence', exact: true }).click()
    await expect(section.getByRole('alert')).toBeVisible()
    await section.getByRole('button', { name: 'Load more evidence', exact: true }).click()
    await expect(section.locator('.ui-content-card')).toHaveCount(2)
    await section.getByRole('button', { name: 'Review and recheck', exact: true }).first().click()
    const confirm = section.getByRole('button', { name: 'Recheck original evidence', exact: true })
    await section.getByRole('textbox', { name: 'Review reason' }).fill('short')
    await expect(confirm).toBeDisabled()
    await section.getByRole('textbox', { name: 'Review reason' }).fill('核'.repeat(1001))
    await expect(confirm).toBeDisabled()
    await section.getByRole('textbox', { name: 'Review reason' }).fill('Original provider evidence was reviewed.')
    await confirm.click()
    await expect(section).toContainText('The evidence still conflicts with the original transaction.')
    await section.getByRole('button', { name: 'Review and recheck', exact: true }).click()
    await section.getByRole('textbox', { name: 'Review reason' }).fill('Original provider evidence was reviewed.')
    await confirm.click()
    await expect(section).toContainText('No matching rejected callbacks')
    await chooseOption(section.locator('select').first(), 'admitted')
    await expect(section).toContainText('Admitted for processing')
    await expect(section.getByRole('button', { name: 'Review and recheck', exact: true })).toHaveCount(0)
    await expect(section).not.toContainText('Financial actions remain on hold')
    expect(await section.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await section.screenshot({ path: `/tmp/hcai-payment-evidence-${width}.png`, animations: 'disabled' })
  })
}

test('payment evidence ignores stale filters and denies non-finance users', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  expect((await page.request.get('/api/v1/admin/payments/webhook-quarantines')).status()).toBe(403)
  await page.goto('/admin?tab=finance')
  await expect(page.getByRole('region', { name: 'Signed payment evidence to review' })).toHaveCount(0)
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  let release!: () => void
  const held = new Promise<void>(resolve => { release = resolve })
  let arrived!: () => void
  const requested = new Promise<void>(resolve => { arrived = resolve })
  await page.route('**/api/v1/admin/payments/webhook-quarantines?*', async route => {
    if (!new URL(route.request().url()).searchParams.has('mode')) {
      arrived()
      await held
      return route.fulfill({ json: { items: [first] } })
    }
    return route.fulfill({ json: { items: [second] } })
  })
  await page.goto('/admin?tab=finance')
  await requested
  const section = page.getByRole('region', { name: 'Signed payment evidence to review' })
  await chooseOption(section.locator('select').nth(1), 'test')
  await expect(section).toContainText(second.providerEventId)
  release()
  await expect(section).not.toContainText(first.providerEventId)
})
