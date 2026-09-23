import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'
import { expect, test } from '@playwright/test'

test('filters and paginates the immutable owner billing statement', async ({ page }) => {
  test.setTimeout(45_000)
  const session = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(session.ok()).toBeTruthy()

  const owner = (await session.json()).user.id
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  for (let index = 0; index < 2; index++) {
    const response = await page.request.post(`/api/v1/admin/finance/accounts/${owner}/adjust`, { headers: { 'Idempotency-Key': crypto.randomUUID() }, data: { deltaCents: 1, currency: 'USD' } })
    expect(response.ok()).toBeTruthy()
  }
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })

  const filteredResponse = await page.request.get('/api/v1/billing/statement?direction=credit&entryType=admin_adjustment&limit=1')
  expect(filteredResponse.ok()).toBeTruthy()
  const filtered = await filteredResponse.json() as {
    account: { availableCents: number }
    entries: Array<{ operationId: string; direction: string; entryType: string }>
    nextCursor?: string
  }
  expect(filtered.account.availableCents).toBeGreaterThanOrEqual(0)
  expect(filtered.entries).toEqual([expect.objectContaining({ direction: 'credit', entryType: 'admin_adjustment' })])
  expect(filtered.nextCursor).toBeTruthy()

  await page.goto('/workspace/billing')
  const utcDate = new Date().toISOString().slice(0, 10)
  const filters = page.locator('.billing-filters')
  await chooseOption(filters.getByRole('combobox', { name: 'Direction', exact: true }), 'credit')
  await chooseOption(filters.getByRole('combobox', { name: 'Entry type', exact: true }), 'admin_adjustment')
  await filters.getByLabel('From (UTC)', { exact: true }).fill(utcDate)
  await filters.getByLabel('To (UTC)', { exact: true }).fill(utcDate)
  await filters.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`direction=credit.*entryType=admin_adjustment.*dateFrom=${utcDate}.*dateTo=${utcDate}`))
  await expect(page.locator('.billing-wallet-ledger .billing-entry').first()).toContainText('Administrative balance adjustment')

  await page.goto('/workspace/billing?direction=credit&entryType=admin_adjustment&limit=1')
  await expect(page.locator('.billing-wallet-ledger .billing-entry')).toHaveCount(1)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.locator('.billing-wallet-ledger .billing-entry')).toHaveCount(2)

  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(page.locator('.billing-filters')).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})
