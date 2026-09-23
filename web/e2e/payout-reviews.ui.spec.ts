import { expect, test, type Page, type Route } from '@playwright/test'
import type { components } from '../src/api/schema'

type Item = components['schemas']['SellerPayoutReviewItem']
const requestId = '00000000-0000-4000-8000-000000009901'
const secondId = '00000000-0000-4000-8000-000000009902'
const actor = (id = 'finance-a', finance = true) => ({ id, handle: id, email: `${id}@example.test`, displayName: id, role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: ['admin:access', ...(finance ? ['admin:finance'] : [])] })
const item: Item = { canAdmitFunding: false, id: requestId, sellerId: 'seller-private', settlementId: secondId, amountCents: 1852, currency: 'USD', status: 'under_review', environment: 'test', bankDestinationId: 'ba_original123', bankName: 'Frozen Review Bank', last4: '6789', createdAt: '2026-09-20T08:00:00Z' }
const review = (decision: 'approved' | 'rejected', revision = 1) => ({ id: secondId, payoutRequestId: requestId, revision, actorId: 'finance-a', decision, reason: 'Verified original settlement and bank.', createdAt: '2026-09-22T08:00:00Z' })
const denied = { error: { code: 'forbidden', message: 'Fixture denial', retryable: false } }
const sellerMessage = 'Your reservation was reviewed. No bank payout was sent.'
type MountedApp = HTMLElement & { __vue_app__: { config: { globalProperties: {
  $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> }
  $router: { push: (path: string) => Promise<unknown> }
} } } }
async function refreshSession(page: Page) {
  await page.evaluate(async () => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true) })
}
async function navigate(page: Page, path: string) {
  await page.evaluate(async path => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$router.push(path) }, path)
}
async function settle(page: Page, route: Route, json: unknown, status = 200) {
  const response = page.waitForResponse(r => r.url() === route.request().url() && r.request().method() === route.request().method())
  await route.fulfill({ json, status }); await (await response).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined = () => undefined, locale = 'en-US') {
  const unexpected: string[] = []; const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(locale => localStorage.setItem('hcai-locale', locale), locale)
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url()); const handled = handler(route, url)
    if (handled) return handled
    if (route.request().method() === 'GET') {
      if (url.pathname.endsWith('/source-reversal')) return route.fulfill({ json: { request: fundedItem(), expectedUpdatedAt: '2026-09-23T00:00:00Z', canSubmit: false, canClose: false, requiresReview: false } })
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request: fundedItem(), canSubmit: false } })
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: { ...actor(), locale } } })
      if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
      if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
      if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (url.pathname === '/api/v1/admin/seller-payout-requests') return route.fulfill({ json: { items: [item] } })
      if (url.pathname === `/api/v1/admin/seller-payout-requests/${requestId}`) return route.fulfill({ json: item })
    }
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 403, json: denied })
  })
  return { unexpected, errors }
}
async function prepare(page: Page, decision = 'Approve reservation', reason = 'Verified original settlement and bank.') {
  await page.getByRole('textbox', { name: 'Message to seller', exact: true }).fill(sellerMessage)
  await page.getByRole('textbox', { name: 'Review reason', exact: true }).fill(reason)
  await page.getByRole('button', { name: decision, exact: true }).click()
  await expect(page.getByRole('alertdialog')).toBeVisible()
}

for (const width of [390, 1308]) {
  test(`reviews exact request, separate funds state and subsequent rejection at ${width}px`, async ({ page }) => {
    const zh = width === 390
    await page.setViewportSize({ width, height: 901 })
    const posts: Record<string, unknown>[] = []
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/review')) {
        const body = route.request().postDataJSON(); posts.push(body)
        expect(route.request().headers()['idempotency-key']).toBeTruthy()
        const latestReview = review(body.decision, posts.length)
        return route.fulfill({ json: { review: latestReview, request: { ...item, latestReview, status: body.decision === 'approved' ? 'under_review' : 'cancelled' }, replayed: false } })
      }
    }, zh ? 'zh-CN' : 'en-US')
    await page.goto('/admin/payouts')
    await page.getByRole('link', { name: zh ? '查看并复核' : 'Review request', exact: true }).click()
    const reason = page.getByRole('textbox', { name: zh ? '复核原因' : 'Review reason', exact: true })
    const approve = page.getByRole('button', { name: zh ? '批准预留申请' : 'Approve reservation', exact: true })
    await expect(approve).toBeDisabled()
    const message = page.getByRole('textbox', { name: zh ? '给卖家的说明' : 'Message to seller', exact: true })
    await message.fill(sellerMessage)
    await reason.fill('🙂'.repeat(9)); await expect(approve).toBeDisabled()
    await reason.fill('🙂'.repeat(10)); await expect(approve).toBeEnabled()
    await page.screenshot({ path: `/tmp/hcai-payout-review-${width}.png`, animations: 'disabled' })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await approve.click()
    await expect(page.getByRole('alertdialog')).toContainText(item.bankDestinationId)
    await expect(page.getByRole('alertdialog')).toContainText('Frozen Review Bank · •••• 6789')
    await page.getByRole('button', { name: zh ? '继续编辑' : 'Continue editing', exact: true }).click()
    await reason.fill('Verified original settlement and bank.')
    await approve.click()
    await page.screenshot({ path: `/tmp/hcai-payout-review-confirm-${width}.png`, animations: 'disabled' })
    await page.getByRole('button', { name: zh ? '确认提交决定' : 'Confirm decision', exact: true }).click()
    await expect(page.getByText(zh ? '复核决定已记录。申请当前状态：待审核。' : 'Decision recorded. Current request status: Under review.', { exact: true })).toBeVisible()
    await reason.fill('Reject after verifying the updated information.')
    await message.fill(sellerMessage)
    await expect(approve).toBeDisabled()
    await page.getByRole('button', { name: zh ? '驳回并释放预留' : 'Reject and release', exact: true }).click()
    await page.getByRole('button', { name: zh ? '确认提交决定' : 'Confirm decision', exact: true }).click()
    await expect(page.getByText(zh ? '复核决定已记录。申请当前状态：已取消。' : 'Decision recorded. Current request status: Cancelled.', { exact: true })).toBeVisible()
    expect(posts).toEqual([
      { expectedRevision: 0, settlementId: secondId, amountCents: 1852, bankDestinationId: item.bankDestinationId, decision: 'approved', reason: 'Verified original settlement and bank.', sellerMessage },
      { expectedRevision: 1, settlementId: secondId, amountCents: 1852, bankDestinationId: item.bankDestinationId, decision: 'rejected', reason: 'Reject after verifying the updated information.', sellerMessage },
    ])
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('long private and seller explanations keep mobile confirmation actions reachable', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 700 })
  const observed = await mockApp(page)
  await page.goto(`/admin/payouts/${requestId}`)
  await page.getByRole('textbox', { name: 'Review reason', exact: true }).fill('🙂'.repeat(1000))
  await page.getByRole('textbox', { name: 'Message to seller', exact: true }).fill('🙂'.repeat(1001))
  await expect(page.getByRole('button', { name: 'Approve reservation', exact: true })).toBeDisabled()
  await page.getByRole('textbox', { name: 'Message to seller', exact: true }).fill('🙂'.repeat(1000))
  await page.getByRole('button', { name: 'Approve reservation', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Confirm decision', exact: true })).toBeInViewport()
  await expect(page.getByRole('button', { name: 'Continue editing', exact: true })).toBeInViewport()
  expect(await page.locator('.ui-alert-dialog__body').evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true)
  await page.screenshot({ path: '/tmp/hcai-payout-long-confirmation.png', animations: 'disabled' })
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('unknown outcome locks input and retries the original key; replay shows current cancellation', async ({ page }) => {
  const keys: string[] = []; const bodies: unknown[] = []
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/review')) {
      keys.push(route.request().headers()['idempotency-key']); bodies.push(route.request().postDataJSON())
      if (keys.length === 1) return route.abort('failed')
      return route.fulfill({ json: { review: review('approved'), request: { ...item, status: 'cancelled', latestReview: review('rejected', 2) }, replayed: true } })
    }
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepare(page)
  await page.getByRole('button', { name: 'Confirm decision', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Review reason', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Retry original decision', exact: true }).click()
  await expect(page.getByText('Original decision recovered. Current request status: Cancelled.', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Retry original decision', exact: true })).toHaveCount(0)
  expect(keys[0]).toBe(keys[1]); expect(bodies[0]).toEqual(bodies[1])
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('version conflict refreshes detail and requires a new explicit decision', async ({ page }) => {
  let conflicted = false; let posts = 0
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/review')) {
      posts++; conflicted = true
      return route.fulfill({ status: 409, json: { error: { code: 'seller_payout_review_conflict', message: 'Changed', retryable: false } } })
    }
    if (conflicted && url.pathname.endsWith(requestId)) return route.fulfill({ json: { ...item, latestReview: review('approved') } })
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepare(page)
  await page.getByRole('button', { name: 'Confirm decision', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Latest decision' })).toContainText('Revision 1')
  await expect(page.getByRole('textbox', { name: 'Review reason', exact: true })).toHaveValue('')
  await expect(page.getByRole('button', { name: 'Retry original decision', exact: true })).toHaveCount(0)
  expect(posts).toBe(1); expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('pagination failure retains rows and self review, missing allocation, terminal state and unbound approval are gated', async ({ page }) => {
  let current = { ...item, bankDestinationId: '' }; let nextReads = 0
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/seller-payout-requests')) {
      if (url.searchParams.has('cursor')) {
        if (++nextReads === 1) return route.fulfill({ status: 500, json: { error: { code: 'unavailable' } } })
        return route.fulfill({ json: { items: [item, { ...item, id: secondId }] } })
      }
      return route.fulfill({ json: { items: [item], nextCursor: requestId } })
    }
    if (url.pathname.endsWith(requestId)) return route.fulfill({ json: current })
  })
  await page.goto('/admin/payouts')
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.getByRole('link', { name: 'Review request', exact: true })).toHaveCount(1)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.getByRole('link', { name: 'Review request', exact: true })).toHaveCount(2)
  await page.getByRole('link', { name: 'Review request', exact: true }).first().click()
  await page.getByRole('textbox', { name: 'Review reason', exact: true }).fill('Missing bank, reject this request.')
  await page.getByRole('textbox', { name: 'Message to seller', exact: true }).fill(sellerMessage)
  await expect(page.getByRole('button', { name: 'Approve reservation', exact: true })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Reject and release', exact: true })).toBeEnabled()
  for (const state of [{ ...item, sellerId: actor().id }, { ...item, status: 'processing' as const }, { ...item, settlementId: '00000000-0000-0000-0000-000000000000' }]) {
    current = state; await refreshSession(page)
    await expect(page.getByRole('textbox', { name: 'Review reason', exact: true })).toHaveCount(0)
  }
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

for (const change of ['account', 'permission', 'route', 'denial', 'same-account'] as const) {
  test(`late review response cannot repopulate private data after ${change}`, async ({ page }) => {
    let user = actor(); let held: Route | undefined
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
      if (url.pathname.endsWith('/review')) { held = route; return Promise.resolve() }
      if (url.pathname.endsWith(requestId) && user.id === 'finance-b') return route.fulfill({ json: { ...item, sellerId: 'new-seller', bankDestinationId: 'ba_newbank' } })
    })
    await page.goto(`/admin/payouts/${requestId}`); await prepare(page)
    await page.getByRole('button', { name: 'Confirm decision', exact: true }).click()
    await expect.poll(() => !!held).toBe(true)
    if (change === 'account') { user = actor('finance-b'); await refreshSession(page) }
    if (change === 'permission') { user = actor('finance-a', false); await refreshSession(page) }
    if (change === 'same-account') await refreshSession(page)
    if (change === 'route') await navigate(page, '/admin/payouts')
    await settle(page, held!, change === 'denial' ? denied : { review: review('approved'), request: { ...item, latestReview: review('approved') }, replayed: false }, change === 'denial' ? 403 : 200)
    await expect(page.getByText('Decision recorded. Current request status: Under review.', { exact: true })).toHaveCount(0)
    await expect(page.getByRole('region', { name: 'Latest decision' })).toHaveCount(0)
    if (change === 'account') {
      await expect(page.getByText('ba_newbank', { exact: true })).toBeVisible()
      await expect(page.getByText(item.bankDestinationId, { exact: true })).toHaveCount(0)
    } else if (!['route', 'same-account'].includes(change)) {
      await expect(page.getByText(item.sellerId, { exact: true })).toHaveCount(0)
      await expect(page.getByRole('textbox', { name: 'Review reason', exact: true })).toHaveCount(0)
    }
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('a stale private directory cannot return after revocation during loading', async ({ page }) => {
  let user = actor(); let held: Route | undefined
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
    if (url.pathname.endsWith('/seller-payout-requests')) { held = route; return Promise.resolve() }
  })
  await page.goto('/admin/payouts'); await expect.poll(() => !!held).toBe(true)
  user = actor('finance-a', false); await refreshSession(page)
  await settle(page, held!, { items: [item], nextCursor: requestId })
  await expect(page.getByRole('heading', { name: 'Finance access required', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Review request', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('revocation while confirming closes the dialog without sending a decision', async ({ page }) => {
  let user = actor(); let posts = 0
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
    if (url.pathname.endsWith('/review')) { posts++; return route.fulfill({ status: 403, json: denied }) }
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepare(page)
  user = actor('finance-a', false); await refreshSession(page)
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Finance access required', exact: true })).toBeVisible()
  expect(posts).toBe(0); expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('a finance permission denial blocks private reads before fetching', async ({ page }) => {
  let privateReads = 0
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user: actor('member', false) } })
    if (url.pathname.includes('/admin/seller-payout-requests')) { privateReads++; return route.fulfill({ json: item }) }
  })
  await page.goto(`/admin/payouts/${requestId}`)
  await expect(page.getByRole('heading', { name: 'Finance access required', exact: true })).toBeVisible()
  expect(privateReads).toBe(0); expect(observed).toEqual({ unexpected: [], errors: [] })
})

for (const kind of ['legacy', 'unnamed', 'literal'] as const) {
  test(`bank summary in finance detail and confirmation preserves ${kind} evidence`, async ({ page }) => {
    const bankName = kind === 'literal' ? '<img src=x onerror=alert(1)>' : undefined
    const last4 = kind === 'legacy' ? undefined : '0007'
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith(requestId)) return route.fulfill({ json: { ...item, bankName, last4 } })
    })
    await page.goto(`/admin/payouts/${requestId}`)
    const expected = kind === 'legacy' ? 'Bank name and last four digits were not recorded' : `${bankName || 'Receiving bank'} · •••• 0007`
    await expect(page.locator('.payout-bank-summary')).toContainText(expected)
    await prepare(page)
    const summary = page.getByRole('alertdialog').locator('.payout-bank-summary')
    await expect(summary).toContainText(expected)
    await expect(summary).toContainText(item.bankDestinationId)
    await expect(summary.locator('img')).toHaveCount(0)
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

const approvedItem: Item = { ...item, canAdmitFunding: true, latestReview: review('approved') }
const fundingReason = 'Explicitly authorize the reviewed original source.'
const fundingBody = { reviewId: secondId, expectedRevision: 1, settlementId: secondId, amountCents: 1852, bankDestinationId: item.bankDestinationId, reason: fundingReason, confirmed: true }
const fundedItem = (status = 'requested', jobStatus = 'queued'): Item => ({ ...approvedItem, canAdmitFunding: false, funding: { transferId: 'source-original', admissionId: 'admission-original', jobId: 'job-original', jobStatus, status } })
const fundingResult = (request = fundedItem(), replayed = false) => ({ admission: { id: 'admission-original' }, jobId: 'job-original', request, replayed })
async function prepareFunding(page: Page) {
  await page.getByRole('textbox', { name: 'Funding reason', exact: true }).fill(fundingReason)
  await page.getByRole('button', { name: 'Initiate source funding', exact: true }).click()
  await expect(page.getByRole('alertdialog')).toBeVisible()
}

for (const width of [390, 1308]) for (const locale of ['zh-CN', 'en-US']) {
  test(`funding explicit confirmation at ${width}px ${locale}`, async ({ page }) => {
    const zh = locale === 'zh-CN'; const bodies: unknown[] = []
    await page.setViewportSize({ width, height: 901 })
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith(requestId)) return route.fulfill({ json: approvedItem })
      if (url.pathname.endsWith('/funding')) {
        bodies.push(route.request().postDataJSON()); expect(route.request().headers()['idempotency-key']).toBeTruthy()
        return route.fulfill({ json: fundingResult() })
      }
    }, locale)
    await page.goto(`/admin/payouts/${requestId}`)
    const reason = page.getByRole('textbox', { name: zh ? '划转原因' : 'Funding reason', exact: true })
    const start = page.getByRole('button', { name: zh ? '发起来源划转' : 'Initiate source funding', exact: true })
    await expect(start).toBeDisabled(); await reason.fill('🙂'.repeat(9)); await expect(start).toBeDisabled()
    await reason.fill(fundingReason); await start.click()
    const dialog = page.getByRole('alertdialog')
    for (const value of [item.sellerId, secondId, item.bankDestinationId, 'Frozen Review Bank · •••• 6789', fundingReason]) await expect(dialog).toContainText(value)
    await expect(dialog).toContainText(zh ? '第 1 版' : 'Revision 1'); expect(bodies).toHaveLength(0)
    await page.getByRole('button', { name: zh ? '继续编辑' : 'Continue editing', exact: true }).click()
    expect(bodies).toHaveLength(0); await start.click()
    const confirm = page.getByRole('button', { name: zh ? '确认并排队划转' : 'Confirm and queue transfer', exact: true })
    await expect(confirm).toBeInViewport()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/hcai-funding-confirm-${width}-${locale}.png`, animations: 'disabled' })
    await confirm.click()
    const source = page.getByRole('region', { name: zh ? '来源划转' : 'Source funding', exact: true })
    await expect(source).toContainText('source-original')
    await expect(source).toContainText(zh ? '来源确认本身不代表银行到账' : 'Source confirmation alone does not confirm bank arrival')
    await expect(start).toHaveCount(0)
    expect(bodies).toEqual([fundingBody]); expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('unknown funding locks both commands and retries only the original snapshot', async ({ page }) => {
  const keys: string[] = []; const bodies: unknown[] = []
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith(requestId)) return route.fulfill({ json: approvedItem })
    if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request: fundedItem('processing', 'running'), canSubmit: false } })
    if (url.pathname.endsWith('/funding')) {
      keys.push(route.request().headers()['idempotency-key']); bodies.push(route.request().postDataJSON())
      if (keys.length === 1) return route.abort('failed')
      return route.fulfill({ json: fundingResult(fundedItem('processing', 'running'), true) })
    }
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepareFunding(page)
  await page.getByRole('button', { name: 'Confirm and queue transfer', exact: true }).click()
  for (const name of ['Funding reason', 'Review reason', 'Message to seller']) await expect(page.getByRole('textbox', { name, exact: true })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Reload', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Retry original funding admission', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Source funding', exact: true })).toContainText('Transfer in progress')
  expect(keys[1]).toBe(keys[0]); expect(bodies).toEqual([fundingBody, fundingBody]); expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('funding conflict refreshes approval and requires another deliberate confirmation', async ({ page }) => {
  let conflict = false; let posts = 0
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith(requestId)) return route.fulfill({ json: { ...approvedItem, latestReview: review('approved', conflict ? 2 : 1) } })
    if (url.pathname.endsWith('/funding')) { posts++; conflict = true; return route.fulfill({ status: 409, json: { error: { code: 'seller_funding_admission_conflict', message: 'Approval changed' } } }) }
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepareFunding(page)
  await page.getByRole('button', { name: 'Confirm and queue transfer', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Latest decision', exact: true })).toContainText('Revision 2')
  await expect(page.getByRole('textbox', { name: 'Funding reason', exact: true })).toHaveValue('')
  await expect(page.getByRole('button', { name: 'Retry original funding admission', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Initiate source funding', exact: true })).toBeDisabled()
  expect(posts).toBe(1); expect(observed).toEqual({ unexpected: [], errors: [] })
})

for (const change of ['account', 'permission', 'route', 'denial', 'same-account'] as const) {
  test(`late funding response suppressed after ${change}`, async ({ page }) => {
    let user = actor(); let held: Route | undefined
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
      if (url.pathname.endsWith(requestId)) return route.fulfill({ json: user.id === 'finance-b' ? { ...item, sellerId: 'new-seller', bankDestinationId: 'ba_newbank' } : approvedItem })
      if (url.pathname.endsWith('/funding')) { held = route; return Promise.resolve() }
    })
    await page.goto(`/admin/payouts/${requestId}`); await prepareFunding(page)
    await page.getByRole('button', { name: 'Confirm and queue transfer', exact: true }).click()
    await expect.poll(() => !!held).toBe(true)
    if (change === 'account') { user = actor('finance-b'); await refreshSession(page) }
    if (change === 'permission') { user = actor('finance-a', false); await refreshSession(page) }
    if (change === 'same-account') await refreshSession(page)
    if (change === 'route') await navigate(page, '/admin/payouts')
    await settle(page, held!, change === 'denial' ? denied : fundingResult(), change === 'denial' ? 403 : 200)
    await expect(page.getByRole('region', { name: 'Source funding', exact: true })).toHaveCount(0)
    await expect(page.getByText('Source transfer recorded and queued. Reload to follow progress; the bank payout is not complete.', { exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Retry original funding admission', exact: true })).toHaveCount(0)
    if (change === 'account') await expect(page.getByText('ba_newbank', { exact: true })).toBeVisible()
    if (['permission', 'denial'].includes(change)) await expect(page.getByRole('textbox', { name: 'Funding reason', exact: true })).toHaveCount(0)
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('revocation closes funding confirmation without sending', async ({ page }) => {
  let user = actor(); let posts = 0
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
    if (url.pathname.endsWith(requestId)) return route.fulfill({ json: approvedItem })
    if (url.pathname.endsWith('/funding')) { posts++; return route.fulfill({ status: 403, json: denied }) }
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepareFunding(page)
  user = actor('finance-a', false); await refreshSession(page)
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Finance access required', exact: true })).toBeVisible()
  expect(posts).toBe(0); expect(observed).toEqual({ unexpected: [], errors: [] })
})

for (const state of ['succeeded', 'recovered', 'reconciliation_required', 'legacy'] as const) {
  test(`existing ${state} source is read-only and does not imply bank payout`, async ({ page }) => {
    const request = fundedItem(state === 'legacy' ? 'processing' : state === 'recovered' ? 'succeeded' : state, state === 'succeeded' ? 'succeeded' : 'failed')
    if (state === 'legacy') { delete request.funding!.admissionId; delete request.funding!.jobId }
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith(requestId)) return route.fulfill({ json: request })
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request, canSubmit: false } })
    })
    await page.goto(`/admin/payouts/${requestId}`)
    const source = page.getByRole('region', { name: 'Source funding', exact: true })
    await expect(source).toContainText('Source confirmation alone does not confirm bank arrival.')
    await expect(page.getByRole('button', { name: 'Initiate source funding', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Reject and release', exact: true })).toHaveCount(0)
    if (state === 'succeeded' || state === 'recovered') {
      await expect(source).toContainText('Transferred to seller account')
      await expect(source).not.toContainText('needs reconciliation')
    }
    else await expect(source).toContainText('needs reconciliation')
    if (state === 'legacy') await expect(source).toContainText('lacks admission or job evidence')
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

type BankOperation = components['schemas']['SellerBankPayoutOperation']
const bankRequest = (): Item => ({ ...fundedItem('succeeded', 'succeeded'), status: 'processing' })
const bankOperation = (): BankOperation => ({ request: bankRequest(), canSubmit: true, canResume: false })
const bankReason = 'Authorize the original source and frozen receiving bank.'
const bankBody = { sourceTransferId: 'source-original', reviewId: secondId, expectedRevision: 1, amountCents: 1852, bankDestinationId: item.bankDestinationId, reason: bankReason, confirmed: true }
const queuedBank = (): BankOperation => ({ request: bankRequest(), canSubmit: false, canResume: false, bank: { commandId: 'bank-command-original', createdAt: '2026-09-23T00:00:00Z', jobId: 'bank-job-original', jobStatus: 'queued', requiresReview: false } })
const stoppedBank = (): BankOperation => ({ ...queuedBank(), canResume: true, bank: { ...queuedBank().bank!, jobStatus: 'cancelled' } })
const resumedBank = (): BankOperation => ({ ...stoppedBank(), canResume: false, bank: { ...stoppedBank().bank!, resume: { id: 'resume-original', commandId: 'bank-command-original', predecessorJobId: 'bank-job-original', jobId: 'bank-job-resumed', revision: 1, createdAt: '2026-09-23T00:01:00Z', jobStatus: 'queued' } } })

for (const width of [390, 1308]) {
  test(`bank continuation confirms the original command and retains its predecessor at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 901 })
    const zh = width === 390
    const posts: unknown[] = []
    const observed = await mockBank(page, (route, url) => {
      if (url.pathname.endsWith('/bank-payout/resume')) {
        posts.push(route.request().postDataJSON())
        return route.fulfill({ json: { resume: resumedBank().bank!.resume, operation: resumedBank(), replayed: false } })
      }
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: stoppedBank() })
    }, zh ? 'zh-CN' : 'en-US')
    await page.goto(`/admin/payouts/${requestId}`)
    const reason = page.getByRole('textbox', { name: zh ? '银行出款原因' : 'Bank payout reason', exact: true })
    const prepare = page.getByRole('button', { name: zh ? '恢复停止的出款任务' : 'Resume stopped bank job', exact: true })
    await expect(prepare).toBeDisabled()
    await reason.fill(bankReason); await prepare.click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('bank-command-original')
    await expect(dialog).toContainText('bank-job-original')
    await expect(dialog).toContainText('18.52')
    await expect(dialog).toContainText('Frozen Review Bank · •••• 6789')
    await page.getByRole('button', { name: zh ? '继续编辑' : 'Continue editing', exact: true }).click()
    expect(posts).toEqual([])
    await prepare.click()
    const confirm = page.getByRole('button', { name: zh ? '确认并创建恢复任务' : 'Confirm and queue continuation', exact: true })
    await expect(confirm).toBeInViewport()
    await page.screenshot({ path: `/tmp/hcai-bank-resume-${width}.png`, animations: 'disabled' })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await confirm.click()
    await expect(page.getByText(zh ? '恢复记录和新任务已保存。原任务继续保留，请跟进银行结果。' : 'The continuation and new job were saved. The original job is retained; follow the bank result.', { exact: true })).toBeVisible()
    await expect(page.getByRole('region', { name: zh ? '银行出款' : 'Bank payout', exact: true })).toContainText(zh ? '恢复任务 · 第 1 次' : 'Continuation · revision 1')
    await expect(prepare).toHaveCount(0)
    expect(posts).toEqual([{ commandId: 'bank-command-original', expectedJobId: 'bank-job-original', reason: bankReason, confirmed: true }])
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('bank continuation retries an unknown response with the original key and snapshot', async ({ page }) => {
  const keys: string[] = [], bodies: unknown[] = []
  const observed = await mockBank(page, (route, url) => {
    if (url.pathname.endsWith('/bank-payout/resume')) {
      keys.push(route.request().headers()['idempotency-key']!); bodies.push(route.request().postDataJSON())
      if (keys.length === 1) return route.fulfill({ status: 502, json: { error: { code: 'unavailable', message: 'Response unknown' } } })
      return route.fulfill({ json: { resume: resumedBank().bank!.resume, operation: resumedBank(), replayed: true } })
    }
    if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: stoppedBank() })
  })
  await page.goto(`/admin/payouts/${requestId}`)
  await page.getByRole('textbox', { name: 'Bank payout reason', exact: true }).fill(bankReason)
  await page.getByRole('button', { name: 'Resume stopped bank job', exact: true }).click()
  await page.getByRole('button', { name: 'Confirm and queue continuation', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Bank payout reason', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Retry original bank command', exact: true }).click()
  await expect(page.getByText('Recovered the original continuation without creating another job. The current state appears below.', { exact: true })).toBeVisible()
  expect(keys).toHaveLength(2); expect(keys[0]).toBeTruthy(); expect(keys[1]).toBe(keys[0]); expect(bodies[1]).toEqual(bodies[0])
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

for (const status of [401, 403, 404, 409, 422]) {
  test(`bank continuation ${status} clears the pending command without another dispatch`, async ({ page }) => {
    let posts = 0
    const observed = await mockBank(page, (route, url) => {
      if (url.pathname.endsWith('/bank-payout/resume')) { posts++; return route.fulfill({ status, json: denied }) }
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { ...stoppedBank(), canResume: posts === 0 } })
    })
    await page.goto(`/admin/payouts/${requestId}`)
    await page.getByRole('textbox', { name: 'Bank payout reason', exact: true }).fill(bankReason)
    await page.getByRole('button', { name: 'Resume stopped bank job', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm and queue continuation', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Retry original bank command', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Resume stopped bank job', exact: true })).toHaveCount(0)
    if (status < 409) await expect(page.getByRole('region', { name: 'Bank payout', exact: true })).toHaveCount(0)
    expect(posts).toBe(1); expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('a late bank continuation response cannot disclose the previous operator context', async ({ page }) => {
  let held: Route | undefined
  let user = actor()
  const observed = await mockBank(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user } })
    if (url.pathname.endsWith('/bank-payout/resume')) { held = route; return Promise.resolve() }
    if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: stoppedBank() })
  })
  await page.goto(`/admin/payouts/${requestId}`)
  await page.getByRole('textbox', { name: 'Bank payout reason', exact: true }).fill(bankReason)
  await page.getByRole('button', { name: 'Resume stopped bank job', exact: true }).click()
  await page.getByRole('button', { name: 'Confirm and queue continuation', exact: true }).click()
  await expect.poll(() => !!held).toBe(true)
  user = actor('next-user', false); await refreshSession(page)
  const operation = { ...resumedBank(), request: { ...bankRequest(), sellerId: 'private-old-seller', bankName: 'Private Old Bank' } }
  await settle(page, held!, { resume: resumedBank().bank!.resume, operation, replayed: false })
  await expect(page.getByText('private-old-seller', { exact: true })).toHaveCount(0)
  await expect(page.getByText('Private Old Bank', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Bank payout', exact: true })).toHaveCount(0)
  expect(observed).toEqual({ unexpected: [], errors: [] })
})
const bankSubmission = (operation = queuedBank(), replayed = false) => ({ command: { id: 'bank-command-original' }, jobId: 'bank-job-original', operation, replayed })
async function mockBank(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined = () => undefined, locale = 'en-US') {
  return mockApp(page, (route, url) => {
    const handled = handler(route, url)
    if (handled) return handled
    if (route.request().method() === 'GET') {
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: bankOperation() })
      if (url.pathname.endsWith(requestId)) return route.fulfill({ json: bankRequest() })
    }
  }, locale)
}
async function prepareBank(page: Page) {
  await page.getByRole('textbox', { name: 'Bank payout reason', exact: true }).fill(bankReason)
  await page.getByRole('button', { name: 'Authorize bank payout', exact: true }).click()
  await expect(page.getByRole('alertdialog')).toBeVisible()
}

for (const width of [390, 1308]) for (const locale of ['zh-CN', 'en-US']) {
  test(`bank payout confirms exact immutable evidence at ${width}px ${locale}`, async ({ page }) => {
    const zh = locale === 'zh-CN'; const posts: unknown[] = []
    await page.setViewportSize({ width, height: 901 })
    const observed = await mockBank(page, (route, url) => {
      if (url.pathname.endsWith('/bank-payout') && route.request().method() === 'POST') {
        posts.push(route.request().postDataJSON()); expect(route.request().headers()['idempotency-key']).toBeTruthy()
        return route.fulfill({ json: bankSubmission() })
      }
    }, locale)
    await page.goto(`/admin/payouts/${requestId}`)
    const reason = page.getByRole('textbox', { name: zh ? '银行出款原因' : 'Bank payout reason', exact: true })
    const start = page.getByRole('button', { name: zh ? '授权银行出款' : 'Authorize bank payout', exact: true })
    await expect(start).toBeDisabled(); await reason.fill('🙂'.repeat(9)); await expect(start).toBeDisabled()
    await reason.fill(bankReason); await start.click()
    const dialog = page.getByRole('alertdialog')
    for (const text of [item.sellerId, requestId, 'source-original', secondId, item.bankDestinationId, 'Frozen Review Bank · •••• 6789', bankReason, '18.52']) await expect(dialog).toContainText(text)
    await expect(dialog).toContainText(zh ? '测试' : 'Test')
    await expect(dialog).toContainText(zh ? '第 1 版' : 'Revision 1')
    expect(posts).toHaveLength(0)
    await expect(page.getByRole('button', { name: zh ? '重新加载' : 'Reload', exact: true })).toBeDisabled()
    await page.getByRole('button', { name: zh ? '继续编辑' : 'Continue editing', exact: true }).click()
    expect(posts).toHaveLength(0)
    await start.click()
    const confirm = page.getByRole('button', { name: zh ? '确认并排队银行出款' : 'Confirm and queue bank payout', exact: true })
    await expect(confirm).toBeInViewport()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/hcai-bank-confirm-${width}-${locale}.png`, animations: 'disabled' })
    await confirm.click()
    const region = page.getByRole('region', { name: zh ? '银行出款' : 'Bank payout', exact: true })
    await expect(region).toContainText(zh ? '银行命令已记录并排队' : 'Bank command recorded and queued')
    await expect(region).toContainText(zh ? '银行到账尚未确认' : 'Bank arrival unconfirmed')
    await expect(start).toHaveCount(0)
    expect(posts).toEqual([bankBody]); expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('unknown bank response retains original command and prevents parent reload until recovery', async ({ page }) => {
  const keys: string[] = []; const bodies: unknown[] = []
  const observed = await mockBank(page, (route, url) => {
    if (url.pathname.endsWith('/bank-payout') && route.request().method() === 'POST') {
      keys.push(route.request().headers()['idempotency-key']); bodies.push(route.request().postDataJSON())
      if (keys.length === 1) return route.abort('failed')
      return route.fulfill({ json: bankSubmission(queuedBank(), true) })
    }
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepareBank(page)
  await page.getByRole('button', { name: 'Confirm and queue bank payout', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Bank payout reason', exact: true })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Reload', exact: true })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Refresh bank status', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Retry original bank command', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Bank payout', exact: true })).toContainText('Original bank command recovered')
  await expect(page.getByRole('button', { name: 'Reload', exact: true })).toBeEnabled()
  expect(keys[1]).toBe(keys[0]); expect(bodies).toEqual([bankBody, bankBody]); expect(observed).toEqual({ unexpected: [], errors: [] })
})

for (const status of [409, 422]) {
  test(`bank ${status} clears submitted fields and synchronizes the current review without resubmitting`, async ({ page }) => {
    let failed = false; let posts = 0
    const observed = await mockBank(page, (route, url) => {
      if (!url.pathname.endsWith('/bank-payout')) return
      if (route.request().method() === 'GET') return route.fulfill({ json: failed ? { request: { ...bankRequest(), status: 'reconciliation_required', latestReview: review('approved', 2) }, canSubmit: false } : bankOperation() })
      posts++; failed = true
      return route.fulfill({ status, json: { error: { code: status === 409 ? 'seller_bank_payout_conflict' : 'invalid_seller_bank_payout', message: 'Current evidence changed' } } })
    })
    await page.goto(`/admin/payouts/${requestId}`); await prepareBank(page)
    await page.getByRole('button', { name: 'Confirm and queue bank payout', exact: true }).click()
    await expect(page.getByRole('region', { name: 'Latest decision', exact: true })).toContainText('Revision 2')
    await expect(page.locator('.payout-review-detail')).toContainText('Needs review')
    await expect(page.getByRole('button', { name: 'Retry original bank command', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Authorize bank payout', exact: true })).toHaveCount(0)
    expect(posts).toBe(1); expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

for (const stage of ['read', 'submit'] as const) for (const code of [401, 403, 404]) {
  test(`bank ${stage} ${code} clears parent financial details and original commands`, async ({ page }) => {
    const observed = await mockBank(page, (route, url) => {
      if (url.pathname.endsWith('/bank-payout') && route.request().method() === (stage === 'read' ? 'GET' : 'POST')) return route.fulfill({ status: code, json: denied })
    })
    await page.goto(`/admin/payouts/${requestId}`)
    if (stage === 'submit') {
      await prepareBank(page); await page.getByRole('button', { name: 'Confirm and queue bank payout', exact: true }).click()
    }
    await expect(page.locator('.payout-review-detail')).toHaveCount(0)
    await expect(page.getByRole('region', { name: 'Source funding', exact: true })).toHaveCount(0)
    await expect(page.getByRole('region', { name: 'Bank payout', exact: true })).toHaveCount(0)
    await expect(page.getByText(item.sellerId, { exact: true })).toHaveCount(0)
    await expect(page.getByRole('alert')).toBeVisible()
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

for (const stage of ['read', 'submit'] as const) for (const change of ['account', 'permission', 'route', 'same-account'] as const) {
  test(`late bank ${stage} cannot overwrite the next ${change} context`, async ({ page }) => {
    let user = actor(); let held: Route | undefined; let first = true
    const nextRequest: Item = { ...item, id: secondId, sellerId: 'next-seller', bankDestinationId: 'ba_next' }
    const observed = await mockBank(page, (route, url) => {
      if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
      if (url.pathname.endsWith(secondId)) return route.fulfill({ json: nextRequest })
      if (!first && url.pathname.endsWith(requestId)) return route.fulfill({ json: { ...nextRequest, id: requestId } })
      if (url.pathname.endsWith('/bank-payout') && route.request().method() === (stage === 'read' ? 'GET' : 'POST') && first) {
        first = false; held = route; return Promise.resolve()
      }
    })
    await page.goto(`/admin/payouts/${requestId}`)
    if (stage === 'submit') { await prepareBank(page); await page.getByRole('button', { name: 'Confirm and queue bank payout', exact: true }).click() }
    await expect.poll(() => !!held).toBe(true)
    if (change === 'account') { user = actor('finance-b'); await refreshSession(page) }
    if (change === 'permission') { user = actor('finance-a', false); await refreshSession(page) }
    if (change === 'same-account') await refreshSession(page)
    if (change === 'route') await navigate(page, `/admin/payouts/${secondId}`)
    const stale = { ...queuedBank(), request: { ...bankRequest(), sellerId: 'stale-private-seller', bankName: 'Stale Private Bank' } }
    await settle(page, held!, stage === 'read' ? stale : bankSubmission(stale))
    await expect(page.getByText('stale-private-seller', { exact: true })).toHaveCount(0)
    await expect(page.getByText('Stale Private Bank', { exact: true })).toHaveCount(0)
    await expect(page.getByRole('region', { name: 'Bank payout', exact: true })).toHaveCount(0)
    if (change !== 'permission') await expect(page.getByText('next-seller', { exact: true })).toBeVisible()
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('bank refresh synchronizes confirmed paid and later return with parent funds status', async ({ page }) => {
  let current = queuedBank()
  const observed = await mockBank(page, (route, url) => {
    if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: current })
  })
  await page.goto(`/admin/payouts/${requestId}`)
  const region = page.getByRole('region', { name: 'Bank payout', exact: true })
  await expect(region).toContainText('Bank arrival unconfirmed')
  current = { ...current, request: { ...bankRequest(), status: 'succeeded' }, bank: { ...current.bank!, jobStatus: 'succeeded', startedAt: '2026-09-23T00:00:01Z', providerStatus: 'paid', checkedAt: '2026-09-23T00:00:02Z' } }
  await page.getByRole('button', { name: 'Refresh bank status', exact: true }).click()
  await expect(region).toContainText('Bank paid confirmed')
  await expect(page.locator('.payout-review-detail')).toContainText('Completed')
  current = { ...current, request: { ...bankRequest(), status: 'reconciliation_required' }, bank: { ...current.bank!, providerStatus: 'failed', requiresReview: true } }
  await page.getByRole('button', { name: 'Refresh bank status', exact: true }).click()
  await expect(region).toContainText('Bank failed or returned')
  await expect(region).toContainText('Bank evidence needs reconciliation')
  await expect(page.locator('.payout-review-detail')).toContainText('Needs review')
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('a stopped unstarted bank command stays read-only after refresh', async ({ page }) => {
  let posts = 0
  const current = { ...queuedBank(), bank: { ...queuedBank().bank!, jobStatus: 'failed' } }
  const observed = await mockBank(page, (route, url) => {
    if (url.pathname.endsWith('/bank-payout')) {
      if (route.request().method() === 'POST') posts++
      return route.fulfill({ json: current })
    }
  })
  await page.goto(`/admin/payouts/${requestId}`)
  await expect(page.getByRole('region', { name: 'Bank payout', exact: true })).toContainText('stopped before its first bank request')
  await page.getByRole('button', { name: 'Refresh bank status', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Authorize bank payout', exact: true })).toHaveCount(0)
  expect(posts).toBe(0); expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('revoking finance access closes bank confirmation without posting', async ({ page }) => {
  let user = actor(); let posts = 0
  const observed = await mockBank(page, (route, url) => {
    if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
    if (url.pathname.endsWith('/bank-payout') && route.request().method() === 'POST') { posts++; return route.fulfill({ status: 403, json: denied }) }
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepareBank(page)
  user = actor('finance-a', false); await refreshSession(page)
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  await expect(page.locator('.payout-review-detail')).toHaveCount(0)
  expect(posts).toBe(0); expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('a late bank permission denial cannot clear the next request', async ({ page }) => {
  let held: Route | undefined
  const observed = await mockBank(page, (route, url) => {
    if (url.pathname.endsWith(secondId)) return route.fulfill({ json: { ...item, id: secondId, sellerId: 'next-seller', bankDestinationId: 'ba_next' } })
    if (url.pathname.endsWith('/bank-payout') && route.request().method() === 'POST') { held = route; return Promise.resolve() }
  })
  await page.goto(`/admin/payouts/${requestId}`); await prepareBank(page)
  await page.getByRole('button', { name: 'Confirm and queue bank payout', exact: true }).click()
  await expect.poll(() => !!held).toBe(true)
  await navigate(page, `/admin/payouts/${secondId}`)
  await expect(page.getByText('next-seller', { exact: true })).toBeVisible()
  await settle(page, held!, denied, 403)
  await expect(page.getByText('next-seller', { exact: true })).toBeVisible()
  await expect(page.getByRole('alert')).toHaveCount(0)
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

test('a maximum-length bank reason leaves mobile confirmation actions reachable', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 700 })
  const observed = await mockBank(page)
  await page.goto(`/admin/payouts/${requestId}`)
  const reason = page.getByRole('textbox', { name: 'Bank payout reason', exact: true })
  const start = page.getByRole('button', { name: 'Authorize bank payout', exact: true })
  await reason.fill('🙂'.repeat(1001)); await expect(start).toBeDisabled()
  await reason.fill('🙂'.repeat(1000)); await start.click()
  await expect(page.getByRole('button', { name: 'Confirm and queue bank payout', exact: true })).toBeInViewport()
  await expect(page.getByRole('button', { name: 'Continue editing', exact: true })).toBeInViewport()
  expect(await page.locator('.ui-alert-dialog__body').evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: '/tmp/hcai-bank-long-confirmation.png', animations: 'disabled' })
  await page.getByRole('button', { name: 'Continue editing', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Reload', exact: true })).toBeEnabled()
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

const sourceTimestamp = '2026-09-23T00:00:00Z'
const sourceCommand = { id: secondId, payoutRequestId: requestId, sourceTransferId: 'source-original', requestUpdatedAt: sourceTimestamp, bankDisposition: 'not_reserved', amountCents: 1852, currency: 'USD', jobId: 'return-job', createdAt: sourceTimestamp }
const sourceOperation = (returned = false): components['schemas']['SellerSourceReversalOperation'] => ({
  request: { ...bankRequest(), status: returned ? 'reconciliation_required' : 'processing' }, expectedUpdatedAt: sourceTimestamp,
  bank: { disposition: 'not_reserved' }, requiresReview: false, canSubmit: !returned, canClose: returned,
  ...(returned ? { command: { ...sourceCommand, bankDisposition: 'not_reserved' as const, currency: 'USD' as const }, acceptedReadId: 'accepted-read', closeResolution: 'released' as const, jobStatus: 'succeeded' as const, startedAt: sourceTimestamp } : {}),
})
const sourceReason = 'Original bank obligation and source funds have been reviewed.'
async function openSource(page: Page, close = false) {
  await page.getByRole('textbox', { name: 'Funds disposition reason', exact: true }).fill(sourceReason)
  await page.getByRole('button', { name: close ? 'Review ledger closure' : 'Authorize source return', exact: true }).click()
  await expect(page.getByRole('alertdialog')).toBeVisible()
}

for (const width of [390, 1308]) {
  test(`source return and separate ledger closure with shared confirmation at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 901 })
    let current = sourceOperation(); const posts: { path: string; body: unknown }[] = []
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/source-reversal')) {
        if (route.request().method() === 'GET') return route.fulfill({ json: current })
        posts.push({ path: url.pathname, body: route.request().postDataJSON() })
        current = sourceOperation(true)
        return route.fulfill({ json: { command: sourceCommand, replayed: false } })
      }
      if (url.pathname.endsWith('/close')) {
        posts.push({ path: url.pathname, body: route.request().postDataJSON() })
        current = { ...current, request: { ...current.request, status: 'cancelled' }, canSubmit: false, canClose: false, closure: { id: 'closure-original', commandId: secondId, payoutRequestId: requestId, readId: 'accepted-read', resolution: 'released', createdAt: sourceTimestamp } }
        return route.fulfill({ json: { closure: current.closure, replayed: false } })
      }
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request: current.request, canSubmit: !current.command } })
      if (url.pathname === `/api/v1/admin/seller-payout-requests/${requestId}`) return route.fulfill({ json: current.request })
    })
    await page.goto(`/admin/payouts/${requestId}`)
    await openSource(page)
    await expect(page.getByRole('alertdialog')).toContainText('source-original')
    await expect(page.getByRole('alertdialog')).toContainText('Bank not reserved')
    expect(posts).toHaveLength(0)
    await page.getByRole('button', { name: 'Continue editing', exact: true }).click()
    expect(posts).toHaveLength(0)
    await openSource(page)
    await page.getByRole('button', { name: 'Confirm and queue source return', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Review ledger closure', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Authorize bank payout', exact: true })).toHaveCount(0)
    expect(posts).toHaveLength(1)
    await openSource(page, true)
    await expect(page.getByRole('alertdialog')).toContainText('accepted-read')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Confirm ledger closure', exact: true }).click()
    await expect(page.getByText('Original reservation released', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Review ledger closure', exact: true })).toHaveCount(0)
    expect(posts).toEqual([
      { path: `/api/v1/admin/seller-payout-requests/${requestId}/source-reversal`, body: { sourceTransferId: 'source-original', expectedUpdatedAt: sourceTimestamp, bankCommandId: null, bankResultId: null, reason: sourceReason, confirmed: true } },
      { path: `/api/v1/admin/seller-source-reversals/${secondId}/close`, body: { readId: 'accepted-read', expectedUpdatedAt: sourceTimestamp, reason: sourceReason, confirmed: true } },
    ])
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/hcai-source-return-${width}.png`, animations: 'disabled' })
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

for (const close of [false, true]) {
  test(`source ${close ? 'closure' : 'return'} unknown response retains original snapshot and key`, async ({ page }) => {
    let posts = 0
    const bodies: unknown[] = [], keys: string[] = []
    const current = sourceOperation(close)
    const observed = await mockApp(page, (route, url) => {
      if ((url.pathname.endsWith('/source-reversal') || url.pathname.endsWith('/close')) && route.request().method() === 'POST') {
        posts++; bodies.push(route.request().postDataJSON()); keys.push(route.request().headers()['idempotency-key'])
        return posts === 1 ? route.abort('failed') : route.fulfill({ json: close ? { closure: { id: 'closure-original' }, replayed: true } : { command: sourceCommand, replayed: true } })
      }
      if (url.pathname.endsWith('/source-reversal')) return route.fulfill({ json: current })
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request: current.request, canSubmit: false } })
      if (url.pathname === `/api/v1/admin/seller-payout-requests/${requestId}`) return route.fulfill({ json: current.request })
    })
    await page.goto(`/admin/payouts/${requestId}`); await openSource(page, close)
    await page.getByRole('alertdialog').getByRole('button', { name: close ? 'Confirm ledger closure' : 'Confirm and queue source return', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Retry original funds operation', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Reload', exact: true })).toBeDisabled()
    await page.getByRole('button', { name: 'Retry original funds operation', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Retry original funds operation', exact: true })).toHaveCount(0)
    expect(posts).toBe(2); expect(bodies[1]).toEqual(bodies[0]); expect(keys[0]).toBeTruthy(); expect(keys[1]).toBe(keys[0])
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('source return accepted command survives failed refresh without offering POST again', async ({ page }) => {
  let posts = 0
  const current = sourceOperation()
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/source-reversal')) {
      if (route.request().method() === 'POST') { posts++; return route.fulfill({ json: { command: sourceCommand, replayed: false } }) }
      return posts ? route.fulfill({ status: 503, json: { error: { code: 'payment_provider_unavailable', message: 'Read temporarily unavailable' } } }) : route.fulfill({ json: current })
    }
    if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request: current.request, canSubmit: false } })
    if (url.pathname === `/api/v1/admin/seller-payout-requests/${requestId}`) return route.fulfill({ json: current.request })
  })
  await page.goto(`/admin/payouts/${requestId}`); await openSource(page)
  await page.getByRole('button', { name: 'Confirm and queue source return', exact: true }).click()
  await expect(page.getByText('Source return command recorded. Refresh to check the verified provider result.', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Retry original funds operation', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Refresh source return', exact: true }).click()
  expect(posts).toBe(1)
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

for (const code of [401, 403, 404]) {
  test(`source return private read ${code} clears the finance detail`, async ({ page }) => {
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/source-reversal')) return route.fulfill({ status: code, json: denied })
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request: bankRequest(), canSubmit: false } })
      if (url.pathname === `/api/v1/admin/seller-payout-requests/${requestId}`) return route.fulfill({ json: bankRequest() })
    })
    await page.goto(`/admin/payouts/${requestId}`)
    await expect(page.getByRole('alert')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Authorize source return', exact: true })).toHaveCount(0)
    await expect(page.getByText('seller-private', { exact: true })).toHaveCount(0)
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

for (const stage of ['read', 'return', 'close']) for (const change of ['account', 'permission', 'route', 'same-account']) {
  test(`late source ${stage} result cannot reappear after ${change} changes`, async ({ page }) => {
    let user = actor(), first = true
    let held: Route | undefined
    const current = sourceOperation(stage === 'close')
    const nextRequest: Item = { ...item, id: secondId, sellerId: 'next-source-seller' }
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
      if (url.pathname === `/api/v1/admin/seller-payout-requests/${secondId}`) return route.fulfill({ json: nextRequest })
      if (url.pathname === `/api/v1/admin/seller-payout-requests/${requestId}`) return route.fulfill({ json: first ? current.request : { ...nextRequest, id: requestId } })
      if ((url.pathname.endsWith('/source-reversal') && route.request().method() === (stage === 'read' ? 'GET' : 'POST') || url.pathname.endsWith('/close') && stage === 'close') && first) {
        first = false; held = route; return Promise.resolve()
      }
      if (url.pathname.endsWith('/source-reversal')) return route.fulfill({ json: current })
      if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request: current.request, canSubmit: false } })
    })
    await page.goto(`/admin/payouts/${requestId}`)
    if (stage !== 'read') {
      await openSource(page, stage === 'close')
      await page.getByRole('alertdialog').getByRole('button', { name: stage === 'close' ? 'Confirm ledger closure' : 'Confirm and queue source return', exact: true }).click()
    }
    await expect.poll(() => !!held).toBe(true)
    if (change === 'account') { user = actor('finance-b'); await refreshSession(page) }
    if (change === 'permission') { user = actor('finance-a', false); await refreshSession(page) }
    if (change === 'same-account') await refreshSession(page)
    if (change === 'route') await navigate(page, `/admin/payouts/${secondId}`)
    await settle(page, held!, stage === 'read' ? { ...current, request: { ...current.request, sellerId: 'stale-source-seller' } } : stage === 'return' ? { command: sourceCommand, replayed: false } : { closure: { id: 'old-private-closure' }, replayed: false })
    await expect(page.getByText('stale-source-seller', { exact: true })).toHaveCount(0)
    await expect(page.getByRole('region', { name: 'Source funds return', exact: true })).toHaveCount(0)
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
    if (change !== 'permission') await expect(page.getByText('next-source-seller', { exact: true })).toBeVisible()
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('source return confirmation closes on revoked finance authority without sending', async ({ page }) => {
  let user = actor(), posts = 0
  const current = sourceOperation()
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user } })
    if (url.pathname.endsWith('/source-reversal')) {
      if (route.request().method() === 'POST') posts++
      return route.fulfill({ json: current })
    }
    if (url.pathname.endsWith('/bank-payout')) return route.fulfill({ json: { request: current.request, canSubmit: false } })
    if (url.pathname === `/api/v1/admin/seller-payout-requests/${requestId}`) return route.fulfill({ json: current.request })
  })
  await page.goto(`/admin/payouts/${requestId}`); await openSource(page)
  user = actor('finance-a', false); await refreshSession(page)
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Source funds return', exact: true })).toHaveCount(0)
  expect(posts).toBe(0); expect(observed).toEqual({ unexpected: [], errors: [] })
})
