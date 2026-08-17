import { expect, test } from '@playwright/test'

const modes = [
  { mode: 'chat', label: 'Chat', contentType: 'text/plain', signature: 'Deterministic Local Test response', selector: 'pre' },
  { mode: 'image', label: 'Image', contentType: 'image/jpeg', signature: null, selector: 'img' },
  { mode: 'video', label: 'Video', contentType: 'video/mp4', signature: null, selector: 'video' },
  { mode: 'music', label: 'Music', contentType: 'audio/wav', signature: 'RIFF', selector: 'audio' },
] as const

test('creates Chat, Image, Video, and Music as typed, reusable Assets', async ({ page }) => {
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  const publishedTitles: string[] = []

  for (const item of modes) {
    const prompt = `E2E ${item.label} typed asset ${Date.now().toString(36)}`
    await page.goto(`/create/${item.mode}`)
    await expect(page.getByRole('heading', { name: new RegExp(item.label, 'i') })).toBeVisible()
    await page.getByLabel('Prompt', { exact: true }).fill(prompt)
    const actionLabel = item.mode === 'image' ? 'Generate image' : `Generate ${item.label}`
    await page.getByRole('button', { name: actionLabel, exact: true }).click()
    await expect(page.getByText('Saved to Assets', { exact: false })).toBeVisible()

    const publishHref = await page.getByRole('link', { name: 'Continue to publish', exact: true }).getAttribute('href')
    const assetID = new URL(publishHref!, 'http://127.0.0.1:5173').searchParams.get('assetId')
    expect(assetID).toBeTruthy()

    const content = await page.request.get(`/api/v1/assets/${assetID}/content`)
    expect(content.ok()).toBeTruthy()
    expect(content.headers()['content-type']).toContain(item.contentType)
    if (item.signature) {
      const bytes = await content.body()
      expect(bytes.toString('utf8')).toContain(item.signature)
    }

    await page.goto(`/workspace/assets/${assetID}`)
    await expect(page.getByRole('heading', { name: prompt, exact: true })).toBeVisible()
    await expect(page.locator(`.asset-detail-media ${item.selector}`)).toBeVisible()
    if (item.mode === 'video') {
      await expect.poll(() => page.locator('.asset-detail-media video').evaluate((element: HTMLVideoElement) => element.readyState)).toBeGreaterThan(0)
    }
    if (item.mode === 'music') {
      await expect.poll(() => page.locator('.asset-detail-media audio').evaluate((element: HTMLAudioElement) => element.readyState)).toBeGreaterThan(0)
    }

    const publishedTitle = `Published ${prompt}`
    publishedTitles.push(publishedTitle)
    await page.getByRole('link', { name: 'Publish asset', exact: true }).click()
    await page.getByLabel('Work title', { exact: true }).fill(publishedTitle)
    await page.getByLabel('Short description', { exact: true }).fill(`Verified ${item.label} creation and publication workflow.`)
    await page.getByLabel('Community note', { exact: true }).fill(`Published from the typed ${item.label} Asset.`)
    await page.getByRole('button', { name: 'Publish work', exact: true }).click()
    await expect(page.getByRole('heading', { name: publishedTitle, exact: true })).toBeVisible()
    await expect(page.locator(`.work-detail-media ${item.selector}`)).toBeVisible()
  }

  await page.goto('/community')
  for (const title of publishedTitles) {
    await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  }
})
