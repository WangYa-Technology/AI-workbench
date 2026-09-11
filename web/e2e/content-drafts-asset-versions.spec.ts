import { expect, test } from '@playwright/test'

test('versions an Asset and publishes a restored server draft', async ({ page }) => {
  const runID = Date.now().toString(36)
  const v1Title = `Draft source ${runID}`
  const v2Title = `Draft source revised ${runID}`
  const workTitle = `Persisted draft work ${runID}`
  const restoredSummary = `Restored from PostgreSQL for ${runID}.`
  const publishedSummary = `Updated server draft version for ${runID}.`

  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  await page.goto('/workspace/assets')
  await page.getByRole('button', { name: 'Upload Asset', exact: true }).click()
  const uploadPanel = page.locator('.asset-upload-panel')
  await uploadPanel.getByLabel('Asset title', { exact: true }).fill(v1Title)
  await uploadPanel.locator('input[type="file"]').setInputFiles({
    name: `draft-source-${runID}.txt`,
    mimeType: 'text/plain',
    buffer: Buffer.from(`Durable draft source v1 ${runID}`),
  })
  await uploadPanel.getByRole('button', { name: 'Upload and scan', exact: true }).click()
  await expect(page.getByText('Upload passed scanning and is ready to use.', { exact: true })).toBeVisible()

  const v1Card = page.locator('.asset-card').filter({ hasText: v1Title }).first()
  await v1Card.getByRole('link', { name: 'View details', exact: true }).click()
  await page.getByRole('button', { name: 'Upload new version', exact: true }).click()
  const versionForm = page.locator('.asset-version-form')
  await versionForm.getByLabel('Version title', { exact: true }).fill(v2Title)
  await versionForm.getByLabel('What changed', { exact: true }).fill('Replaced the initial copy with the reviewed publication source.')
  await versionForm.locator('input[type="file"]').setInputFiles({
    name: `draft-source-v2-${runID}.txt`,
    mimeType: 'text/plain',
    buffer: Buffer.from(`Durable draft source v2 ${runID}`),
  })
  const v1URL = page.url()
  await versionForm.getByRole('button', { name: 'Upload version and scan', exact: true }).click()
  await expect(page).not.toHaveURL(v1URL)
  await expect(page).toHaveURL(/\/workspace\/assets\/[0-9a-f-]+$/)

  const v2ID = page.url().split('/').pop()!
  await expect.poll(async () => {
    const response = await page.request.get(`/api/v1/assets/${v2ID}`)
    if (!response.ok()) return 'request_failed'
    return ((await response.json()) as { scanStatus: string }).scanStatus
  }).toBe('clean')
  await page.reload()
  await expect(page.getByRole('heading', { name: v2Title, exact: true })).toBeVisible()
  await expect(page.locator('.asset-version-block')).toContainText('v2 · Clean')
  await expect(page.locator('.asset-version-block')).toContainText('v1 · Clean')

  await page.getByRole('button', { name: 'Publish asset', exact: true }).click()
  await page.getByLabel('Work title', { exact: true }).fill(workTitle)
  await page.getByLabel('Short description', { exact: true }).fill(restoredSummary)
  await page.getByLabel('Community note', { exact: true }).fill(`Private draft publication note ${runID}.`)
  await page.getByLabel('Prompt', { exact: true }).fill(`Durable content draft prompt ${runID}`)
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page.getByText('Private draft saved with a new version.', { exact: true })).toBeVisible()
  await expect(page).toHaveURL(/draftId=[0-9a-f-]+/)

  await page.reload()
  await expect(page.getByLabel('Work title', { exact: true })).toHaveValue(workTitle)
  await expect(page.getByLabel('Short description', { exact: true })).toHaveValue(restoredSummary)
  await expect(page.getByLabel('Prompt', { exact: true })).toHaveValue(`Durable content draft prompt ${runID}`)

  await page.getByLabel('Short description', { exact: true }).fill(publishedSummary)
  await page.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect(page.locator('#asset-publish-draft option:checked')).toContainText('v2')
  await page.getByRole('button', { name: 'Publish work', exact: true }).click()

  await expect(page).toHaveURL(/\/works\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: workTitle, exact: true })).toBeVisible()
  await expect(page.getByText(publishedSummary, { exact: true })).toBeVisible()
  await page.getByLabel('Primary navigation').getByRole('link', { name: 'Community', exact: true }).click()
  await expect(page.getByRole('heading', { name: workTitle, exact: true })).toBeVisible()
})
