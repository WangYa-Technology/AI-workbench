import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'

const jobID = '10000000-0000-4000-8000-000000009811'
const retryID = '20000000-0000-4000-8000-000000009812'
const ownerID = '30000000-0000-4000-8000-000000009813'
const path = '/api/v1/admin/data-rights/media-cleanups'
const failedJob = (id = jobID) => ({
  id, userId: ownerID, kind: 'account', status: 'failed', attempts: 20, maxAttempts: 20,
  errorCode: 'handler_failed', canRetry: true, unavailableReason: '',
  createdAt: '2026-09-19T10:00:00Z', updatedAt: '2026-09-19T10:01:00Z',
})

for (const width of [390, 1308]) {
  test(`cleanup recovery preserves failure history and queues once at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    let queued = false
    let calls = 0
    await page.route(/\/api\/v1\/admin\/data-rights\/media-cleanups(?:\?|$)/, route => route.fulfill({ json: {
      items: [{ ...failedJob(), canRetry: !queued, unavailableReason: queued ? 'already_retried' : '', ...(queued ? { retryJobId: retryID } : {}) }],
    } }))
    await page.route(`**${path}/${jobID}/retry`, async route => {
      calls++
      expect(route.request().postDataJSON()).toEqual({ expectedAttempts: 20, reason: 'Storage access was repaired and checked.', confirmed: true })
      queued = true
      await route.fulfill({ status: 201, json: { ...failedJob(retryID), status: 'queued', attempts: 0, canRetry: false, unavailableReason: 'not_failed', retryOf: jobID } })
    })
    await page.goto('/admin?tab=dataRights')
    const queue = page.getByRole('region', { name: 'Media cleanup recovery', exact: true })
    await expect(queue.getByText('20 / 20 attempts', { exact: false })).toBeVisible()
    await queue.getByRole('button', { name: 'Retry media cleanup', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'Retry media cleanup', exact: true })
    const submit = drawer.getByRole('button', { name: 'Retry media cleanup', exact: true })
    await expect(submit).toBeDisabled()
    await drawer.getByLabel('Decision reason', { exact: true }).fill('Storage access was repaired and checked.')
    await expect(submit).toBeDisabled()
    await drawer.getByRole('checkbox').check()
    await expect(submit).toBeEnabled()
    await expect.poll(() => drawer.evaluate(el => getComputedStyle(el).filter)).toBe('none')
    await page.screenshot({ path: testInfo.outputPath('cleanup-recovery-drawer.png'), animations: 'disabled' })
    await submit.click()
    await expect(drawer).not.toBeVisible()
    await expect(queue.getByRole('status')).toHaveText('Cleanup retry queued. File deletion is not yet complete.')
    await expect(queue.getByText(`Replacement job · ${retryID}`)).toBeVisible()
    await expect(queue.getByRole('button', { name: 'Retry media cleanup', exact: true })).toHaveCount(0)
    await expect(queue.getByText('20 / 20 attempts', { exact: false })).toBeVisible()
    expect(calls).toBe(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await queue.screenshot({ path: testInfo.outputPath('cleanup-recovery-queue.png'), animations: 'disabled' })
  })
}

test('cleanup errors are actionable and stale recovery does not claim success', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  let failing = true
  await page.route(/\/api\/v1\/admin\/data-rights\/media-cleanups(?:\?|$)/, route => route.fulfill(failing
    ? { status: 500, json: { error: { code: 'internal_error', message: 'Temporary failure', retryable: true } } }
    : { json: { items: [failedJob()] } }))
  await page.route(`**${path}/${jobID}/retry`, route => route.fulfill({ status: 409, json: {
    error: { code: 'media_cleanup_conflict', message: 'Stale request', retryable: false },
  } }))
  await page.goto('/admin?tab=dataRights')
  const queue = page.getByRole('region', { name: 'Media cleanup recovery', exact: true })
  await expect(queue.getByRole('alert')).toBeVisible()
  failing = false
  await queue.getByRole('button', { name: 'Refresh', exact: true }).click()
  await expect(queue.getByRole('alert')).toHaveCount(0)
  await queue.getByRole('button', { name: 'Retry media cleanup', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Retry media cleanup', exact: true })
  await drawer.getByLabel('Decision reason', { exact: true }).fill('Storage access was repaired and checked.')
  await drawer.getByRole('checkbox').check()
  await drawer.getByRole('button', { name: 'Retry media cleanup', exact: true }).click()
  await expect(drawer.getByRole('alert')).toContainText('Refresh')
  await expect(queue.getByText('Cleanup retry queued. File deletion is not yet complete.')).toHaveCount(0)
  await expect(drawer.getByRole('button', { name: 'Retry media cleanup', exact: true })).toBeEnabled()
})

test('an accepted recovery remains disabled when the follow-up refresh fails', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  let accepted = false
  await page.route(/\/api\/v1\/admin\/data-rights\/media-cleanups(?:\?|$)/, route => route.fulfill(accepted
    ? { status: 500, json: { error: { code: 'internal_error', message: 'Temporarily unavailable', retryable: true } } }
    : { json: { items: [failedJob()] } }))
  await page.route(`**${path}/${jobID}/retry`, route => {
    accepted = true
    return route.fulfill({ status: 201, json: { ...failedJob(retryID), status: 'queued', retryOf: jobID, canRetry: false, unavailableReason: 'not_failed' } })
  })
  await page.goto('/admin?tab=dataRights')
  const queue = page.getByRole('region', { name: 'Media cleanup recovery', exact: true })
  await queue.getByRole('button', { name: 'Retry media cleanup', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Retry media cleanup', exact: true })
  await drawer.getByLabel('Decision reason', { exact: true }).fill('Storage access was repaired and checked.')
  await drawer.getByRole('checkbox').check()
  await drawer.getByRole('button', { name: 'Retry media cleanup', exact: true }).click()
  await expect(queue.getByRole('alert')).toBeVisible()
  await expect(queue.getByRole('status')).toHaveText('Cleanup retry queued. File deletion is not yet complete.')
  await expect(queue.getByText(`Replacement job · ${retryID}`)).toBeVisible()
  await expect(queue.getByRole('button', { name: 'Retry media cleanup', exact: true })).toHaveCount(0)
})

test('cleanup pagination retains filters and ignores responses from an earlier status', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  let release: (() => void) | undefined
  const delayed = new Promise<void>(resolve => { release = resolve })
  let slowStarted = false
  await page.route(/\/api\/v1\/admin\/data-rights\/media-cleanups(?:\?|$)/, async route => {
    const url = new URL(route.request().url())
    if (url.searchParams.get('status') === 'queued') {
      slowStarted = true
      await delayed
      await route.fulfill({ json: { items: [{ ...failedJob(retryID), status: 'queued', canRetry: false, unavailableReason: 'not_failed' }] } })
    } else if (url.searchParams.get('status') === 'all') {
      await route.fulfill({ json: { items: [] } })
    } else if (url.searchParams.has('cursor')) {
      expect(url.searchParams.get('cursor')).toBe('next/+')
      expect(url.searchParams.get('status')).toBe('failed')
      await route.fulfill({ json: { items: [failedJob(), failedJob(retryID)] } })
    } else {
      await route.fulfill({ json: { items: [failedJob()], nextCursor: 'next/+' } })
    }
  })
  await page.goto('/admin?tab=dataRights')
  const queue = page.getByRole('region', { name: 'Media cleanup recovery', exact: true })
  await queue.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(queue.locator('article')).toHaveCount(2)
  await chooseOption(queue.getByRole('combobox', { name: 'Status', exact: true }), 'queued')
  await expect.poll(() => slowStarted).toBe(true)
  await chooseOption(queue.getByRole('combobox', { name: 'Status', exact: true }), 'all')
  await expect(queue.getByText('No matching cleanup jobs.')).toBeVisible()
  const response = page.waitForResponse(res => res.url().includes('media-cleanups?status=queued'))
  release!()
  await response
  await expect(queue.getByText('No matching cleanup jobs.')).toBeVisible()
  await expect(queue.locator('article')).toHaveCount(0)
})

for (const width of [390, 1308]) {
  test(`product delivery recovery shows its order and retention guards at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    const orderId = '00000000-0000-4000-8000-000000009904'
    let queued = false
    await page.route(/\/api\/v1\/admin\/data-rights\/media-cleanups(?:\?|$)/, route => {
      const kind = new URL(route.request().url()).searchParams.get('kind')
      return route.fulfill({ json: { items: kind !== 'product' ? [failedJob()] : [
        { ...failedJob(), kind: 'product', orderId, canRetry: !queued, unavailableReason: queued ? 'already_retried' : '', ...(queued ? { retryJobId: retryID } : {}) },
        { ...failedJob(retryID), kind: 'product', orderId, canRetry: false, unavailableReason: 'delivery_required' },
        { ...failedJob('40000000-0000-4000-8000-000000009905'), kind: 'product', userId: undefined, canRetry: false, unavailableReason: 'missing_subject' },
      ] } })
    })
    await page.route(`**${path}/${jobID}/retry`, route => {
      queued = true
      return route.fulfill({ status: 201, json: { ...failedJob(retryID), kind: 'product', orderId, status: 'queued', attempts: 0, canRetry: false, unavailableReason: 'not_failed', retryOf: jobID } })
    })
    await page.goto('/admin?tab=dataRights')
    const queue = page.getByRole('region', { name: 'Media cleanup recovery', exact: true })
    await chooseOption(queue.getByRole('combobox', { name: 'Cleanup scope', exact: true }), 'product')
    await expect(queue.locator('article')).toHaveCount(3)
    await expect(queue.getByText('A pending payment or active purchase still requires this delivery')).toBeVisible()
    await expect(queue.getByText('Account or delivery evidence is missing; review is required')).toBeVisible()
    await expect(queue.getByRole('button', { name: 'Retry media cleanup', exact: true })).toHaveCount(1)
    await queue.getByRole('button', { name: 'Retry media cleanup', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'Retry media cleanup', exact: true })
    await expect(drawer).toContainText(orderId)
    await drawer.getByLabel('Decision reason', { exact: true }).fill('Object storage deletion access has been repaired.')
    await drawer.getByRole('checkbox').check()
    await drawer.getByRole('button', { name: 'Retry media cleanup', exact: true }).click()
    await expect(queue.getByText(`Replacement job · ${retryID}`)).toBeVisible()
    await expect(queue.getByRole('button', { name: 'Retry media cleanup', exact: true })).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    // The shell can clip overflowing children without widening the document.
    // Check the actual text boxes against their row and the visible queue.
    expect(await queue.evaluate(el => {
      const bounds = el.getBoundingClientRect()
      return Array.from(el.querySelectorAll('article small')).every(node => {
        const box = node.getBoundingClientRect()
        const row = node.closest('article')!.getBoundingClientRect()
        const range = document.createRange()
        range.selectNodeContents(node)
        return box.width > 0 && node.scrollWidth <= node.clientWidth + 1 &&
          Array.from(range.getClientRects()).every(text => text.left >= Math.max(row.left, bounds.left) - 1 &&
            text.right <= Math.min(row.right, bounds.right) + 1 && text.top >= box.top - 1 && text.bottom <= box.bottom + 1)
      })
    })).toBe(true)
    await queue.screenshot({ path: testInfo.outputPath('product-cleanup-queue.png'), animations: 'disabled' })
  })
}

test('product cleanup pages keep their kind and reject late results after switching scope', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  let started = false
  await page.route(/\/api\/v1\/admin\/data-rights\/media-cleanups(?:\?|$)/, async route => {
    const url = new URL(route.request().url())
    if (url.searchParams.get('kind') === 'product') {
      if (url.searchParams.has('cursor')) {
        expect(url.searchParams.get('cursor')).toBe('product-next')
        expect(url.searchParams.get('status')).toBe('failed')
        started = true
        await gate
        await route.fulfill({ json: { items: [{ ...failedJob(retryID), kind: 'product', orderId: ownerID }] } })
      } else await route.fulfill({ json: { items: [{ ...failedJob(), kind: 'product', orderId: ownerID }], nextCursor: 'product-next' } })
    } else await route.fulfill({ json: { items: [] } })
  })
  await page.goto('/admin?tab=dataRights')
  const queue = page.getByRole('region', { name: 'Media cleanup recovery', exact: true })
  await chooseOption(queue.getByRole('combobox', { name: 'Cleanup scope', exact: true }), 'product')
  await queue.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect.poll(() => started).toBe(true)
  await chooseOption(queue.getByRole('combobox', { name: 'Cleanup scope', exact: true }), 'account')
  await expect(queue.getByText('No matching cleanup jobs.')).toBeVisible()
  const response = page.waitForResponse(res => res.url().includes('cursor=product-next'))
  release()
  await response
  await expect(queue.locator('article')).toHaveCount(0)
})
