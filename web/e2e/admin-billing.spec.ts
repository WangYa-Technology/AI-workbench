import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('enforces operations permissions and persists the exact wallet adjustment', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect((await page.request.get('/api/v1/admin/finance/accounts')).status()).toBe(403)
  await page.goto('/admin')
  await expect(page.getByRole('heading', { name: 'Operations access required', exact: true })).toBeVisible()
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  await page.reload()
  await page.getByRole('link', { name: 'Finance', exact: true }).click()
  const accounts = await (await page.request.get('/api/v1/admin/finance/accounts')).json()
  const before = accounts.items.find((item: { displayName: string }) => item.displayName === 'Fixture Studio')
  expect(before).toBeTruthy()
  const target = page.locator('.finance-admin-list article').filter({ hasText: 'Fixture Studio' })
  await target.getByRole('button', { name: 'Adjust credits', exact: true }).click()
  await page.getByLabel('Adjustment in cents', { exact: true }).fill('1')
  const response = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith(`/finance/accounts/${before.userId}/adjust`))
  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  const updated = await response
  expect(updated.ok()).toBeTruthy()
  expect(await updated.json()).toMatchObject({ userId: before.userId, balanceCents: before.balanceCents + 1 })
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await page.reload()
  const after = await (await page.request.get('/api/v1/admin/finance/accounts')).json()
  expect(after.items.find((item: { userId: string }) => item.userId === before.userId).balanceCents).toBe(before.balanceCents + 1)
})

test('charges a completed generation once in points and exposes the personal statement', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  const before = await (await page.request.get('/api/v1/billing/points')).json()
  const prompt = `Billing capture verification ${Date.now().toString(36)}`
  await page.goto('/create/image')
  await page.locator('.creation-composer textarea').fill(prompt)
  const submitted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/api/v1/generations'))
  await page.locator('.creation-submit').click()
  const response = await submitted
  expect(response.status()).toBe(202)
  const generation = await response.json()
  await expect(page.locator('.creation-turn').filter({ hasText: prompt })).toHaveAttribute('data-status', 'succeeded')
  const completed = await (await page.request.get(`/api/v1/generations/${generation.id}`)).json()
  const after = await (await page.request.get('/api/v1/billing/points')).json()
  const charges = after.entries.filter((entry: { operationId: string; entryType: string }) => entry.operationId === generation.id && entry.entryType === 'generation_charge')
  expect(charges).toHaveLength(1)
  expect(charges[0].amountPoints).toBe(completed.chargedPoints)
  expect(after.account.balancePoints).toBe(before.account.balancePoints - completed.chargedPoints)
  await page.goto('/workspace/billing')
  await expect(page.locator('.billing-point-ledger .billing-entry').filter({ hasText: 'AI generation' }).first()).toBeVisible()
})

test('retries a committed adjustment after response loss and reload without charging twice', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  await page.goto('/admin?tab=finance')
  const accounts = await (await page.request.get('/api/v1/admin/finance/accounts')).json()
  const before = accounts.items.find((item: { displayName: string }) => item.displayName === 'Fixture Studio')
  const path = `/api/v1/admin/finance/accounts/${before.userId}/adjust`
  const keys: string[] = []
  let lost = true
  let denied = false
  let operation = ''
  await page.route(`**${path}`, async route => {
    keys.push(route.request().headers()['idempotency-key'] || '')
    if (!lost && !denied) {
      denied = true
      await route.fulfill({ status: 403, json: { error: { code: 'permission_required', message: 'Permission temporarily unavailable', retryable: false } } })
      return
    }
    const response = await route.fetch()
    expect(response.status()).toBe(200)
    const receipt = await response.json()
    if (lost) {
      lost = false
      operation = receipt.operationId
      await route.abort('connectionreset')
    } else {
      expect(receipt).toMatchObject({ operationId: operation, replayed: true, balanceCents: before.balanceCents + 7 })
      await route.fulfill({ response })
    }
  })
  const open = async () => {
    await page.locator('.finance-admin-list article').filter({ hasText: 'Fixture Studio' }).getByRole('button', { name: 'Adjust credits', exact: true }).click()
    await page.getByLabel('Adjustment in cents', { exact: true }).fill('7')
    await page.getByRole('button', { name: 'Apply', exact: true }).click()
  }
  await open()
  await expect.poll(() => operation).not.toBe('')
  await expect(page.getByRole('button', { name: 'Apply', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect.poll(() => denied).toBe(true)
  await expect(page.getByRole('heading', { name: 'Operations access needs verification' })).toBeVisible()
  await page.reload()
  await open()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  expect(keys).toHaveLength(3)
  expect(keys[0]).toBeTruthy()
  expect(keys[1]).toBe(keys[0])
  expect(keys[2]).toBe(keys[0])
  const after = await (await page.request.get('/api/v1/admin/finance/accounts')).json()
  expect(after.items.find((item: { userId: string }) => item.userId === before.userId).balanceCents).toBe(before.balanceCents + 7)
})

test('closes a confirmed adjustment even when refreshing the directory fails', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  await page.goto('/admin?tab=finance')
  await page.locator('.finance-admin-list article').filter({ hasText: 'Fixture Studio' }).getByRole('button', { name: 'Adjust credits', exact: true }).click()
  await page.getByLabel('Adjustment in cents', { exact: true }).fill('2')
  await page.route('**/api/v1/admin/finance/accounts?*', route => route.fulfill({ status: 503, json: { error: { code: 'temporary_failure', message: 'Directory unavailable', retryable: true } } }))
  const accepted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/adjust'))
  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  expect((await accepted).status()).toBe(200)
  await expect(page.getByRole('button', { name: 'Apply', exact: true })).toHaveCount(0)
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
})

test('keeps the provider catalog usable on a mobile viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  await page.goto('/admin?tab=providers')
  await expect(page.getByRole('heading', { name: 'Model management', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Add Provider', exact: true })).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
  // Runtime Local Test profiles are separate from administrator-created Provider configurations.
  const response = await page.request.get('/api/v1/admin/providers')
  expect(response.ok()).toBeTruthy()
})
