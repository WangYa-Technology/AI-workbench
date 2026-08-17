import { readFile } from 'node:fs/promises'
import { expect, test } from '@playwright/test'

test('exports account data, controls a legal hold, and cancels scheduled deletion', async ({ page }) => {
  const runID = Date.now().toString(36)
  const handle = `rights_${runID}`
  const email = `${handle}@example.test`
  const password = `Local-rights-${runID}`
  const registration = await page.request.post('/api/v1/auth/register', {
    data: { email, password, handle, displayName: 'Data Rights Owner', locale: 'en-US', timezone: 'UTC' },
  })
  expect(registration.status()).toBe(201)

  await page.goto('/settings?section=privacy')
  await expect(page.getByRole('heading', { name: 'Privacy and data', exact: true }).first()).toBeVisible()
  await page.getByLabel('Confirm your exact handle', { exact: true }).fill(handle)
  await page.getByRole('button', { name: 'Request export', exact: true }).click()
  await expect(page.getByText('Ready to download', { exact: false })).toBeVisible({ timeout: 10_000 })
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('link', { name: 'Download JSON', exact: true }).click()
  const download = await downloadPromise
  const downloadPath = await download.path()
  expect(downloadPath).toBeTruthy()
  const exportBody = await readFile(downloadPath!, 'utf8')
  expect(exportBody).toContain(handle)
  expect(exportBody.toLowerCase()).not.toContain('token_hash')
  expect(exportBody.toLowerCase()).not.toContain('network_hash')

  await page.getByLabel('Confirm your exact handle', { exact: true }).fill(handle)
  await page.getByLabel('I understand that deletion becomes irreversible after the cancellation window.', { exact: true }).check()
  await page.getByRole('button', { name: 'Schedule deletion', exact: true }).click()
  await expect(page.getByText('Account deletion scheduled with a 30-day cancellation window.', { exact: true })).toBeVisible()

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto('/admin?tab=dataRights')
  const requestRow = page.locator('.data-rights-admin .admin-list article').filter({ hasText: handle }).first()
  await expect(requestRow).toBeVisible()
  await requestRow.getByRole('button', { name: 'Place legal hold', exact: true }).click()
  await page.getByLabel('Authority reference', { exact: true }).fill(`LEGAL-${runID}`)
  await page.getByLabel('Required reason', { exact: true }).fill(`E2E ${runID}: signed authority requires temporary preservation review.`)
  await page.getByLabel('I reviewed the target and confirm this operation.', { exact: true }).check()
  await page.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()

  const ownerSession = await page.request.post('/api/v1/auth/login', { data: { email, password } })
  expect(ownerSession.ok()).toBeTruthy()
  await page.goto('/settings?section=privacy')
  await expect(page.getByText('Blocked by legal hold', { exact: false })).toBeVisible()

  const adminAgain = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminAgain.ok()).toBeTruthy()
  await page.goto('/admin?tab=dataRights')
  const holdRow = page.locator('.data-rights-admin section').nth(1).locator('.admin-list article').filter({ hasText: handle }).first()
  await expect(holdRow).toBeVisible()
  await holdRow.getByRole('button', { name: 'Release hold', exact: true }).click()
  await page.getByLabel('Required reason', { exact: true }).fill(`E2E ${runID}: authority confirmed preservation is no longer required.`)
  await page.getByLabel('I reviewed the target and confirm this operation.', { exact: true }).check()
  await page.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()

  const ownerAgain = await page.request.post('/api/v1/auth/login', { data: { email, password } })
  expect(ownerAgain.ok()).toBeTruthy()
  await page.goto('/settings?section=privacy')
  const deletionRow = page.locator('.data-rights-list article').filter({ hasText: 'Account deletion' }).first()
  await expect(deletionRow.getByText('Scheduled', { exact: false })).toBeVisible()
  await deletionRow.getByRole('button', { name: 'Cancel request', exact: true }).click()
  await expect(deletionRow.getByText('Cancelled', { exact: false })).toBeVisible()
})

test('keeps privacy and Admin data-rights evidence usable on mobile', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const runID = Date.now().toString(36)
  const handle = `mobile_${runID}`
  const registration = await page.request.post('/api/v1/auth/register', {
    data: { email: `${handle}@example.test`, password: `Mobile-rights-${runID}`, handle, displayName: 'Mobile Rights', locale: 'en-US', timezone: 'UTC' },
  })
  expect(registration.status()).toBe(201)
  await page.goto('/settings?section=privacy')
  await expect(page.getByRole('heading', { name: 'Privacy and data', exact: true }).first()).toBeVisible()
  let widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto('/admin?tab=dataRights')
  await expect(page.getByRole('heading', { name: 'Data-rights queue', exact: true })).toBeVisible()
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})
