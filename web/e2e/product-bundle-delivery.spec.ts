import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

// Presentation test with accepted-delivery API fixtures. The Go HTTP and
// payment suites exercise actual bytes/rights and the public purchase path.
for (const width of [390, 1308]) {
  test(`purchased package uses shared file actions at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    const response = await page.request.get('/api/v1/assets')
    expect(response.ok()).toBeTruthy()
    const list = await response.json()
    expect(list.items.length).toBeGreaterThan(0)
    const original = await (await page.request.get(`/api/v1/assets/${list.items[0].id}`)).json()
    const id = original.id
    let available = true
    let automaticPackageReads = 0
    const files = [
      { name: 'workflow.txt', mimeType: 'text/plain', sizeBytes: 64, sha256: 'a'.repeat(64) },
      { name: '详细使用说明与完整授权范围-creative-resource-instructions-and-license.txt', mimeType: 'text/plain', sizeBytes: 128, sha256: 'b'.repeat(64) },
    ]
    await page.route(`**/api/v1/assets/${id}`, route => route.fulfill({ json: {
      ...original, title: 'Purchased creative package', kind: 'document', mimeType: 'application/zip', width: null, height: null,
      sourceType: 'purchase', mediaUrl: `/api/v1/assets/${id}/content`, originAssetId: null, sizeBytes: 1024,
      provenance: { purchase: { orderId: id, productId: id, productTitle: 'Licensed creative package', licenseCode: 'hcai-commercial-standard-v1',
        licenseName: 'HCAI Commercial Standard', orderStatus: available ? 'fulfilled' : 'refunded', grantedAt: '2026-09-20T00:00:00Z', paymentMode: 'stripe', realCharge: false,
        canDownload: available, canReuse: false, delivery: { format: 'zip-v1', sizeBytes: 1024, sha256: 'c'.repeat(64), files },
      } },
    } }))
    await page.route(`**/api/v1/assets/${id}/content?fileIndex=1`, route => route.fulfill({
      contentType: 'text/plain', headers: { 'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(files[1]!.name)}`, 'Cache-Control': 'private, no-store' }, body: 'Accepted member fixture',
    }))
    await page.route(`**/api/v1/assets/${id}/content`, route => {
      automaticPackageReads++
      return route.fulfill({ contentType: 'application/zip', body: 'Binary package must not be text-previewed' })
    })
    await page.goto(`/workspace/assets/${id}`)
    const manifest = page.locator('.ui-data-list[aria-label="Delivery files"]')
    await expect(manifest).toBeVisible()
    await expect(page.locator('.asset-renderer[data-kind="package"]')).toBeVisible()
    await expect(page.locator('.asset-renderer pre')).toHaveCount(0)
    expect(automaticPackageReads).toBe(0)
    await expect(manifest.locator('.ui-card-actions')).toHaveCount(2)
    for (const [index, file] of files.entries()) {
      await expect(manifest.getByRole('link', { name: `Download ${file.name}`, exact: true })).toHaveAttribute('href', `/api/v1/assets/${id}/content?fileIndex=${index}`)
    }
    await expect(page.getByRole('link', { name: 'Download complete package', exact: true })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Use in Create', exact: true })).toHaveCount(0)
    await manifest.scrollIntoViewIfNeeded()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/hcai-bundle-delivery-${width}.png`, fullPage: true })
    const downloaded = page.waitForEvent('download')
    await manifest.getByRole('link', { name: `Download ${files[1]!.name}`, exact: true }).click()
    expect((await downloaded).suggestedFilename()).toBe(files[1]!.name)
    available = false
    await page.reload()
    await expect(manifest.locator('.ui-card-actions')).toHaveCount(2)
    await expect(manifest.getByRole('link')).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'Download complete package', exact: true })).toHaveCount(0)
  })
}
