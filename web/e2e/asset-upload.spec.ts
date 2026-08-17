import { expect, test } from '@playwright/test'

test('uploads, scans, inspects, and administratively reviews an Asset', async ({ page }) => {
  const runID = Date.now().toString(36)
  const title = `Uploaded source note ${runID}`
  const creatorSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(creatorSession.ok()).toBeTruthy()
  await page.goto('/workspace/assets')
  await page.getByRole('button', { name: 'Upload Asset', exact: true }).click()
  const uploadPanel = page.locator('.asset-upload-panel')
  await uploadPanel.getByLabel('Asset title', { exact: true }).fill(title)
  await uploadPanel.locator('input[type="file"]').setInputFiles({
    name: `source-${runID}.txt`, mimeType: 'text/plain', buffer: Buffer.from(`Local upload workflow evidence ${runID}`),
  })
  await uploadPanel.getByRole('button', { name: 'Upload and scan', exact: true }).click()
  await expect(page.getByText('Upload passed scanning and is ready to use.', { exact: true })).toBeVisible()

  const card = page.locator('.asset-card').filter({ hasText: title }).first()
  await expect(card).toBeVisible()
  await expect(card.getByText('Clean', { exact: true })).toBeVisible()
  await card.getByRole('link', { name: 'View details', exact: true }).click()
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Upload and scan evidence', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Publish asset', exact: true })).toBeVisible()
  const assetURL = page.url()

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto('/admin?tab=media')
  const mediaRow = page.locator('.media-admin-list article').filter({ hasText: title }).first()
  await expect(mediaRow).toBeVisible()
  await mediaRow.getByRole('button', { name: 'Review status', exact: true }).click()
  await page.locator('.admin-command-panel select').selectOption('rejected')
  await page.getByLabel('Required reason', { exact: true }).fill(`E2E ${runID}: reject this local upload to verify content access revocation.`)
  await page.getByLabel('I reviewed the target and confirm this operation.', { exact: true }).check()
  await page.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()

	const assetID = new URL(assetURL).pathname.split('/').at(-1)
	expect(assetID).toMatch(/^[0-9a-f-]{36}$/)
	await page.goto(`/admin?tab=risk&resourceType=asset&resourceId=${assetID}`)
  const mediaSignal = page.locator('.risk-admin-list article').filter({ hasText: title }).first()
  await expect(mediaSignal).toContainText('Media rejection')
  await expect(mediaSignal.getByRole('link', { name: title, exact: true })).toHaveAttribute('href', new URL(assetURL).pathname)

  const creatorAgain = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(creatorAgain.ok()).toBeTruthy()
  await page.goto(assetURL)
  await expect(page.getByText('Blocked', { exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Publish asset', exact: true })).toHaveCount(0)
})

test('keeps the upload panel usable on mobile', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const creatorSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(creatorSession.ok()).toBeTruthy()
  await page.goto('/workspace/assets')
  await page.getByRole('button', { name: 'Upload Asset', exact: true }).click()
  await expect(page.locator('.asset-upload-panel')).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})
