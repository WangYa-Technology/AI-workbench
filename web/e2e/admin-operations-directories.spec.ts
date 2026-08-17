import { expect, test } from '@playwright/test'

test('restores generation, finance, and audit operations filters with exact mutations', async ({ page }) => {
  test.setTimeout(60_000)
  const runID = Date.now().toString(36)
  const handle = `ops_${runID}`
  const password = `operations-${runID}-test`
  const prompt = `Operations directory generation ${runID}`

  const registered = await page.request.post('/api/v1/auth/register', { data: {
    email: `${handle}@example.test`, password, handle, displayName: `Operations Directory ${runID}`, locale: 'en-US', timezone: 'UTC',
  } })
  expect(registered.status()).toBe(201)
  const principal = await registered.json() as { user: { id: string } }
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

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/admin?tab=generations&generationQ=${runID}&generationMode=chat&generationStatus=succeeded`)
  await expect(page.getByRole('searchbox', { name: 'Search generations', exact: true })).toHaveValue(runID)
  await expect(page.getByRole('combobox', { name: 'Creation mode', exact: true })).toHaveValue('chat')
  await expect(page.getByRole('combobox', { name: 'Generation status', exact: true })).toHaveValue('succeeded')
  await expect(page.locator('.admin-list article').filter({ hasText: prompt })).toBeVisible()
  let widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await page.goto(`/admin?tab=finance&financeQ=${handle}&financeState=available`)
  await expect(page.getByRole('searchbox', { name: 'Search accounts', exact: true })).toHaveValue(handle)
  await expect(page.getByRole('combobox', { name: 'Account evidence', exact: true })).toHaveValue('available')
  const account = page.locator('.finance-admin-list article').filter({ hasText: handle })
  await expect(account).toBeVisible()
  await account.getByRole('button', { name: 'Adjust credits', exact: true }).click()
  await page.getByLabel('Adjustment in cents', { exact: true }).fill('1')
  const reason = `Operations directory adjustment ${runID} with exact account evidence.`
  await page.getByLabel('Required reason', { exact: true }).fill(reason)
  await page.getByLabel('I reviewed the target and confirm this operation.', { exact: true }).check()
  await page.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()
  await expect(account).toBeVisible()
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await page.goto(`/admin?tab=audit&auditQ=${runID}&auditAction=admin.billing_adjusted&auditResourceType=billing_account`)
  await expect(page.getByRole('searchbox', { name: 'Search audit evidence', exact: true })).toHaveValue(runID)
  await expect(page.getByRole('textbox', { name: 'Exact action', exact: true })).toHaveValue('admin.billing_adjusted')
  await expect(page.getByRole('textbox', { name: 'Exact resource type', exact: true })).toHaveValue('billing_account')
  const event = page.locator('.audit-list article').filter({ hasText: reason })
  await expect(event).toBeVisible()
  await expect(event).toContainText(principal.user.id)
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})
