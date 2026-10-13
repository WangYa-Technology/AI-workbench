import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'

test('uploaded asset can be found with persisted title, media, source and scan filters', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  const title = `筛选验收 Garden ${Date.now().toString(36)}`
  await page.goto('/workspace/assets')
  await page.getByRole('button', { name: 'Upload Asset', exact: true }).click()
  await page.locator('.asset-upload-panel').getByLabel('Asset title', { exact: true }).fill(title)
  await page.locator('.asset-upload-panel input[type="file"]').setInputFiles({ name: 'search.txt', mimeType: 'text/plain', buffer: Buffer.from('Independent filter acceptance fixture.') })
  await page.locator('.asset-upload-panel').getByRole('button', { name: 'Upload and scan', exact: true }).click()
  await expect(page.getByText('Upload passed scanning and is ready to use.', { exact: true })).toBeVisible()
  const filters = page.locator('.asset-search-filters')
  await filters.getByLabel('Search titles', { exact: true }).fill(title)
  await chooseOption(filters.locator('select').nth(0), 'document')
  await chooseOption(filters.locator('select').nth(1), 'upload')
  await chooseOption(filters.locator('select').nth(2), 'clean')
  await filters.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(page.locator('.asset-row')).toHaveCount(1)
  await expect(page.locator('.asset-row')).toContainText(title)
  await expect(page.locator('.task-results-meta')).toContainText('1 Assets')
  await page.reload()
  await expect(page.locator('.asset-row')).toHaveCount(1)
  await expect(filters.getByLabel('Search titles', { exact: true })).toHaveValue(title)
  await chooseOption(filters.locator('select').nth(2), 'rejected')
  await filters.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(page.getByText('No matching results', { exact: true })).toBeVisible()
})
