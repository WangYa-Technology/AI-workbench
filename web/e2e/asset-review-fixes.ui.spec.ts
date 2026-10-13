import { test, expect, type Page } from '@playwright/test'
const id = '00000000-0000-4000-8000-000000009801'
const row = (n: number) => ({ id: n === 1 ? id : `00000000-0000-4000-8000-${String(9800 + n).padStart(12, '0')}`, title: `Reviewed asset ${n}`, kind: 'image', sourceType: 'upload', mediaUrl: '/brand/logo.png', mimeType: 'image/png', scanStatus: n % 2 ? 'clean' : 'review', licenseCode: 'creator-owned', createdAt: '2026-09-20T00:00:00Z', versionNumber: 1, isLatestVersion: true, versions: [], usages: [] })
async function setup(page: Page, total = 1) {
  const state = { total, ownedFailure: false, savedFailure: false, refreshFailure: false, uploadMode: 'new', uploadCalls: 0, keys: [] as string[] }
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const u = new URL(route.request().url()), p = u.pathname
    const failure = () => route.fulfill({ status: 503, json: { error: { code: 'temporarily_unavailable', message: 'Unavailable', retryable: true } } })
    if (p === '/api/v1/auth/session') return route.fulfill({ json: { user: { id, email: 'review@fixture.test', handle: 'review', displayName: 'Review', role: 'creator', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: ['assets:upload'] } } })
    if (p === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: {} } })
    if (p === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (p === '/api/v1/assets') {
      if (state.ownedFailure || (state.refreshFailure && state.uploadCalls)) return failure()
      const start = Number(u.searchParams.get('cursor') || 0), end = Math.min(state.total, start + 20)
      return route.fulfill({ json: { items: Array.from({ length: end - start }, (_, i) => row(start + i + 1)), total: state.total, nextCursor: end < state.total ? String(end) : undefined } })
    }
    if (p === '/api/v1/assets/saved-works') return state.savedFailure ? failure() : route.fulfill({ json: { items: [] } })
    if (p === '/api/v1/assets/uploads') {
      state.uploadCalls++
      state.keys.push(route.request().headers()['idempotency-key'] || '')
      if (state.uploadCalls === 1 && state.uploadMode !== 'existing') state.total++
      if (state.uploadMode === 'lost' && state.uploadCalls === 1) return route.abort('connectionreset')
      return route.fulfill({ json: { ...row(state.uploadMode === 'existing' ? 1 : state.total), scanStatus: 'pending' } })
    }
    if (p.startsWith('/api/v1/assets/')) return route.fulfill({ json: { ...row(1), scanStatus: 'clean' } })
    return route.fulfill({ status: 500, json: { error: { code: 'unmocked', message: `Unmocked ${p}`, retryable: false } } })
  })
  return state
}
async function openList(page: Page) {
  await page.goto('/workspace/assets')
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
}
async function upload(page: Page) {
  await page.getByRole('button', { name: 'Upload Asset', exact: true }).click()
  await page.locator('.asset-upload-panel input[type="text"]').fill('图'.repeat(120))
  await page.locator('.asset-upload-panel input[type="file"]').setInputFiles({ name: 'review.txt', mimeType: 'text/plain', buffer: Buffer.from('review evidence') })
  await page.locator('.asset-upload-panel button[type="submit"]').click()
}
for (const total of [0, 1, 20, 21, 41]) test(`A02 total ${total} remains canonical while loaded counts grow`, async ({ page }) => {
  await setup(page, total); await openList(page)
  const owned = page.getByRole('tab', { name: 'Mine', exact: true }).locator('.catalog-view-count')
  await expect(owned).toContainText(String(total))
  await expect(page.locator('.catalog-toolbar [role=status]')).toHaveText(`${total} Assets`)
  await expect(page.locator('.asset-row')).toHaveCount(Math.min(20, total))
  for (let loaded = 20; loaded < total; loaded += 20) {
    await page.getByRole('button', { name: 'Load more', exact: true }).click()
    await expect(page.locator('.asset-row')).toHaveCount(Math.min(total, loaded + 20))
  }
  await expect(page.locator('.asset-row')).toHaveCount(total)
  await expect(owned).toContainText(String(total))
  await expect(page.locator('.asset-row .ui-card-tag').filter({ hasText: 'Scan passed' })).toHaveCount(Math.ceil(total / 2))
  await expect(page.locator('.page-hero-stats')).toHaveCount(0)
})
for (const mode of ['new', 'existing', 'lost', 'empty']) test(`A03 upload ${mode} reconciles total without duplicate increments`, async ({ page }) => {
  const state = await setup(page, mode === 'empty' ? 0 : 1); state.uploadMode = mode
  await openList(page); await upload(page)
  if (mode === 'lost') {
    await expect(page.locator('.asset-upload-panel button[type="submit"]')).toBeEnabled()
    await page.locator('.asset-upload-panel button[type="submit"]').click()
  }
  const expected = mode === 'existing' || mode === 'empty' ? 1 : 2
  await expect(page.locator('.catalog-toolbar [role=status]')).toHaveText(`${expected} Assets`)
  await expect(page.locator('.asset-row')).toHaveCount(expected)
  if (mode === 'lost') { expect(state.keys).toHaveLength(2); expect(state.keys[0]).toBeTruthy(); expect(state.keys[0]).toBe(state.keys[1]) }
})
test('A03 confirmed upload survives a failed count refresh', async ({ page }) => {
  const state = await setup(page); state.refreshFailure = true
  await openList(page); await upload(page)
  await expect(page.getByText('Upload confirmed, but the count could not be refreshed. Retry reading the list.')).toBeVisible()
  await expect(page.locator('.asset-row')).toHaveCount(2)
  await expect(page.locator('.catalog-toolbar [role=status]')).toHaveText('— Assets')
  state.refreshFailure = false
  await page.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(page.locator('.catalog-toolbar [role=status]')).toHaveText('2 Assets')
  expect(state.uploadCalls).toBe(1)
})
for (const failing of ['saved', 'owned']) test(`A04 ${failing} failure cannot hide the other source`, async ({ page }) => {
  const state = await setup(page); state.savedFailure = failing === 'saved'; state.ownedFailure = failing === 'owned'
  await openList(page)
  if (failing === 'saved') {
    await expect(page.locator('.asset-row')).toHaveCount(1)
    await page.getByRole('tab', { name: 'Saved' }).click()
  }
  await expect(page.locator('.asset-results [role="alert"]')).toBeVisible()
  if (failing === 'owned') {
    await page.getByRole('tab', { name: 'Saved' }).click()
    await expect(page.locator('.asset-results [role="alert"]')).toHaveCount(0)
    await page.getByRole('tab', { name: 'Mine' }).click()
  }
  state.savedFailure = false; state.ownedFailure = false
  await page.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(page.locator('.asset-results [role="alert"]')).toHaveCount(0)
})
for (const final of ['clean', 'review', 'rejected']) test(`A05 detail observes ${final} after slow scan and transient failure`, async ({ page }) => {
  await setup(page); let reads = 0, ready = false
  await page.route(`**/api/v1/assets/${id}`, route => {
    reads++
    if (reads === 3) return route.fulfill({ status: 503, json: { error: { code: 'temporarily_unavailable', message: 'Retry', retryable: true } } })
    return route.fulfill({ json: { ...row(1), scanStatus: ready ? final : 'pending', scanReason: `Read ${reads}` } })
  })
  const now = new Date('2026-09-27T00:00:00Z')
  await page.clock.install({ time: now })
  await page.clock.pauseAt(new Date(now.getTime() + 1000))
  await page.goto(`/workspace/assets/${id}`)
  await expect(page.locator('.asset-scan-state')).toContainText('Read 1')
  // Drain each network read before advancing the next timer. Advancing 20 s
  // in one call does not also flush browser/network promises.
  for (let n = 2; n <= 25; n++) {
    const response = page.waitForResponse(r => r.url().endsWith(`/assets/${id}`))
    await page.clock.runFor(800)
    await (await response).finished()
    if (n !== 3) await expect(page.locator('.asset-scan-state')).toContainText(`Read ${n}`)
    else await page.evaluate(() => Promise.resolve())
  }
  ready = true
  await page.clock.runFor(1000)
  if (final === 'clean') await expect(page.locator('.asset-scan-state')).toHaveCount(0)
  else await expect(page.locator('.asset-scan-state')).toHaveAttribute('data-status', final)
  const settled = reads; await page.clock.runFor(5000); expect(reads).toBe(settled)
})
test('A05 timeout has visible refresh and does not pretend completion', async ({ page }) => {
  await setup(page); let ready = false
  await page.route(`**/api/v1/assets/${id}`, route => route.fulfill({ json: { ...row(1), scanStatus: ready ? 'clean' : 'pending' } }))
  await page.clock.install(); await page.goto(`/workspace/assets/${id}`)
  await expect(page.locator('.asset-scan-state')).toBeVisible()
  await page.clock.runFor(61_000)
  const retry = page.getByRole('button', { name: 'Refresh scan status' })
  await expect(retry).toBeVisible(); ready = true
  await retry.click(); await page.clock.runFor(1000)
  await expect(page.locator('.asset-scan-state')).toHaveCount(0)
})
test('A08 known and unknown licenses provide honest scope and a working policy destination', async ({ page }) => {
  await setup(page)
  await page.goto(`/workspace/assets/${id}`)
  await expect(page.getByText('An ownership record is not a copyright guarantee.', { exact: false })).toBeVisible()
  await expect(page.getByRole('link', { name: 'View platform license policy' })).toHaveAttribute('href', '/policies/licensing')
  await page.route(`**/api/v1/assets/${id}`, route => route.fulfill({ json: { ...row(1), licenseCode: 'future-unknown' } }))
  await page.reload()
  await expect(page.locator('.asset-detail-card dd').filter({ hasText: 'License unverified' })).toBeVisible()
  await expect(page.getByText('Unknown or unverified license.', { exact: false })).toBeVisible()
})

for (const source of ['local_test', 'openai', 'upload', 'unknown']) test(`A01 ${source} disclosure follows evidence without a billing claim`, async ({ page }) => {
  await setup(page)
  await page.route('**/api/v1/task-types?scope=community', route => route.fulfill({ json: { items: [{ code: 'general', nameEn: 'General', nameZh: '综合' }] } }))
  await page.route('**/api/v1/content-drafts', route => route.fulfill({ json: { items: [] } }))
  await page.route('**/api/v1/community/capabilities', route => route.fulfill({ json: { canSaveDraft: true, canPublish: true } }))
  await page.route(`**/api/v1/assets/${id}`, route => route.fulfill({ json: { ...row(1), sourceType: source === 'upload' ? 'upload' : 'generation', provenance: source === 'local_test' || source === 'openai' ? { generation: { provider: source, modelName: 'fixture-model' } } : undefined } }))
  await page.goto(`/workspace/assets?publish=${id}`)
  const disclosure = page.getByRole('dialog').getByLabel('AI disclosure', { exact: true })
  if (source === 'upload' || source === 'unknown') {
    await expect(disclosure).toHaveValue('')
    let writes = 0
    page.on('request', request => { if (request.method() === 'POST') writes++ })
    await page.getByRole('dialog').getByRole('button', { name: 'Publish work', exact: true }).click()
    await expect(disclosure).toBeFocused()
    expect(writes).toBe(0)
  } else {
    await expect(disclosure).toHaveValue(new RegExp(`${source} / fixture-model`))
    await expect(disclosure).not.toHaveValue(/paid|charge/)
  }
})
for (const code of ['hcai-commercial-v1', 'hcai-commercial-standard-v1']) test(`A08 ${code} purchase opens frozen order terms`, async ({ page }) => {
  await setup(page)
  const order = { id, productId: id, productTitle: 'Contract source', assetId: id, status: 'fulfilled', amountCents: 1900, currency: 'USD', licenseName: 'Frozen order license', licenseCode: code, licenseVersion: '1', licenseTerms: 'Frozen contract: attribution is required; source redistribution is prohibited.', refundWindowDays: 7, canRequestRefund: false, paymentMode: 'test', realCharge: false, createdAt: '2026-09-20T00:00:00Z', events: [] }
  await page.route(`**/api/v1/assets/${id}`, route => route.fulfill({ json: { ...row(1), sourceType: 'purchase', licenseCode: code, provenance: { purchase: { orderId: id, productId: id, productTitle: order.productTitle, licenseName: order.licenseName, licenseCode: code, paymentMode: 'test', realCharge: false, orderStatus: 'fulfilled', canDownload: false, canReuse: false, grantedAt: order.createdAt } } } }))
  await page.route('**/api/v1/orders*', route => route.fulfill({ json: { items: [order] } }))
  await page.route(`**/api/v1/orders/${id}`, route => route.fulfill({ json: order }))
  await page.goto(`/workspace/assets/${id}`)
  await page.getByRole('link', { name: 'View order evidence', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/workspace/orders\\?orderId=${id}`))
  await expect(page.locator('.order-license-panel')).toContainText(order.licenseTerms)
})
