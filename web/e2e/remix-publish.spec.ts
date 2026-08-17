import { expect, test } from '@playwright/test'

const seededWorkID = '00000000-0000-4000-8000-000000000201'

test('remixes a discovered work, saves an asset, and publishes to Community', async ({ page }) => {
  const runID = Date.now().toString(36)
  const title = `E2E Coral Observatory ${runID}`
  const prompt = `Editorial observatory above a quiet coast, coral sunrise, local E2E run ${runID}`

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto(`/works/${seededWorkID}`)
  await expect(page.getByRole('heading', { name: 'Signal Architecture' })).toBeVisible()
  await page.getByRole('link', { name: 'Remix', exact: true }).click()

  await expect(page).toHaveURL(new RegExp(`/create/image\\?sourceWorkId=${seededWorkID}`))
  const promptField = page.getByLabel('Prompt', { exact: true })
  await expect(promptField).toHaveValue(/Cinematic architectural photography/)
  await promptField.fill(prompt)
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()

  await expect(page.getByText('Saved to Assets', { exact: false })).toBeVisible()
  await page.getByRole('link', { name: 'Continue to publish', exact: true }).click()
  await expect(page).toHaveURL(/\/publish\?assetId=/)

  await page.getByLabel('Work title', { exact: true }).fill(title)
  await page.getByLabel('Short description', { exact: true }).fill('A verified local workflow from inspiration to a published asset.')
  await page.getByLabel('Community note', { exact: true }).fill('Published by the first durable generation workflow E2E test.')
  await page.getByRole('button', { name: 'Publish work', exact: true }).click()

  await expect(page).toHaveURL(/\/works\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  await expect(page.locator('.prompt-text')).toContainText(prompt)
  await expect(page.locator('.prompt-text')).toContainText('Tone: precise')

  await page.getByLabel('Primary navigation').getByRole('link', { name: 'Community', exact: true }).click()
  await expect(page).toHaveURL(/\/community$/)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
})
