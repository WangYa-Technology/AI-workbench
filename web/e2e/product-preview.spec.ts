import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

const productID = '00000000-0000-4000-8000-000000000503'
const endpoint = `/api/v1/products/${productID}`

for (const width of [390, 1308]) {
  test(`seller selects a separate public sample at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('studio') })).ok()).toBeTruthy()
    const original = await (await page.request.get(endpoint)).json()
    const requestedMedia: string[] = []
    page.on('request', request => {
      if (request.url().includes('/content')) requestedMedia.push(request.url())
    })
    try {
      await page.goto(`/market/assets/${productID}`)
      const candidates = page.waitForResponse(response => response.url().includes('/api/v1/assets?purpose=product_preview'))
      await page.getByRole('button', { name: 'Set preview', exact: true }).click()
      const eligible = await (await candidates).json()
      expect(eligible.items.some((asset: { id: string }) => asset.id === original.assetId)).toBe(false)
      expect(eligible.items.some((asset: { id: string }) => asset.id === original.previewAssetId)).toBe(true)
      await page.getByRole('combobox', { name: 'Public preview', exact: true }).click()
      await page.getByRole('option', { name: 'No preview', exact: true }).click()
      const removed = page.waitForResponse(response => response.url().endsWith(`${endpoint}/preview`) && response.request().method() === 'PUT')
      await page.getByRole('button', { name: 'Save public preview', exact: true }).click()
      expect((await removed).status()).toBe(200)
      await expect(page.locator('.product-detail-media .asset-media-unavailable')).toBeVisible()
      expect((await (await page.request.get(endpoint)).json()).mediaUrl).toBe('')
      expect(requestedMedia.some(url => url.includes(`/assets/${original.assetId}/content`))).toBe(false)

      await page.getByRole('button', { name: 'Set preview', exact: true }).click()
      const save = page.getByRole('button', { name: 'Save public preview', exact: true })
      await expect(save).toBeEnabled()
      await page.getByRole('combobox', { name: 'Public preview', exact: true }).click()
      await page.getByRole('option', { name: 'Signal Architecture', exact: true }).click()
      const restored = page.waitForResponse(response => response.url().endsWith(`${endpoint}/preview`) && response.request().method() === 'PUT')
      await save.click()
      expect((await restored).status()).toBe(200)
      await expect(page.locator('.product-detail-media img')).toBeVisible()
      const updated = await (await page.request.get(endpoint)).json()
      expect(updated.previewAssetId).toBe(original.previewAssetId)
      expect(updated.mediaUrl).not.toContain(original.assetId)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    } finally {
      const current = await (await page.request.get(endpoint)).json()
      expect((await page.request.put(`${endpoint}/preview`, { data: { previewAssetId: original.previewAssetId ?? null, offerVersion: current.offerVersion } })).ok()).toBeTruthy()
    }
  })
}

test('public catalog renders each sample kind and never offers seller controls', async ({ page }) => {
  const catalog = await (await page.request.get('/api/v1/products')).json()
  const base = catalog.items[0]
  await page.route('**/preview-sample.txt', route => route.fulfill({ contentType: 'text/plain', body: 'Public excerpt only' }))
  await page.route(/\/api\/v1\/products(?:\?.*)?$/, route => route.fulfill({ json: {
    ...catalog,
    total: 4,
    nextCursor: undefined,
    items: ['image', 'video', 'audio', 'document'].map((kind, index) => ({
      ...base, id: `preview-${index}`, title: `Sample ${kind}`, mediaKind: kind,
      mediaUrl: kind === 'document' ? '/preview-sample.txt' : base.mediaUrl,
    })),
  } }))
  await page.goto('/market')
  const cards = page.locator('.ui-catalog .ui-content-card')
  await expect(cards).toHaveCount(4)
  await expect(cards.nth(0).locator('.asset-renderer img')).toBeVisible()
  // The video fixture reuses image bytes: assert the correct renderer, not playback.
  await expect(cards.nth(1).locator('.asset-renderer')).toHaveAttribute('data-kind', 'video')
  await expect(cards.nth(2).locator('.asset-audio')).toBeVisible()
  await expect(cards.nth(3).locator('.asset-document pre')).toHaveText('Public excerpt only')
  await page.goto(`/market/assets/${productID}`)
  await expect(page.locator('.product-detail-media')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Set preview', exact: true })).toHaveCount(0)
})
