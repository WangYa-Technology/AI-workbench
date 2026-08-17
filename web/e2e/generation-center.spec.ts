import { expect, test, type APIRequestContext } from '@playwright/test'

type Generation = {
  id: string
  prompt: string
  status: string
  actions: {
    canCancel: boolean
    canRetry: boolean
    canDownload: boolean
    canReuse: boolean
    canView: boolean
  }
}

async function submitGeneration(request: APIRequestContext, mode: 'chat' | 'image', prompt: string) {
  const response = await request.post('/api/v1/generations', {
    headers: { 'Idempotency-Key': `generation-center-${crypto.randomUUID()}` },
    data: { mode, prompt },
  })
  expect(response.status()).toBe(202)
  return await response.json() as Generation
}

async function waitForSuccess(request: APIRequestContext, id: string) {
  let item: Generation | undefined
  await expect.poll(async () => {
    const response = await request.get(`/api/v1/generations/${id}`)
    expect(response.ok()).toBeTruthy()
    item = await response.json() as Generation
    return item.status
  }).toBe('succeeded')
  return item!
}

test('filters, paginates, restores deep links, and renders completed actions', async ({ page }) => {
  test.setTimeout(60_000)
  const runID = Date.now().toString(36)
  const firstPrompt = `Generation Center chat alpha ${runID}`
  const secondPrompt = `Generation Center chat beta ${runID}`
  const imagePrompt = `Generation Center image ${runID}`

  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  const first = await submitGeneration(page.request, 'chat', firstPrompt)
  const second = await submitGeneration(page.request, 'chat', secondPrompt)
  const image = await submitGeneration(page.request, 'image', imagePrompt)
  const [, completedSecond] = await Promise.all([
    waitForSuccess(page.request, first.id),
    waitForSuccess(page.request, second.id),
    waitForSuccess(page.request, image.id),
  ])

  expect(completedSecond.actions).toMatchObject({
    canCancel: false,
    canRetry: false,
    canDownload: true,
    canReuse: true,
    canView: true,
  })

  await page.goto('/workspace/generations')
  const utcDate = new Date().toISOString().slice(0, 10)
  const filters = page.locator('.generation-filters')
  await filters.getByRole('combobox', { name: 'Mode', exact: true }).selectOption('chat')
  await filters.getByRole('combobox', { name: 'Status', exact: true }).selectOption('succeeded')
  await filters.getByRole('textbox', { name: 'From (UTC)', exact: true }).fill(utcDate)
  await filters.getByRole('textbox', { name: 'To (UTC)', exact: true }).fill(utcDate)
  await page.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`mode=chat.*status=succeeded.*dateFrom=${utcDate}.*dateTo=${utcDate}`))
  await expect(page.getByText(firstPrompt, { exact: true })).toBeVisible()
  await expect(page.getByText(secondPrompt, { exact: true })).toBeVisible()
  await expect(page.getByText(imagePrompt, { exact: true })).toHaveCount(0)

  await page.goto('/workspace/generations?mode=chat&status=succeeded&limit=1')
  await expect(page.locator('.generation-row')).toHaveCount(1)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.locator('.generation-row')).toHaveCount(2)
  await expect(page.getByText(firstPrompt, { exact: true })).toBeVisible()
  await expect(page.getByText(secondPrompt, { exact: true })).toBeVisible()

  await page.goto(`/workspace/generations?mode=music&generationId=${second.id}`)
  const focused = page.locator('.generation-row.usage-focus')
  await expect(focused).toContainText(secondPrompt)
  await expect(focused).toContainText(second.id)
  await expect(focused.getByRole('link', { name: 'Download', exact: true })).toBeVisible()
  await expect(focused.getByRole('link', { name: 'Use in Create', exact: true })).toBeVisible()
  await expect(focused.getByRole('link', { name: 'View details', exact: true })).toBeVisible()

  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(page.locator('.generation-row.usage-focus')).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})

test('renders server-derived cancel and retry eligibility', async ({ page }) => {
  const runID = Date.now().toString(36)
  const cancelPrompt = `Generation Center cancelled ${runID}`
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  const cancellable = await submitGeneration(page.request, 'image', cancelPrompt)
  expect(cancellable.actions).toMatchObject({ canCancel: true, canRetry: false })
  const cancelledResponse = await page.request.post(`/api/v1/generations/${cancellable.id}/cancel`, {
    headers: { 'Idempotency-Key': `cancel-generation-center-${runID}` },
    data: { reason: 'Cancelled to verify retry eligibility in Generation Center.' },
  })
  expect(cancelledResponse.ok()).toBeTruthy()
  expect(await cancelledResponse.json()).toMatchObject({ status: 'cancelled', actions: { canCancel: false, canRetry: true } })

  await page.goto('/workspace/generations?status=cancelled')
  const cancelledRow = page.locator('.generation-row').filter({ hasText: cancelPrompt })
  await expect(cancelledRow).toBeVisible()
  await expect(cancelledRow.getByRole('button', { name: 'Try again', exact: true })).toBeVisible()
  await expect(cancelledRow.getByRole('button', { name: 'Cancel', exact: true })).toHaveCount(0)
  await cancelledRow.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(page.getByText('A new generation was queued with retry lineage.', { exact: true })).toBeVisible()
  await expect(cancelledRow).toBeVisible()
})
