import { expect, test } from '@playwright/test'

const seededAssetID = '00000000-0000-4000-8000-000000000102'

test('publishes from an individual asset drawer instead of a global page', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  await page.goto('/workspace/assets')
  await expect(page.getByLabel('Primary navigation').getByRole('link', { name: 'Publish work', exact: true })).toHaveCount(0)
  await expect(page.locator('.header-actions').getByRole('link', { name: 'Publish', exact: true })).toHaveCount(0)

  const assetRow = page.locator('.asset-row').filter({ hasText: 'Creator delivery study' })
  const publishButton = assetRow.getByRole('button', { name: 'Publish asset', exact: true })
  await expect(publishButton).toBeVisible()
  await publishButton.click()

  await expect(page).toHaveURL(new RegExp(`/workspace/assets\\?publish=${seededAssetID}`))
  const drawer = page.getByRole('dialog', { name: 'Publish work' })
  await expect(drawer).toBeVisible()
  await expect(drawer.getByLabel('Source Asset').getByText('Creator delivery study', { exact: true })).toBeVisible()
  await expect(drawer.getByLabel('Work title', { exact: true })).toHaveValue('Creator delivery study')
  await expect(drawer.getByLabel('Asset', { exact: true })).toHaveCount(0)

  await drawer.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(drawer).toHaveCount(0)
  await expect(page).toHaveURL('/workspace/assets')

  await page.goto(`/publish?assetId=${seededAssetID}`)
  await expect(page).toHaveURL(new RegExp(`/workspace/assets\\?publish=${seededAssetID}`))
  const legacyDrawer = page.getByRole('dialog', { name: 'Publish work' })
  await expect(legacyDrawer).toBeVisible()
  const publishedTitle = `Published from asset drawer ${Date.now().toString(36)}`
  await legacyDrawer.getByLabel('Work title', { exact: true }).fill(publishedTitle)
  await legacyDrawer.getByLabel('Short description', { exact: true }).fill('Published from the selected asset without a standalone workflow page.')
  await legacyDrawer.getByRole('button', { name: 'Publish work', exact: true }).click()
  await expect(page).toHaveURL(/\/works\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: publishedTitle, exact: true })).toBeVisible()
})

test('uses a full-width asset publishing drawer on mobile', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto(`/workspace/assets?publish=${seededAssetID}`)

  const drawer = page.getByRole('dialog', { name: 'Publish work' })
  await expect(drawer).toBeVisible()
  const bounds = await drawer.boundingBox()
  expect(bounds?.x).toBe(0)
  expect(bounds?.width).toBe(390)
  const layout = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(layout.scroll).toBe(layout.client)
})
