import { fixtureCredentials } from './helpers/identity'
import { chooseOption, expectSelection } from './helpers/select'
import { expect, test } from '@playwright/test'

test('restores Admin content and media filters and reloads the active queue after review', async ({ page }) => {
  const runID = Date.now().toString(36)
  const mediaTitle = `Filtered media evidence ${runID}`

  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/workspace/assets')
  await page.getByRole('button', { name: 'Upload Asset', exact: true }).click()
  const uploadPanel = page.locator('.asset-upload-panel')
  await uploadPanel.getByLabel('Asset title', { exact: true }).fill(mediaTitle)
  await uploadPanel.locator('input[type="file"]').setInputFiles({
    name: `filtered-${runID}.txt`, mimeType: 'text/plain', buffer: Buffer.from(`Filtered Admin media evidence ${runID}`),
  })
  await uploadPanel.getByRole('button', { name: 'Upload and scan', exact: true }).click()
  await expect(page.getByText('Upload passed scanning and is ready to use.', { exact: true })).toBeVisible()

  const adminSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect(adminSession.ok()).toBeTruthy()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/admin?tab=content&q=Signal&type=work&status=published')
  await expect(page.getByRole('searchbox', { name: 'Search content', exact: true })).toHaveValue('Signal')
  await expectSelection(page.getByRole('combobox', { name: 'Resource type', exact: true }), 'work')
  await expectSelection(page.getByRole('combobox', { name: 'Status', exact: true }).last(), 'published')
  const contentRow = page.locator('.admin-content-directory .admin-list article').filter({ hasText: 'Signal Architecture' })
  await expect(contentRow).toHaveCount(1)
  await expect(page).toHaveURL(/tab=content.*q=Signal.*type=work.*status=published/)

  await contentRow.getByRole('button', { name: 'Review status', exact: true }).click()
  const contentCommand = page.locator('.admin-command-panel')
  await chooseOption(contentCommand.getByRole('combobox', { name: 'Status', exact: true }), 'published')
  await contentCommand.getByLabel('Decision reason', { exact: true }).fill('Reviewed the source and current publication state.')
  await contentCommand.getByRole('checkbox', { name: 'I have reviewed the content and confirm this decision' }).check()
  await contentCommand.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await expect(contentRow).toHaveCount(1)

  await page.goto('/admin?tab=media')
  await expect(page).toHaveURL(/\/admin\?tab=media$/)
  await page.goto(`/admin?tab=media&q=${runID}&kind=document&status=clean`)
  await expect(page.getByRole('searchbox', { name: 'Search media', exact: true })).toHaveValue(runID)
  await expectSelection(page.getByRole('combobox', { name: 'Media type', exact: true }), 'document')
  await expectSelection(page.getByRole('combobox', { name: 'Scan decision', exact: true }).last(), 'clean')
  const mediaRow = page.locator('.media-admin-list article').filter({ hasText: mediaTitle })
  await expect(mediaRow).toHaveCount(1)
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await mediaRow.getByRole('button', { name: 'Review status', exact: true }).click()
  const mediaCommand = page.locator('.admin-command-panel')
  await chooseOption(mediaCommand.getByRole('combobox', { name: 'Scan decision', exact: true }), 'rejected')
  await mediaCommand.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await expect(mediaRow).toHaveCount(0)
})
