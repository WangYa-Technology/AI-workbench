import { expect, test } from '@playwright/test'

test('filters and paginates the immutable owner billing statement', async ({ page }) => {
  test.setTimeout(45_000)
  const runID = Date.now().toString(36)
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  const generation = await page.request.post('/api/v1/generations', {
    headers: { 'Idempotency-Key': `billing-statement-${runID}` },
    data: { mode: 'chat', prompt: `Billing statement evidence ${runID}` },
  })
  expect(generation.status()).toBe(202)
  const generationID = (await generation.json() as { id: string }).id
  await expect.poll(async () => {
    const response = await page.request.get(`/api/v1/generations/${generationID}`)
    return (await response.json() as { status: string }).status
  }).toBe('succeeded')

  const filteredResponse = await page.request.get('/api/v1/billing/statement?direction=debit&entryType=generation_charge&limit=1')
  expect(filteredResponse.ok()).toBeTruthy()
  const filtered = await filteredResponse.json() as {
    account: { availableCents: number }
    entries: Array<{ operationId: string; direction: string; entryType: string }>
    nextCursor?: string
  }
  expect(filtered.account.availableCents).toBeGreaterThanOrEqual(0)
  expect(filtered.entries).toEqual([expect.objectContaining({ operationId: generationID, direction: 'debit', entryType: 'generation_charge' })])
  expect(filtered.nextCursor).toBeTruthy()

  await page.goto('/workspace/billing')
  const utcDate = new Date().toISOString().slice(0, 10)
  const filters = page.locator('.billing-filters')
  await filters.getByRole('combobox', { name: 'Direction', exact: true }).selectOption('debit')
  await filters.getByRole('combobox', { name: 'Entry type', exact: true }).selectOption('generation_charge')
  await filters.getByRole('textbox', { name: 'From (UTC)', exact: true }).fill(utcDate)
  await filters.getByRole('textbox', { name: 'To (UTC)', exact: true }).fill(utcDate)
  await filters.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`direction=debit.*entryType=generation_charge.*dateFrom=${utcDate}.*dateTo=${utcDate}`))
  await expect(page.locator('.billing-entry').first()).toContainText('Generation charge')

  await page.goto('/workspace/billing?direction=debit&entryType=generation_charge&limit=1')
  await expect(page.locator('.billing-entry')).toHaveCount(1)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.locator('.billing-entry')).toHaveCount(2)

  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(page.locator('.billing-filters')).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})
