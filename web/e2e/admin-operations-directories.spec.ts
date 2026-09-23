import { fixtureCredentials } from './helpers/identity'
import { expectSelection } from './helpers/select'
import { expect, test } from '@playwright/test'

test('restores generation and finance filters with exact mutations', async ({ page }) => {
  test.setTimeout(60_000)
  const runID = Date.now().toString(36)
  const handle = `ops_${runID}`
  const password = `operations-${runID}-test`
  const prompt = `Operations directory generation ${runID}`

  const registered = await page.request.post('/api/v1/auth/register', { data: {
    email: `${handle}@example.test`, password, handle, displayName: `Operations Directory ${runID}`, locale: 'en-US', timezone: 'UTC',
  } })
  expect(registered.status()).toBe(201)
  const generation = await page.request.post('/api/v1/generations', {
    headers: { 'Idempotency-Key': `admin-operations-${runID}` },
    data: { mode: 'chat', prompt },
  })
  expect(generation.status()).toBe(202)
  const generationID = (await generation.json() as { id: string }).id
  await expect.poll(async () => {
    const response = await page.request.get(`/api/v1/generations/${generationID}`)
    return (await response.json() as { status: string }).status
  }).toBe('succeeded')

  const adminSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect(adminSession.ok()).toBeTruthy()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/admin?tab=generations&generationQ=${runID}&generationMode=chat&generationStatus=succeeded`)
  await expect(page.getByRole('searchbox', { name: 'Search generations', exact: true })).toHaveValue(runID)
  await expectSelection(page.getByRole('combobox', { name: 'Creation type', exact: true }), 'chat')
  await expectSelection(page.getByRole('combobox', { name: 'Generation status', exact: true }), 'succeeded')
  await expect(page.locator('.admin-list article').filter({ hasText: prompt })).toBeVisible()
  let widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await page.goto(`/admin?tab=finance&financeQ=${handle}&financeState=available`)
  await expect(page.getByRole('searchbox', { name: 'Search accounts', exact: true })).toHaveValue(handle)
  await expectSelection(page.getByRole('combobox', { name: 'Account evidence', exact: true }), 'available')
  const account = page.locator('.finance-admin-list article').filter({ hasText: handle })
  await expect(account).toBeVisible()
  await account.getByRole('button', { name: 'Adjust credits', exact: true }).click()
  await page.getByLabel('Adjustment in cents', { exact: true }).fill('1')
  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await expect(account).toBeVisible()
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await page.reload()
  await expect(account).toBeVisible()
})
