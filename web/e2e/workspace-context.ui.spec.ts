import { expect, test, type Page, type Route } from '@playwright/test'
import { chooseOption } from './helpers/select'
import { topupSettingsFixture } from './helpers/topup'

const firstID = '00000000-0000-4000-8000-000000008901'
const secondID = '00000000-0000-4000-8000-000000008902'
const stamp = '2026-09-21T10:00:00Z'
const user = (id: string) => ({ id, email: 'fixture@example.test', handle: id, displayName: id, role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: [] })
const order = (id: string, title: string) => ({ id, productId: id, productTitle: title, status: 'fulfilled', amountCents: 500, currency: 'USD', licenseName: 'License', licenseVersion: '1', licenseTerms: 'Terms', licenseCode: 'hcai-personal-v1', refundWindowDays: 7, canRequestRefund: true, paymentMode: 'stripe', realCharge: false, createdAt: stamp, events: [] })
const generation = (id: string, prompt: string) => ({ id, prompt, mode: 'image', modelName: 'Fixture model', provider: 'fixture', status: 'failed', estimatedPoints: 1, chargedPoints: 0, progress: 0, createdAt: stamp, isFavorite: false, actions: { canRetry: true, canCancel: true, canView: false, canDownload: false, canReuse: false } })
const statement = (description: string, nextCursor?: string) => ({ account: { availableCents: 1500, heldCents: 0, currency: 'USD' }, entries: [{ id: description, operationId: description, description, entryType: 'product_purchase', direction: 'debit', amountCents: 500, balanceAfterCents: 1500, currency: 'USD', metadata: {}, createdAt: stamp }], nextCursor })
const points = { account: { balancePoints: 100, lifetimeSpentPoints: 0, heldPoints: 0 }, entries: [], plans: [{ id: firstID, name: 'Fixture plan', description: 'Plan', modelIds: [], includedPoints: 100, priceCents: 500, currency: 'USD', billingPeriodDays: 30 }] }

// Use the mounted application's real router and session action; no test-only
// product hooks or direct writes to Pinia state are required.
type MountedApp = HTMLElement & { __vue_app__: { config: { globalProperties: {
  $router: { push: (path: string) => Promise<unknown> }
  $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> }
} } } }
async function navigate(page: Page, path: string) {
  await page.evaluate(async path => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$router.push(path) }, path)
}
async function refreshSession(page: Page) {
  await page.evaluate(async () => { await (document.querySelector('#app') as MountedApp).__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true) })
}
async function settle(page: Page, route: Route, json: unknown) {
  const response = page.waitForResponse(r => r.url() === route.request().url())
  await route.fulfill({ json })
  await (await response).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined) {
  const unexpected: string[] = []
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    const handled = handler(route, url)
    if (handled) return handled
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(firstID) } })
    if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
    if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
    if (url.pathname === '/api/v1/billing/points') return route.fulfill({ json: points })
    if (url.pathname === '/api/v1/billing/topup-settings') return route.fulfill({ json: topupSettingsFixture })
    if (url.pathname === '/api/v1/billing/statement') return route.fulfill({ json: statement('Current statement') })
    if (url.pathname === '/api/v1/orders' || url.pathname === '/api/v1/assets' || url.pathname === '/api/v1/assets/saved-works') return route.fulfill({ json: { items: [], total: 0 } })
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 500, json: { error: { code: 'unexpected_response', message: 'Unmocked API', retryable: false } } })
  })
  return unexpected
}

test('same-route account changes clear private orders and refund drafts before accepting new data', async ({ page }) => {
  let actor = firstID
  let oldPage: Route | undefined
  let currentPage: Route | undefined
  let returning = false
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(actor) } })
    if (url.pathname === '/api/v1/orders') {
      if (url.searchParams.has('cursor')) { oldPage = route; return Promise.resolve() }
      if (actor === secondID) { currentPage = route; return Promise.resolve() }
      return route.fulfill({ json: { items: [order(firstID, 'First private order')], nextCursor: returning ? undefined : 'first-orders' } })
    }
  })
  await page.goto('/workspace/orders')
  await page.locator('.order-row textarea').fill('Private refund draft for the first account')
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect.poll(() => Boolean(oldPage)).toBe(true)
  actor = secondID
  await refreshSession(page)
  await expect(page.locator('.order-row')).toHaveCount(0)
  await expect.poll(() => Boolean(currentPage)).toBe(true)
  await settle(page, currentPage!, { items: [order(secondID, 'Second private order')] })
  await settle(page, oldPage!, { items: [order(firstID, 'Stale first order')] })
  await expect(page.locator('.order-row')).toHaveCount(1)
  await expect(page.locator('.order-row')).toContainText('Second private order')
  returning = true
  actor = firstID
  await refreshSession(page)
  await expect(page.locator('.order-row')).toContainText('First private order')
  await expect(page.locator('.order-row textarea')).toHaveValue('')
  expect(unexpected).toEqual([])
})

test('a private asset response cannot survive a same-route account switch', async ({ page }) => {
  let actor = firstID
  let oldAsset: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(actor) } })
    if (url.pathname === `/api/v1/assets/${firstID}`) {
      if (actor === firstID) { oldAsset = route; return Promise.resolve() }
      return route.fulfill({ status: 403, json: { error: { code: 'forbidden', message: 'Not this account', retryable: false } } })
    }
  })
  await page.goto(`/workspace/assets/${firstID}`)
  await expect.poll(() => Boolean(oldAsset)).toBe(true)
  actor = secondID
  await refreshSession(page)
  await settle(page, oldAsset!, { id: firstID, title: 'Private source of first account', kind: 'image', sourceType: 'upload', mediaUrl: '/brand/logo.png', mimeType: 'image/png', scanStatus: 'clean', createdAt: stamp, versions: [], usages: [] })
  await expect(page.locator('main [role="alert"]')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Private source of first account', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

for (const kind of ['billing', 'generations'] as const) {
  test(`${kind} pagination cannot overwrite a changed filter or clear its new pending request`, async ({ page }) => {
    const pending: Route[] = []
    const endpoint = kind === 'billing' ? '/api/v1/billing/statement' : '/api/v1/generations'
    const filter = kind === 'billing' ? 'direction' : 'mode'
    const result = (name: string, cursor?: string) => kind === 'billing' ? statement(name, cursor) : { items: [generation(name, name)], nextCursor: cursor }
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === endpoint) {
        if (url.searchParams.has('cursor')) { pending.push(route); return Promise.resolve() }
        return route.fulfill({ json: result(url.searchParams.has(filter) ? 'Filtered record' : 'Original record', 'next') })
      }
    })
    await page.goto(`/workspace/${kind}`)
    const more = page.locator(kind === 'billing' ? '.billing-load-more' : '.generation-load-more')
    await more.click()
    await expect.poll(() => pending.length).toBe(1)
    const filters = page.locator(kind === 'billing' ? '.billing-filters' : '.generation-filters')
    await chooseOption(filters.getByRole('combobox', { name: kind === 'billing' ? 'Direction' : 'Mode', exact: true }), kind === 'billing' ? 'debit' : 'image')
    await filters.getByRole('button', { name: 'Apply filters', exact: true }).click()
    await expect(page.getByText('Filtered record', { exact: true })).toBeVisible()
    await expect(more).toBeEnabled()
    await more.click()
    await expect.poll(() => pending.length).toBe(2)
    await settle(page, pending[0]!, result('Stale record'))
    await expect(page.getByText('Stale record', { exact: true })).toHaveCount(0)
    await expect(more).toBeDisabled()
    await settle(page, pending[1]!, result('Current continuation'))
    await expect(page.getByText('Current continuation', { exact: true })).toBeVisible()
    await expect(more).toHaveCount(0)
    expect(unexpected).toEqual([])
  })
}

test('a delayed focused generation cannot enter a different generation query', async ({ page }) => {
  let focused: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/generations') return route.fulfill({ json: { items: [generation(secondID, 'Current list')] } })
    if (url.pathname === `/api/v1/generations/${firstID}`) { focused = route; return Promise.resolve() }
  })
  await page.goto(`/workspace/generations?generationId=${firstID}`)
  await expect.poll(() => Boolean(focused)).toBe(true)
  await navigate(page, '/workspace/generations?mode=image')
  await expect(page.getByText('Current list', { exact: true })).toBeVisible()
  await settle(page, focused!, generation(firstID, 'Old focused private result'))
  await expect(page.locator('.generation-row')).toHaveCount(1)
  await expect(page.getByText('Old focused private result', { exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('a payment poll cannot replace a freshly revisited billing statement', async ({ page }) => {
  let poll: Route | undefined
  let reads = 0
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/billing/statement') {
      reads++
      if (reads === 2) { poll = route; return Promise.resolve() }
      return route.fulfill({ json: statement(reads === 1 ? 'Original statement' : 'Fresh statement') })
    }
  })
  await page.goto(`/workspace/billing?payment=success&paymentId=${firstID}`)
  await expect.poll(() => Boolean(poll)).toBe(true)
  await navigate(page, '/workspace/orders')
  await navigate(page, '/workspace/billing')
  await expect(page.getByText('Fresh statement', { exact: true })).toBeVisible()
  await settle(page, poll!, statement('Stale statement'))
  await expect(page.getByText('Fresh statement', { exact: true })).toBeVisible()
  await expect(page.getByText('Stale statement', { exact: true })).toHaveCount(0)
  expect(reads).toBe(3)
  expect(unexpected).toEqual([])
})

for (const action of ['favorite', 'batch', 'retry', 'cancel'] as const) {
 for (const leave of [false, true]) {
  test(`generation ${action} respects its original view: leave=${leave}`, async ({ page }) => {
    let command: Route | undefined
    let reads = 0
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/generations' && route.request().method() === 'GET') { reads++; return route.fulfill({ json: { items: [generation(firstID, 'Source generation')] } }) }
      if (url.pathname.startsWith('/api/v1/generations') && route.request().method() !== 'GET') { command = route; return Promise.resolve() }
    })
    await page.goto('/workspace/generations')
    if (action === 'batch') {
      await page.locator('.generation-row').getByRole('checkbox').check()
      await page.getByRole('button', { name: 'Favorite selected', exact: true }).click()
    } else await page.locator('.generation-row').getByRole('button', { name: action === 'favorite' ? 'Favorite generation' : action === 'retry' ? 'Try again' : 'Cancel', exact: true }).click()
    await expect.poll(() => Boolean(command)).toBe(true)
    if (leave) {
      await page.locator('#primary-navigation a[href="/workspace/assets"]').click()
      await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
    }
    const updated = { ...generation(firstID, 'Source generation'), isFavorite: true }
    await settle(page, command!, action === 'batch' ? { items: [updated], failures: [] } : updated)
    if (leave) await expect(page.locator('main .task-feedback.success')).toHaveCount(0)
    else {
      await expect(page.locator('main .task-feedback.success')).toBeVisible()
      if (action === 'favorite' || action === 'batch') await expect(page.locator('.generation-row').getByRole('button', { name: 'Remove generation favorite', exact: true })).toBeVisible()
    }
    expect(reads).toBe(!leave && (action === 'retry' || action === 'cancel') ? 2 : 1)
    expect(unexpected).toEqual([])
  })
 }
}

for (const purpose of ['topups', 'subscriptions']) {
 for (const leave of [false, true]) {
  test(`${purpose} checkout uses only its original billing context: leave=${leave}`, async ({ page }) => {
    let checkout: Route | undefined
    await page.addInitScript(() => {
      const popup = { opener: null, location: { href: 'about:blank' }, closed: false, close() { this.closed = true } }
      Object.assign(window, { fixtureCheckout: popup })
      window.open = () => popup as unknown as Window
    })
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === `/api/v1/billing/${purpose}/checkout`) { checkout = route; return Promise.resolve() }
    })
    await page.goto('/workspace/billing')
    await page.locator(purpose === 'topups' ? '.billing-topup-form button[type="submit"]' : '.billing-plan-list button').click()
    await expect.poll(() => Boolean(checkout)).toBe(true)
    if (leave) {
      await page.locator('#primary-navigation a[href="/workspace/orders"]').click()
      await expect(page.locator('.orders-workspace')).toBeVisible()
    }
    await settle(page, checkout!, { checkoutUrl: 'https://checkout.fixture.test/never-open', paymentId: firstID })
    expect(await page.evaluate(() => (window as unknown as { fixtureCheckout: { closed: boolean; location: { href: string } } }).fixtureCheckout)).toMatchObject({ closed: leave, location: { href: leave ? 'about:blank' : 'https://checkout.fixture.test/never-open' } })
    if (leave) await expect(page.locator('main .task-feedback.success')).toHaveCount(0)
    else await expect(page.locator('main .task-feedback.success')).toBeVisible()
    expect(unexpected).toEqual([])
  })
 }
}

test('an old first generation page cannot overwrite a new query', async ({ page }) => {
  let oldPage: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/generations') {
      if (!url.searchParams.has('mode')) { oldPage = route; return Promise.resolve() }
      return route.fulfill({ json: { items: [generation(secondID, 'Current filtered generation')] } })
    }
  })
  await page.goto('/workspace/generations')
  await expect.poll(() => Boolean(oldPage)).toBe(true)
  await navigate(page, '/workspace/generations?mode=image')
  await expect(page.getByText('Current filtered generation', { exact: true })).toBeVisible()
  await settle(page, oldPage!, { items: [generation(firstID, 'Old initial generation')] })
  await expect(page.getByText('Current filtered generation', { exact: true })).toBeVisible()
  await expect(page.getByText('Old initial generation', { exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('task pagination distinguishes two visits to the same task view', async ({ page }) => {
  const pending: Route[] = []
  const task = (id: string, title: string) => ({ id, title, summary: title, status: 'open', deliverableType: 'image', client: { id: firstID, handle: 'owner' }, budgetCents: 100, currency: 'USD', clientTimezone: 'UTC', deadline: stamp, proposalCount: 0 })
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/tasks') {
      if (url.searchParams.has('cursor')) { pending.push(route); return Promise.resolve() }
      return route.fulfill({ json: { items: [task(firstID, 'Current task')], total: 2, nextCursor: 'tasks-next' } })
    }
  })
  await page.goto('/workspace/tasks')
  const more = page.locator('.workspace-task-list').getByRole('button', { name: 'Load more', exact: true })
  await more.click()
  await expect.poll(() => pending.length).toBe(1)
  await navigate(page, '/workspace/orders')
  await navigate(page, '/workspace/tasks')
  await expect(more).toBeEnabled()
  await more.click()
  await expect.poll(() => pending.length).toBe(2)
  await settle(page, pending[0]!, { items: [task(secondID, 'Stale task')], total: 2 })
  await expect(page.getByRole('heading', { name: 'Stale task', exact: true })).toHaveCount(0)
  await expect(more).toBeDisabled()
  await settle(page, pending[1]!, { items: [task(secondID, 'Next current task')], total: 2 })
  await expect(page.getByRole('heading', { name: 'Next current task', exact: true })).toBeVisible()
  expect(unexpected).toEqual([])
})

test('removing a saved reference cannot put its old feedback on the new view', async ({ page }) => {
  let removal: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/assets/saved-works') return route.fulfill({ json: { items: [{ postId: firstID, workId: firstID, title: 'Saved source', mediaKind: 'image', mediaUrl: '/brand/logo.png', savedAt: stamp, authorHandle: 'creator', licenseCode: 'hcai-personal-v1' }] } })
    if (url.pathname === `/api/v1/community/posts/${firstID}/reactions/bookmark`) { removal = route; return Promise.resolve() }
  })
  await page.goto('/workspace/assets?view=saved')
  await page.getByRole('button', { name: 'Remove saved work', exact: true }).click()
  await expect.poll(() => Boolean(removal)).toBe(true)
  await page.getByRole('tab', { name: 'Owned Assets', exact: true }).click()
  await expect(page.locator('.asset-results')).toHaveAttribute('aria-busy', 'false')
  await settle(page, removal!, {})
  await expect(page.locator('main .task-feedback.success')).toHaveCount(0)
  expect(unexpected).toEqual([])
})
