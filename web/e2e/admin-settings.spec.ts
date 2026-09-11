import { expect, test } from '@playwright/test'

test('applies and restores an audited platform generation gate', async ({ page }) => {
  const runID = Date.now().toString(36)

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect((await page.request.get('/api/v1/admin/settings')).status()).toBe(403)

	await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
	await page.goto('/admin?tab=settings')
	const settingsWorkspace = page.locator('.system-settings-admin')
	await expect(settingsWorkspace.getByRole('heading', { name: 'System settings', exact: true })).toBeVisible()
	await expect(settingsWorkspace.getByText('Revision name', { exact: true })).toHaveCount(0)
	await expect(settingsWorkspace.getByText('Settings history', { exact: true })).toHaveCount(0)
	await expect(settingsWorkspace.getByText(/^v\d+$/)).toHaveCount(0)

	const form = settingsWorkspace.locator('form')
	await form.getByRole('checkbox', { name: 'Generation submissions', exact: true }).uncheck()
	await form.getByRole('textbox', { name: 'Public notice', exact: true }).fill('Generation submissions are paused for bounded Local Test maintenance.')
	await form.getByRole('button', { name: 'Save system settings', exact: true }).click()
	await expect(page.getByText('System settings saved.', { exact: true })).toBeVisible()

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  const disabled = await page.request.post('/api/v1/generations', {
    headers: { 'Accept-Language': 'zh-CN', 'Idempotency-Key': `disabled-generation-${runID}` },
    data: { mode: 'image', prompt: `Disabled generation gate evidence ${runID}` },
  })
  expect(disabled.status()).toBe(503)
  expect(await disabled.json()).toMatchObject({ error: { code: 'feature_disabled' } })

	await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
	await page.goto('/admin?tab=settings')
	const restoreForm = page.locator('.system-settings-admin form')
	await restoreForm.getByRole('checkbox', { name: 'Generation submissions', exact: true }).check()
	await restoreForm.getByRole('textbox', { name: 'Public notice', exact: true }).fill('')
	await restoreForm.getByRole('button', { name: 'Save system settings', exact: true }).click()
	await expect(page.getByText('System settings saved.', { exact: true })).toBeVisible()

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  const restored = await page.request.post('/api/v1/generations', {
    headers: { 'Idempotency-Key': `restored-generation-${runID}` },
    data: { mode: 'image', prompt: `Restored generation gate evidence ${runID}` },
  })
  expect(restored.status()).toBe(202)

})
