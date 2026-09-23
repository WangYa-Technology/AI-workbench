import { expect, test } from '@playwright/test'

const workId = '00000000-0000-4000-8000-000000000201'

test('shared text preview stays bounded when an origin ignores Range', async ({ page }) => {
  const work = await (await page.request.get(`/api/v1/works/${workId}`)).json()
  await page.route(`**/api/v1/works/${workId}`, route => route.fulfill({ json: {
    ...work, mediaKind: 'document', mediaUrl: '/preview-ignores-range.txt',
  } }))
  let range = ''
  await page.route('**/preview-ignores-range.txt', route => {
    range = route.request().headers()['range'] || ''
    return route.fulfill({ contentType: 'text/plain; charset=utf-8', body: '中'.repeat(100_000) + 'END-OF-FILE' })
  })
  await page.goto(`/works/${workId}`)
  const preview = page.locator('.asset-document')
  await expect(preview.locator('pre')).toHaveText('中'.repeat(21845))
  expect(range).toBe('bytes=0-65536')
  await expect(preview.locator('.asset-text-preview-note')).toBeVisible()
  await expect(preview).not.toContainText('END-OF-FILE')
})

test('a stalled text preview times out and a later successful visit can recover', async ({ page }) => {
  await page.clock.install()
  const work = await (await page.request.get(`/api/v1/works/${workId}`)).json()
  await page.route(`**/api/v1/works/${workId}`, route => route.fulfill({ json: {
    ...work, mediaKind: 'document', mediaUrl: '/stalled-preview.txt',
  } }))
  let waiting = false
  let delayed = true
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route('**/stalled-preview.txt', async route => {
    if (delayed) { waiting = true; await gate }
    await route.fulfill({ contentType: 'text/plain', body: 'Recovered text preview' }).catch(() => undefined)
  })
  try {
    await page.goto(`/works/${workId}`)
    await expect.poll(() => waiting).toBe(true)
    await page.clock.runFor(15_100)
    await expect(page.locator('.asset-document [role="alert"]')).toHaveText('This media result is unavailable.')
    delayed = false
    release()
    await page.reload()
    await expect(page.locator('.asset-document pre')).toHaveText('Recovered text preview')
    await expect(page.locator('.asset-document [role="alert"]')).toHaveCount(0)
    await expect(page.locator('.asset-text-preview-note')).toHaveCount(0)
  } finally { release() }
})
