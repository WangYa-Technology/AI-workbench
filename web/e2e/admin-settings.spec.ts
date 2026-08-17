import { expect, test } from '@playwright/test'

test('applies and restores an audited platform generation gate', async ({ page }) => {
  const runID = Date.now().toString(36)

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect((await page.request.get('/api/v1/admin/settings')).status()).toBe(403)

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  await page.goto('/admin?tab=settings')
  await expect(page.getByRole('heading', { name: 'System settings', exact: true })).toBeVisible()

  const form = page.locator('.system-settings-admin form')
  await form.getByRole('textbox', { name: 'Revision name', exact: true }).fill(`Generation maintenance ${runID}`)
  await form.getByRole('checkbox', { name: 'Generation submissions', exact: true }).uncheck()
  await form.getByRole('textbox', { name: 'Public notice', exact: true }).fill('Generation submissions are paused for bounded Local Test maintenance.')
  const disableReason = `Pause generation submissions while validating an audited Local Test maintenance gate ${runID}.`
  await form.getByRole('textbox', { name: 'Required reason', exact: true }).fill(disableReason)
  await form.getByRole('checkbox', { name: 'I reviewed every write gate and confirm this immutable platform revision.', exact: true }).check()
  await form.getByRole('button', { name: 'Activate revision', exact: true }).click()
  await expect(page.getByText('System settings revision activated with audit evidence.', { exact: true })).toBeVisible()

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
  await restoreForm.getByRole('textbox', { name: 'Revision name', exact: true }).fill(`Generation restored ${runID}`)
  await restoreForm.getByRole('checkbox', { name: 'Generation submissions', exact: true }).check()
  await restoreForm.getByRole('textbox', { name: 'Public notice', exact: true }).fill('')
  const restoreReason = `Restore generation submissions after completing bounded Local Test maintenance verification ${runID}.`
  await restoreForm.getByRole('textbox', { name: 'Required reason', exact: true }).fill(restoreReason)
  await restoreForm.getByRole('checkbox', { name: 'I reviewed every write gate and confirm this immutable platform revision.', exact: true }).check()
  await restoreForm.getByRole('button', { name: 'Activate revision', exact: true }).click()
  await expect(page.getByText('System settings revision activated with audit evidence.', { exact: true })).toBeVisible()

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  const restored = await page.request.post('/api/v1/generations', {
    headers: { 'Idempotency-Key': `restored-generation-${runID}` },
    data: { mode: 'image', prompt: `Restored generation gate evidence ${runID}` },
  })
  expect(restored.status()).toBe(202)

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  await page.goto('/admin?tab=audit')
  await expect(page.getByText('admin.system_settings_updated', { exact: true }).first()).toBeVisible()
  await expect(page.locator('.audit-list article').filter({ hasText: disableReason }).first()).toBeVisible()
  await expect(page.locator('.audit-list article').filter({ hasText: restoreReason }).first()).toBeVisible()
})
