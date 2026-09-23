import { expect, test, type Page, type Route } from '@playwright/test'

const firstID = '00000000-0000-4000-8000-000000009901'
const secondID = '00000000-0000-4000-8000-000000009902'
const buyerID = '00000000-0000-4000-8000-000000009903'
const orderID = '00000000-0000-4000-8000-000000009904'
const stamp = '2026-09-21T10:00:00Z'
const asset = (id: string, title: string) => ({
  id, title, ownerId: buyerID, kind: 'image', sourceType: 'upload',
  mediaUrl: '/brand/logo.png', mimeType: 'image/png', scanStatus: 'clean',
  licenseCode: 'creator-owned', createdAt: stamp, versionNumber: 1,
  isLatestVersion: true, usages: [], usageNextCursor: `usage-${id}`,
  versions: [
    { id: firstID, title: 'First source', versionNumber: 1, scanStatus: 'clean', createdAt: stamp },
    { id: secondID, title: 'Current source', versionNumber: 2, scanStatus: 'clean', createdAt: stamp },
  ],
})

async function mockApp(page: Page, handle: (route: Route, url: URL) => Promise<void> | undefined) {
  const unexpected: string[] = []
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    const result = handle(route, url)
    if (result) return result
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: {
      id: buyerID, email: 'buyer@fixture.test', handle: 'buyer', displayName: 'Buyer',
      role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: [],
    } } })
    if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: {
      siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png',
      footerText: { enUS: '', zhCN: '' },
      policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])),
    } })
    if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (url.pathname === `/api/v1/assets/${firstID}`) return route.fulfill({ json: asset(firstID, 'First source') })
    if (url.pathname === `/api/v1/assets/${secondID}`) return route.fulfill({ json: asset(secondID, 'Current source') })
    if (url.pathname === '/api/v1/assets') return route.fulfill({ json: { items: [], total: 0 } })
    if (url.pathname === '/api/v1/assets/saved-works') return route.fulfill({ json: { items: [] } })
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 500, json: { error: { code: 'unexpected_response', message: 'Unmocked API', retryable: false } } })
  })
  return unexpected
}

async function drainResponse(page: Page, release: () => Promise<void>, suffix: string) {
  const response = page.waitForResponse(r => r.url().includes(suffix))
  await release()
  await (await response).finished()
  // Let the real fetch/JSON/Vue continuations render before asserting absence.
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}

for (const outcome of ['success', 'failure'] as const) {
  test(`old asset usage ${outcome} cannot replace the current asset`, async ({ page }) => {
    let pending: Route | undefined
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === `/api/v1/assets/${firstID}/usages`) {
        pending = route
        return Promise.resolve()
      }
    })
    await page.goto(`/workspace/assets/${firstID}`)
    await page.locator('.asset-usage-block').getByRole('button', { name: 'Load more', exact: true }).click()
    await expect.poll(() => Boolean(pending)).toBe(true)
    await page.locator(`.asset-version-list a[href="/workspace/assets/${secondID}"]`).click()
    await expect(page.getByRole('heading', { name: 'Current source', exact: true })).toBeVisible()
    await drainResponse(page, () => outcome === 'success'
      ? pending!.fulfill({ json: { items: [] } })
      : pending!.fulfill({ status: 500, json: { error: { code: 'old_usage_failure', message: 'Old asset read failed', retryable: true } } }), `/assets/${firstID}/usages`)
    await expect(page.getByRole('heading', { name: 'Current source', exact: true })).toBeVisible()
    await expect(page.locator('main [role="alert"]')).toHaveCount(0)
    expect(unexpected).toEqual([])
  })
}

test('old usage completion cannot release the current asset pagination lock', async ({ page }) => {
  const pending = new Map<string, Route>()
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/usages')) {
      pending.set(url.pathname.split('/')[4]!, route)
      return Promise.resolve()
    }
  })
  await page.goto(`/workspace/assets/${firstID}`)
  const more = page.locator('.asset-usage-block').getByRole('button', { name: 'Load more', exact: true })
  await more.click()
  await expect.poll(() => pending.has(firstID)).toBe(true)
  await page.locator(`.asset-version-list a[href="/workspace/assets/${secondID}"]`).click()
  await expect(page.getByRole('heading', { name: 'Current source', exact: true })).toBeVisible()
  await expect(more).toBeEnabled()
  await more.click()
  await expect.poll(() => pending.has(secondID)).toBe(true)
  await drainResponse(page, () => pending.get(firstID)!.fulfill({ json: { items: [] } }), `/assets/${firstID}/usages`)
  await expect(more).toBeDisabled()
  await drainResponse(page, () => pending.get(secondID)!.fulfill({ json: { items: [], nextCursor: 'current-next' } }), `/assets/${secondID}/usages`)
  await expect(more).toBeEnabled()
  expect(unexpected).toEqual([])
})

test('leaving and returning to purchases starts an independent pagination request', async ({ page }) => {
  const pending: Route[] = []
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/assets' && url.searchParams.get('source') === 'purchase') {
      if (url.searchParams.has('cursor')) { pending.push(route); return Promise.resolve() }
      return route.fulfill({ json: { items: [{ ...asset(firstID, 'Purchased source'), sourceType: 'purchase' }], total: 2, nextCursor: 'next-purchase' } })
    }
    if (url.pathname === '/api/v1/orders') return route.fulfill({ json: { items: [] } })
  })
  await page.goto('/workspace/purchases')
  const more = page.locator('main').getByRole('button', { name: 'Load more', exact: true })
  await more.click()
  await expect.poll(() => pending.length).toBe(1)
  await page.locator('#primary-navigation a[href="/workspace/orders"]').click()
  await expect(page.locator('.orders-workspace')).toBeVisible()
  await page.locator('#primary-navigation a[href="/workspace/purchases"]').click()
  await expect(page.getByRole('heading', { name: 'Purchased source', exact: true })).toBeVisible()
  await expect(more).toBeEnabled()
  await more.click()
  await expect.poll(() => pending.length).toBe(2)
  await drainResponse(page, () => pending[0]!.fulfill({ json: { items: [], total: 2 } }), 'cursor=next-purchase')
  await expect(more).toBeDisabled()
  await drainResponse(page, () => pending[1]!.fulfill({ json: { items: [{ ...asset(secondID, 'Next purchase'), sourceType: 'purchase' }], total: 2 } }), 'cursor=next-purchase')
  await expect(page.getByRole('heading', { name: 'Next purchase', exact: true })).toBeVisible()
  await expect(more).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('late asset version upload does not navigate away from the current asset', async ({ page }) => {
  let upload: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === `/api/v1/assets/${firstID}/versions` && route.request().method() === 'POST') {
      upload = route
      return Promise.resolve()
    }
  })
  await page.goto(`/workspace/assets/${firstID}`)
  await page.locator('.asset-version-block').getByRole('button', { name: 'Upload new version', exact: true }).click()
  await page.locator('.asset-version-form textarea').fill('A corrected source image')
  await page.locator('.asset-version-form input[type="file"]').setInputFiles({ name: 'source.png', mimeType: 'image/png', buffer: Buffer.from('isolated-upload-fixture') })
  await page.locator('.asset-version-form button[type="submit"]').click()
  await expect.poll(() => Boolean(upload)).toBe(true)
  await page.locator(`.asset-version-list a[href="/workspace/assets/${secondID}"]`).click()
  await expect(page.getByRole('heading', { name: 'Current source', exact: true })).toBeVisible()
  await drainResponse(page, () => upload!.fulfill({ json: asset(firstID, 'First source') }), `/assets/${firstID}/versions`)
  await expect(page).toHaveURL(new RegExp(`/workspace/assets/${secondID}$`))
  await expect(page.getByRole('heading', { name: 'Current source', exact: true })).toBeVisible()
  expect(unexpected).toEqual([])
})

test('saved work pagination from a previous visit cannot merge into the new visit', async ({ page }) => {
  const pending: Route[] = []
  const saved = (id: string, title: string) => ({ postId: id, workId: id, title, mediaKind: 'image', mediaUrl: '/brand/logo.png', authorHandle: 'creator', savedAt: stamp, licenseCode: 'creator-owned' })
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/assets/saved-works') {
      if (url.searchParams.has('cursor')) { pending.push(route); return Promise.resolve() }
      return route.fulfill({ json: { items: [saved(firstID, 'Saved reference')], nextCursor: 'next-saved' } })
    }
  })
  await page.goto('/workspace/assets?view=saved')
  const more = page.locator('main').getByRole('button', { name: 'Load more', exact: true })
  await more.click()
  await expect.poll(() => pending.length).toBe(1)
  await page.getByRole('tab', { name: 'Owned Assets', exact: true }).click()
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
  await page.getByRole('tab', { name: 'Saved Works', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Saved reference', exact: true })).toBeVisible()
  await expect(more).toBeEnabled()
  await more.click()
  await expect.poll(() => pending.length).toBe(2)
  await drainResponse(page, () => pending[0]!.fulfill({ json: { items: [saved(secondID, 'Stale removed reference')] } }), 'cursor=next-saved')
  await expect(page.getByRole('heading', { name: 'Stale removed reference', exact: true })).toHaveCount(0)
  await expect(more).toBeDisabled()
  await drainResponse(page, () => pending[1]!.fulfill({ json: { items: [saved(secondID, 'Current saved reference')] } }), 'cursor=next-saved')
  await expect(page.getByRole('heading', { name: 'Current saved reference', exact: true })).toBeVisible()
  expect(unexpected).toEqual([])
})

for (const pendingRead of [false, true]) {
  test(`upload scan polling stops when its view is left: in-flight=${pendingRead}`, async ({ page }) => {
    let poll: Route | undefined
    let assetReads = 0
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/assets/uploads' && route.request().method() === 'POST') return route.fulfill({ json: { ...asset(firstID, 'Uploaded source'), scanStatus: 'pending' } })
      if (url.pathname === `/api/v1/assets/${firstID}`) {
        assetReads++
        poll = route
        return Promise.resolve()
      }
    })
    await page.goto('/workspace/assets')
    await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
    const now = new Date('2026-09-21T12:00:00Z')
    await page.clock.install({ time: now })
    await page.clock.pauseAt(new Date(now.getTime() + 1000))
    await page.getByRole('button', { name: 'Upload Asset', exact: true }).click()
    await page.locator('.asset-upload-panel input[type="text"]').fill('Uploaded source')
    await page.locator('.asset-upload-panel input[type="file"]').setInputFiles({ name: 'source.png', mimeType: 'image/png', buffer: Buffer.from('isolated-source-fixture') })
    await page.locator('.asset-upload-panel button[type="submit"]').click()
    await expect(page.locator('main .task-feedback.success')).toContainText('scanning is queued')
    if (pendingRead) {
      await page.clock.runFor(800)
      await expect.poll(() => Boolean(poll)).toBe(true)
    }
    await page.getByRole('tab', { name: 'Saved Works', exact: true }).click()
    await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
    if (pendingRead) {
      const response = page.waitForResponse(r => r.url().endsWith(`/assets/${firstID}`))
      await poll!.fulfill({ json: asset(firstID, 'Uploaded source') })
      await (await response).finished()
    }
    await page.clock.runFor(2000)
    await expect(page.locator('main .task-feedback.success')).toHaveCount(0)
    expect(assetReads).toBe(pendingRead ? 1 : 0)
    expect(unexpected).toEqual([])
  })
}

for (const leave of [false, true]) {
test(`refund feedback belongs only to its original order view: leave=${leave}`, async ({ page }) => {
  let refund: Route | undefined
  const order = {
    id: orderID, productId: firstID, productTitle: 'Purchased resource', assetId: firstID,
    status: 'fulfilled', amountCents: 1900, currency: 'USD', licenseName: 'Commercial license',
    licenseCode: 'hcai-commercial-standard-v1', licenseVersion: '1', licenseTerms: 'Terms',
    refundWindowDays: 7, canRequestRefund: true, paymentMode: 'stripe', realCharge: false, createdAt: stamp, events: [],
  }
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/orders') return route.fulfill({ json: { items: [order] } })
    if (url.pathname === `/api/v1/orders/${orderID}/refund`) { refund = route; return Promise.resolve() }
  })
  await page.goto('/workspace/orders')
  await page.locator('.order-row textarea').fill('The delivered file cannot be used for this project.')
  await page.locator('.order-row').getByRole('button', { name: 'Request payment gateway refund', exact: true }).click()
  await expect.poll(() => Boolean(refund)).toBe(true)
  if (leave) {
    await page.locator('#primary-navigation a[href="/workspace/assets"]').click()
    await expect(page).toHaveURL(/\/workspace\/assets$/)
    await expect(page.locator('main .asset-view-toolbar')).toBeVisible()
  }
  await drainResponse(page, () => refund!.fulfill({ json: { ...order, status: 'refund_requested' } }), `/orders/${orderID}/refund`)
  if (leave) await expect(page.locator('main .task-feedback.success')).toHaveCount(0)
  else {
    await expect(page.locator('.orders-workspace .task-feedback.success')).toContainText('Refund request accepted')
    await expect(page.locator('.order-row')).toHaveAttribute('data-order-status', 'refund_requested')
  }
  expect(unexpected).toEqual([])
})
}
