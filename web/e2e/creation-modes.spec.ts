import { expect, test } from '@playwright/test'

const modes = [
  { mode: 'chat', label: 'Chat', view: 'Responses', contentType: 'text/plain', signature: 'Deterministic Local Test response', selector: 'pre', settingsLabel: 'Response length', accept: 'text/plain,.txt,.md' },
  { mode: 'image', label: 'Image', view: 'Gallery', contentType: 'image/jpeg', signature: null, selector: 'img', settingsLabel: 'Aspect ratio', accept: 'image/jpeg,image/png' },
  { mode: 'video', label: 'Video', view: 'Clips', contentType: 'video/mp4', signature: null, selector: 'video', settingsLabel: 'Duration', accept: 'image/jpeg,image/png' },
  { mode: 'music', label: 'Music', view: 'Tracks', contentType: 'audio/wav', signature: 'RIFF', selector: 'audio', settingsLabel: 'Duration', accept: 'audio/wav,audio/x-wav,audio/wave,audio/mpeg' },
] as const

test('creates Chat, Image, Video, and Music as typed, reusable Assets', async ({ page }) => {
  test.setTimeout(90_000)
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  const publishedTitles: string[] = []

  for (const item of modes) {
    const prompt = `E2E ${item.label} typed asset ${Date.now().toString(36)}`
    await page.goto('/create/chat')
    const modeMenu = page.getByRole('button', { name: 'Choose what to create' })
    await modeMenu.click()
    await page.getByRole('menuitem', { name: new RegExp(item.label) }).click()
    await page.locator('.studio-composer textarea').fill(prompt)
    const actionLabel = item.mode === 'image' ? 'Generate image' : `Generate ${item.label}`
    await page.getByRole('button', { name: actionLabel, exact: true }).click()
    if (item.mode === 'chat') {
      const conversation = page.locator('.conversation-feed')
      await expect(conversation.getByText(prompt, { exact: true })).toBeVisible()
      await expect(conversation.getByText('Deterministic Local Test response', { exact: false })).toBeVisible()

      const followUp = `Adapt the previous answer for product teams ${Date.now().toString(36)}`
      await page.locator('.studio-composer textarea').fill(followUp)
      await page.getByRole('button', { name: actionLabel, exact: true }).click()
      await expect(conversation.getByText(followUp, { exact: true })).toBeVisible()
      await expect(conversation.getByText('Conversation context: 1 prior turn(s).', { exact: false })).toBeVisible()
    }
    const generatedTask = page.locator('.studio-task').filter({ hasText: prompt }).first()
    await expect(generatedTask.locator('.conversation-result')).toContainText('Saved to Assets')

    await generatedTask.locator('.conversation-result').click()
    const publishLink = page.getByRole('dialog', { name: 'Generation details' }).getByRole('link', { name: 'Publish work', exact: true })
    const publishHref = await publishLink.getAttribute('href')
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

  const multiReferencePrompt = `E2E ordered multi-reference image ${Date.now().toString(36)}`
  await page.goto('/create/image')
  await page.getByRole('button', { name: 'Choose what to create' }).click()
  await page.getByRole('button', { name: 'Add reference', exact: true }).click()
  const referenceOptions = page.locator('.reference-list > button')
  await expect.poll(async () => referenceOptions.count(), { timeout: 10_000 }).toBeGreaterThanOrEqual(2)
  await referenceOptions.nth(0).click()
  await referenceOptions.nth(1).click()
  await expect(page.locator('.studio-context-item')).toHaveCount(2)
  await page.locator('.reference-mode-switch').getByRole('button', { name: 'Mask', exact: true }).click()
  await referenceOptions.nth(0).click()
  await expect(page.locator('.mask-context-item')).toHaveCount(1)
  await page.locator('.studio-composer textarea').fill(multiReferencePrompt)
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()
  const editedTask = page.locator('.studio-task').filter({ hasText: multiReferencePrompt }).first()
  await expect(editedTask.locator('.conversation-result')).toContainText('Saved to Assets')
  await editedTask.getByRole('button', { name: 'Favorite generation', exact: true }).click()
  await expect(editedTask.getByRole('button', { name: 'Remove generation favorite', exact: true })).toBeVisible()
  const generationPage = await (await page.request.get('/api/v1/generations?mode=image&limit=1')).json() as { items: Array<{ sourceAssetIds: string[]; maskAssetId?: string; isFavorite: boolean }> }
  expect(generationPage.items[0].sourceAssetIds).toHaveLength(2)
  expect(generationPage.items[0].maskAssetId).toBeTruthy()
  expect(generationPage.items[0].isFavorite).toBeTruthy()

  await page.goto('/workspace/generations')
  const selectionBoxes = page.locator('.generation-row-select input')
  await selectionBoxes.nth(0).check()
  await selectionBoxes.nth(1).check()
  await page.getByRole('button', { name: 'Favorite selected', exact: true }).click()
  await expect(page.getByText('2 generation(s) updated.', { exact: true })).toBeVisible()
  await selectionBoxes.nth(0).check()
  await selectionBoxes.nth(1).check()
  await page.getByRole('button', { name: 'Remove favorites', exact: true }).click()
  await expect(page.getByText('2 generation(s) updated.', { exact: true })).toBeVisible()
})

test('initializes every creation mode with its own controls and compatible references', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')

  for (const item of modes) {
    await page.goto(`/create/${item.mode}`)
    await expect(page.getByRole('button', { name: 'Choose what to create' })).toBeVisible()
    await page.getByRole('button', { name: 'Output settings' }).click()
    await expect(page.getByText(item.settingsLabel, { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Output settings' }).click()

    await page.getByRole('button', { name: 'Choose what to create' }).click()
    await page.getByRole('button', { name: 'Add reference', exact: true }).click()
    await expect(page.locator('.reference-picker input[type="file"]')).toHaveAttribute('accept', item.accept)
  }
})

test('keeps every creation mode visible on a phone-sized viewport', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/create/video')

  await page.getByRole('button', { name: 'Choose what to create' }).click()
  const menuBox = await page.locator('.studio-mode-menu').boundingBox()
  const menu = page.locator('.studio-mode-menu')
  expect(menuBox).not.toBeNull()
  expect(menuBox!.width).toBeLessThanOrEqual(224)
  expect(menuBox!.height).toBeLessThanOrEqual(320)
  expect(menuBox!.y).toBeGreaterThanOrEqual(0)
  await expect(menu).toHaveCSS('scrollbar-width', 'none')
  await expect(menu).toHaveCSS('overflow-y', 'auto')
  const initialScrollTop = await menu.evaluate(element => element.scrollTop)
  await menu.hover()
  await page.mouse.wheel(0, 500)
  await expect.poll(() => menu.evaluate(element => element.scrollTop)).toBeGreaterThan(initialScrollTop)
  for (const label of modes.map(mode => mode.label)) {
    await expect(page.getByRole('menuitem', { name: new RegExp(label) })).toBeVisible()
  }
  await expect(page.evaluate(() => document.documentElement.scrollWidth)).resolves.toBe(390)
})

test('highlights only the active creation mode and dismisses the menu', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/create/chat')

  const trigger = page.getByRole('button', { name: 'Choose what to create' })
  const menu = page.getByRole('menu', { name: 'Choose what to create' })
  await trigger.click()
  await expect(page.getByRole('menuitem', { name: 'Files', exact: true })).not.toHaveClass(/active/)
  await expect(page.getByRole('menuitem', { name: 'Chat', exact: true })).toHaveClass(/active/)

  await page.getByRole('heading', { name: 'AI creation space', exact: true }).click()
  await expect(menu).toBeHidden()
  await expect(trigger).toHaveAttribute('aria-expanded', 'false')

  await trigger.click()
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()
})

test('uses the compact floating style for output settings and dismisses it', async ({ page }) => {
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/create/chat')

  const trigger = page.getByRole('button', { name: 'Output settings' })
  const modeTrigger = page.getByRole('button', { name: 'Choose what to create' })
  await expect(page.locator('.studio-submit')).toHaveCSS('box-shadow', 'none')
  const buttonVisuals = async (button: typeof trigger) => button.evaluate((element) => {
    const style = globalThis.getComputedStyle(element)
    return {
      width: style.width,
      height: style.height,
      padding: style.padding,
      borderRadius: style.borderRadius,
      borderColor: style.borderColor,
      backgroundColor: style.backgroundColor,
      color: style.color,
    }
  })
  expect(await buttonVisuals(trigger)).toEqual(await buttonVisuals(modeTrigger))

  await trigger.click()
  const panel = page.getByRole('dialog', { name: 'Output settings' })
  await expect(panel).toBeVisible()
  await expect(panel).toHaveCSS('width', '220px')
  await expect(panel).toHaveCSS('border-radius', '24px')
  await expect(panel.locator('label')).toHaveCount(2)

  await page.getByRole('heading', { name: 'AI creation space', exact: true }).click()
  await expect(panel).toBeHidden()

  await trigger.click()
  const settingsActiveVisuals = await buttonVisuals(trigger)
  await trigger.click()
  await modeTrigger.click()
  expect(await buttonVisuals(modeTrigger)).toEqual(settingsActiveVisuals)
  await modeTrigger.click()

  await trigger.click()
  await page.keyboard.press('Escape')
  await expect(panel).toBeHidden()
})

test('keeps the creation workspace in sync with the global theme', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))
  await page.goto('/create/image')

  const studio = page.locator('.creation-studio-new')
  await expect(studio).toHaveClass(/is-light/)
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')

  await page.getByRole('button', { name: /Switch color theme|切换主题/ }).click()
  await expect(studio).not.toHaveClass(/is-light/)
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
})

test('keeps the creation workspace free of the global footer', async ({ page }) => {
  await page.goto('/create/chat')
  await expect(page.locator('.site-footer')).toHaveCount(0)

  await page.goto('/discover')
  await expect(page.locator('.site-footer')).toBeVisible()
})

test('switches creation modes in place and keeps the selected route active', async ({ page }) => {
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto('/create/image')

  for (const item of modes) {
    await page.getByRole('button', { name: 'Choose what to create' }).click()
    await page.getByRole('menuitem', { name: new RegExp(item.label) }).click()
    await expect(page).toHaveURL(new RegExp(`/create/${item.mode}$`))
    await expect(page.locator(`.creation-studio[data-mode="${item.mode}"]`)).toBeVisible()
    if (item.mode === 'chat') {
      await expect(page.locator('.creation-mode-chip')).toHaveCount(0)
    } else {
      await expect(page.locator('.creation-mode-chip')).toContainText(item.label)
      const closeButton = page.locator('.creation-mode-chip button')
      await expect(closeButton).toHaveCSS('width', '18px')
      await expect(closeButton).toHaveCSS('height', '18px')
      await expect(closeButton).toHaveCSS('padding', '0px')
      await expect(closeButton).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
    }
  }
})
