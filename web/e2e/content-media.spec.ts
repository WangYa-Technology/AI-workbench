import { expect, test } from '@playwright/test'

const workId = '00000000-0000-4000-8000-000000000201'
const longTitle = 'LongUnbrokenTitle'.repeat(15)
const longPrompt = 'A detailed composition with careful light and clear geometry. '.repeat(150)
const wav = Buffer.alloc(16044)
wav.write('RIFF'); wav.writeUInt32LE(wav.length - 8, 4); wav.write('WAVEfmt ', 8)
wav.writeUInt32LE(16, 16); wav.writeUInt16LE(1, 20); wav.writeUInt16LE(1, 22)
wav.writeUInt32LE(8000, 24); wav.writeUInt32LE(16000, 28); wav.writeUInt16LE(2, 32)
wav.writeUInt16LE(16, 34); wav.write('data', 36); wav.writeUInt32LE(16000, 40)

for (const theme of ['light', 'dark']) for (const width of [390, 768, 1308, 1551]) for (const kind of ['image', 'video', 'audio', 'document', 'empty']) {
  test(`work ${kind}, ${theme}, ${width}: long content stays readable`, async ({ page }) => {
    await page.setViewportSize({ width, height: 901 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.addInitScript(theme => localStorage.setItem('hcai-theme', theme), theme)
    const work = await (await page.request.get(`/api/v1/works/${workId}`)).json()
    await page.route('**/audit-document.txt', route => route.fulfill({ contentType: 'text/plain', body: longPrompt }))
    await page.route('**/audit-audio.wav', route => route.fulfill({ contentType: 'audio/wav', body: wav }))
    const mediaUrl = kind === 'video' ? '/media/local-video-test.mp4' : kind === 'audio' ? '/audit-audio.wav' : kind === 'document' ? '/audit-document.txt' : kind === 'empty' ? '' : work.mediaUrl
    await page.route(`**/api/v1/works/${workId}`, route => route.fulfill({ json: { ...work, title: longTitle, prompt: longPrompt, mediaKind: kind === 'empty' ? 'image' : kind, mediaUrl } }))
    await page.goto(`/works/${workId}`)
    await expect(page.getByRole('heading', { name: longTitle, exact: true })).toBeVisible()
    await expect(page.locator('.prompt-text')).toHaveText(longPrompt.trim())
    if (kind === 'video' || kind === 'audio') {
      const media = page.locator(`.work-detail-media ${kind}`)
      await expect(media).toBeVisible()
      await expect.poll(() => media.evaluate((node: HTMLMediaElement) => node.readyState)).toBeGreaterThan(0)
    } else if (kind === 'document') await expect(page.locator('.asset-document pre')).toHaveText(longPrompt.trim())
    else if (kind === 'empty') {
      await expect(page.locator('.asset-media-unavailable')).toBeVisible()
      await expect(page.locator('.work-full-preview')).toHaveCount(0)
    }
    const overflow = await page.locator('.work-detail-grid').evaluate(root => [root, ...root.querySelectorAll('header, aside, .detail-block, .prompt-text, .asset-renderer')].filter(node => node.scrollWidth > node.clientWidth + 2).map(node => node.className))
    expect(overflow).toEqual([])
    if (theme === 'light' && width === 390 && kind === 'image') {
      await page.getByRole('button', { name: 'Expand prompt', exact: true }).click()
      await expect(page.getByRole('button', { name: 'Collapse prompt', exact: true })).toHaveAttribute('aria-expanded', 'true')
      expect(await page.locator('.prompt-text').evaluate(node => node.clientHeight)).toBeGreaterThan(280)
      await page.getByRole('button', { name: 'Collapse prompt', exact: true }).click()
      expect(await page.locator('.prompt-text').evaluate(node => node.clientHeight)).toBeLessThanOrEqual(280)
    }
  })
}

test('denied work shows a recoverable error without remix controls', async ({ page }) => {
  await page.route(`**/api/v1/works/${workId}`, route => route.fulfill({ status: 403, json: { error: { code: 'forbidden', message: 'Access denied' } } }))
  await page.goto(`/works/${workId}`)
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Try again', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Remix', exact: true })).toHaveCount(0)
})

test('failed image uses a readable fallback', async ({ page }) => {
  const work = await (await page.request.get(`/api/v1/works/${workId}`)).json()
  await page.route('**/missing-audit-image.png', route => route.fulfill({ status: 404, body: '' }))
  await page.route(`**/api/v1/works/${workId}`, route => route.fulfill({ json: { ...work, mediaUrl: '/missing-audit-image.png' } }))
  await page.goto(`/works/${workId}`)
  await expect(page.locator('.asset-media-unavailable')).toBeVisible()
  await expect(page.locator('.work-detail-media img')).toHaveCount(0)
})

for (const width of [390, 768, 1308, 1551]) test(`long post and product content at ${width}`, async ({ page }) => {
  await page.setViewportSize({ width, height: 901 })
  const postId = '00000000-0000-4000-8000-000000000301'
  const post = await (await page.request.get(`/api/v1/community/posts/${postId}`)).json()
  await page.route(`**/api/v1/community/posts/${postId}`, route => route.fulfill({ json: { ...post, title: longTitle, authorName: 'Author'.repeat(12), body: `## Long discussion\n\n${longPrompt}\n\n\`\`\`text\n${longTitle}\n\`\`\``, workId: null } }))
  await page.goto(`/community/posts/${postId}`)
  await expect(page.locator('.community-post-body pre')).toBeVisible()
  expect(await page.locator('.community-post-layout').evaluate(node => node.scrollWidth > node.clientWidth + 2)).toBe(false)
  await expect(page.locator('.community-post-context')).toHaveCount(0)

  const productId = '00000000-0000-4000-8000-000000000501'
  const product = await (await page.request.get(`/api/v1/products/${productId}`)).json()
  await page.route(`**/api/v1/products/${productId}`, route => route.fulfill({ json: { ...product, title: longTitle, description: longPrompt, mediaUrl: '' } }))
  await page.goto(`/market/assets/${productId}`)
  await expect(page.locator('.asset-media-unavailable')).toBeVisible()
  expect(await page.locator('.product-detail-layout').evaluate(node => node.scrollWidth > node.clientWidth + 2)).toBe(false)
})
