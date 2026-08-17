import { expect, test } from '@playwright/test'

test('controls, issues, authenticates, rotates, and revokes a hash-only Developer API key', async ({ page }) => {
  const suffix = Date.now().toString(36)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  const initialResponse = await page.request.get('/api/v1/admin/developer/access')
  expect(initialResponse.ok()).toBeTruthy()
  const initial = await initialResponse.json()
  const reason = `Enable bounded browser Developer Access verification ${suffix}.`
  const enabled = await page.request.put('/api/v1/admin/developer/control', { data: {
    enabled: true,
    maxServiceAccounts: initial.control.maxServiceAccounts,
    maxActiveKeys: initial.control.maxActiveKeys,
    defaultTtlDays: initial.control.defaultTtlDays,
    expectedVersion: initial.control.version,
    reason,
    confirmed: true,
  } })
  expect(enabled.ok()).toBeTruthy()

  await page.request.post('/api/v1/auth/logout')
  const registered = await page.request.post('/api/v1/auth/register', { data: {
    email: `developer_${suffix}@example.test`,
    password: 'developer-contract-2026',
    handle: `dev_${suffix}`,
    displayName: 'Developer Contract',
    locale: 'en-US',
    timezone: 'America/New_York',
  } })
  expect(registered.status()).toBe(201)

  await page.goto('/settings?section=developer')
  await expect(page.getByRole('heading', { name: 'Developer Access' })).toBeVisible()
  await page.getByLabel('Service Account name').fill(`Render pipeline ${suffix}`)
  await page.getByRole('button', { name: 'Create Service Account' }).click()
  const account = page.locator('.developer-account-row').filter({ hasText: `Render pipeline ${suffix}` })
  await expect(account).toBeVisible()
  await account.getByRole('button', { name: 'Issue API key' }).click()
  const secret = await page.locator('.developer-secret code').textContent()
  expect(secret).toMatch(/^hcai_sk_[0-9a-f]{12}_/)

  const principal = await page.request.get('/api/v1/principal', { headers: { Authorization: `Bearer ${secret}` } })
  expect(principal.status()).toBe(200)
  expect(principal.headers()['x-api-version']).toBe('v1')
  const principalBody = await principal.json()
  expect(principalBody.data.ownerHandle).toBe(`dev_${suffix}`)
  expect(principalBody.meta.requestId).toBeTruthy()

  await page.getByLabel('Operation reason').fill(`Rotate the bounded browser credential ${suffix}.`)
  await page.getByLabel(/I confirm this rotation or revocation/).check()
  await account.locator('.developer-key-row').filter({ hasText: 'Active' }).getByRole('button', { name: 'Rotate key' }).click()
  const replacement = await page.locator('.developer-secret code').textContent()
  expect(replacement).not.toBe(secret)
  expect((await page.request.get('/api/v1/principal', { headers: { Authorization: `Bearer ${secret}` } })).status()).toBe(401)
  expect((await page.request.get('/api/v1/principal', { headers: { Authorization: `Bearer ${replacement}` } })).status()).toBe(200)

  await page.getByLabel('Operation reason').fill(`Revoke the completed browser credential ${suffix}.`)
  await page.getByLabel(/I confirm this rotation or revocation/).check()
  const selfRevokeResponse = page.waitForResponse(response => response.request().method() === 'POST' && response.url().includes('/developer-service-accounts/') && response.url().endsWith('/revoke'))
  await account.locator('.developer-key-row').filter({ hasText: 'Active' }).getByRole('button', { name: 'Revoke' }).click()
  expect((await selfRevokeResponse).ok()).toBeTruthy()
  expect((await page.request.get('/api/v1/principal', { headers: { Authorization: `Bearer ${replacement}` } })).status()).toBe(401)

  const emergencyIssueResponse = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/keys'))
  await account.getByRole('button', { name: 'Issue API key' }).click()
  expect((await emergencyIssueResponse).status()).toBe(201)
  const emergencySecret = await page.locator('.developer-secret code').textContent()
  expect(emergencySecret).toMatch(/^hcai_sk_[0-9a-f]{12}_/)
  expect(emergencySecret).not.toBe(replacement)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  await page.goto('/admin?tab=developer')
  await expect(page.getByRole('heading', { name: 'Developer Access control' })).toBeVisible()
  const adminAccount = page.locator('.developer-admin-account').filter({ hasText: `Render pipeline ${suffix}` })
  const emergencyForm = page.locator('.developer-emergency-form')
  await emergencyForm.getByLabel('Reason').fill(`Revoke the exposed browser credential ${suffix}.`)
  await emergencyForm.getByLabel(/selected credential must lose access immediately/).check()
  await adminAccount.locator('.developer-admin-key').filter({ hasText: 'Active' }).getByRole('button', { name: /Revoke API key/ }).click()
  await expect(page.getByText('API key was revoked with audit evidence.')).toBeVisible()
  expect((await page.request.get('/api/v1/principal', { headers: { Authorization: `Bearer ${emergencySecret}` } })).status()).toBe(401)

  await emergencyForm.getByLabel('Reason').fill(`Revoke the browser Service Account after incident ${suffix}.`)
  await emergencyForm.getByLabel(/selected credential must lose access immediately/).check()
  await adminAccount.getByRole('button', { name: `Revoke Service Account Render pipeline ${suffix}` }).click()
  await expect(page.getByText('Service Account and its active keys were revoked with audit evidence.')).toBeVisible()

  await page.goto('/admin?tab=audit')
  await expect(page.getByText('developer.api_key_rotated', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('developer.api_key_revoked', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('admin.developer_api_key_revoked', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('admin.developer_service_account_revoked', { exact: true }).first()).toBeVisible()

  const current = await (await page.request.get('/api/v1/admin/developer/access')).json()
  const restored = await page.request.put('/api/v1/admin/developer/control', { data: {
    enabled: initial.control.enabled,
    maxServiceAccounts: initial.control.maxServiceAccounts,
    maxActiveKeys: initial.control.maxActiveKeys,
    defaultTtlDays: initial.control.defaultTtlDays,
    expectedVersion: current.control.version,
    reason: `Restore Developer Access after browser verification ${suffix}.`,
    confirmed: true,
  } })
  expect(restored.ok()).toBeTruthy()
})
