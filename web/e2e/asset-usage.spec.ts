import { expect, test } from '@playwright/test'

test('traces an Asset into downstream creation and publication usage', async ({ page }) => {
  const runID = Date.now().toString(36)
  const sourcePrompt = `Asset usage source ${runID}`
  const downstreamPrompt = `Asset usage downstream creation ${runID}`
  const workTitle = `Asset usage published Work ${runID}`

  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  await page.goto('/create/image')
  await page.getByLabel('Prompt', { exact: true }).fill(sourcePrompt)
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()
  await expect(page.getByText('Saved to Assets', { exact: false })).toBeVisible()
  const publishHref = await page.getByRole('link', { name: 'Continue to publish', exact: true }).getAttribute('href')
  const sourceAssetID = new URL(publishHref!, 'http://127.0.0.1:5173').searchParams.get('assetId')
  expect(sourceAssetID).toBeTruthy()

  await page.goto(`/create/image?sourceAssetId=${sourceAssetID}`)
  await expect(page.locator('.source-reference').filter({ hasText: sourcePrompt })).toBeVisible()
  await page.getByLabel('Prompt', { exact: true }).fill(downstreamPrompt)
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()
  await expect(page.getByText('Saved to Assets', { exact: false })).toBeVisible()

  await page.goto(`/publish?assetId=${sourceAssetID}`)
  await page.getByLabel('Work title', { exact: true }).fill(workTitle)
  await page.getByLabel('Short description', { exact: true }).fill('Published evidence for the downstream Asset usage graph.')
  await page.getByLabel('Community note', { exact: true }).fill(`Usage graph verification ${runID}.`)
  await page.getByRole('button', { name: 'Publish work', exact: true }).click()
  await expect(page.getByRole('heading', { name: workTitle, exact: true })).toBeVisible()

  await page.goto(`/workspace/assets/${sourceAssetID}`)
  await expect(page.getByRole('heading', { name: 'Used across HCAI', exact: true })).toBeVisible()
  const creationUsage = page.locator('.usage-lineage').filter({ hasText: downstreamPrompt })
  await expect(creationUsage).toContainText('Creation source')
  await expect(creationUsage).toContainText('Asset v1')
  await expect(page.locator('.usage-lineage').filter({ hasText: workTitle })).toContainText('Published')

  await creationUsage.click()
  await expect(page).toHaveURL(/\/workspace\/generations\?generationId=[0-9a-f-]+$/)
  const focusedGeneration = page.locator('.generation-row.usage-focus')
  await expect(focusedGeneration).toContainText(downstreamPrompt)
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})
