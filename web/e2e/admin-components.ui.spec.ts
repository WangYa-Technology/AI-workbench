import { expect, test, type Page, type Route } from '@playwright/test'
import { topupSettingsFixture } from './helpers/topup'
import { chooseOption } from './helpers/select'

const id = '10000000-0000-4000-8000-000000008301'
const otherID = '20000000-0000-4000-8000-000000008302'
const user = (key = 'operator') => ({ id: key, handle: key, email: `${key}@example.test`, displayName: key, role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: ['admin:access', 'admin:content', 'admin:tasks', 'admin:finance', 'admin:data-rights', 'admin:media'] })
const category = { code: 'market_asset', scope: 'marketplace', nameEn: 'Assets', nameZh: '素材', icon: 'mixed', sortOrder: 1, version: 1 }
const content = { id, title: 'Private content candidate', category: category.code }
const evidencePath = '/api/v1/admin/payments/webhook-quarantines'
const evidence = { id, provider: 'stripe', providerEventId: 'evt_private_original', eventType: 'checkout.session.completed', liveMode: false, amountCents: 1900, currency: 'USD', state: 'pending', rejectionCode: 'payment_binding_conflict', receivedAt: '2026-01-01T00:00:00Z', version: 1, hasReviewHold: true }
const gapPath = '/api/v1/admin/product-deliveries/evidence-gaps'
const gap = { orderId: id, title: 'Private delivery evidence', orderStatus: 'fulfilled', environment: 'unknown', gap: 'contract_missing', hasActiveRights: true, hasPendingPayment: false, hasUnsettledFunds: false, hasSnapshot: true, createdAt: '2020-01-01T00:00:00Z' }
const jobs = [
  { endpoint: 'media-cleanups', title: 'Media cleanup recovery', retry: 'Retry media cleanup', kind: 'account' },
  { endpoint: 'export-jobs', title: 'Export recovery', retry: 'Retry export job', kind: 'export' },
  { endpoint: 'deletion-jobs', title: 'Account deletion recovery', retry: 'Retry deletion job', kind: 'prepare' },
] as const
const job = (kind: string) => ({ id, userId: otherID, kind, status: 'failed', attempts: 20, maxAttempts: 20, errorCode: 'handler_failed', canRetry: true, unavailableReason: '', createdAt: '2026-09-19T10:00:00Z', updatedAt: '2026-09-19T10:01:00Z' })
const failure = (status: number) => ({ status, json: { error: { code: status === 503 ? 'unavailable' : 'forbidden', message: 'Fixture failure', retryable: status >= 500 } } })
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
async function settle(page: Page, route: Route, json: unknown, status = 200) {
  const response = page.waitForResponse(r => r.url() === route.request().url() && r.request().method() === route.request().method())
  await route.fulfill({ json, status })
  await (await response).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined) {
  const unexpected: string[] = [], errors: string[] = [], requests: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    requests.push(`${route.request().method()} ${url.pathname}${url.search}`)
    const handled = handler(route, url)
    if (handled) return handled
    if (route.request().method() === 'GET') {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user() } })
      if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
      if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
      if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (url.pathname === '/api/v1/task-types') return route.fulfill({ json: { items: [category] } })
      if (url.pathname === '/api/v1/admin/content-category') return route.fulfill({ json: { items: [content], nextCursor: 'content-next' } })
      if (url.pathname === evidencePath) return route.fulfill({ json: { items: [evidence], nextCursor: 'evidence-next' } })
      if (url.pathname === gapPath) return route.fulfill({ json: { items: [gap], scanned: 1, nextCursor: 'gap-next' } })
      for (const mode of jobs) if (url.pathname === `/api/v1/admin/data-rights/${mode.endpoint}`) return route.fulfill({ json: { items: [job(mode.kind)], nextCursor: 'job-next' } })
      if (url.pathname === '/api/v1/admin/wallet-topup-settings') return route.fulfill({ json: topupSettingsFixture })
      if (['/api/v1/admin/finance/accounts', '/api/v1/admin/payments', '/api/v1/admin/payment-destinations', '/api/v1/admin/subscription-plans', '/api/v1/admin/subscription-models', '/api/v1/admin/payment-providers', '/api/v1/admin/data-rights', '/api/v1/admin/data-rights/holds'].includes(url.pathname)) return route.fulfill({ json: { items: [] } })
    }
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill(failure(500))
  })
  return { unexpected, errors, requests }
}

for (const destination of ['stay', 'leave', 'account'] as const) {
  test(`category creation keeps its follow-up reads in the original context: ${destination}`, async ({ page }) => {
    let current = user()
    let pending: Route | undefined
    const { unexpected, errors, requests } = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: current } })
      if (url.pathname === '/api/v1/admin/task-types') { pending = route; return Promise.resolve() }
    })
    await page.goto('/admin?tab=marketplaceCategories')
    const manager = page.locator('.category-manager')
    const form = manager.locator('.category-edit-row').first()
    await form.getByLabel('Code', { exact: true }).fill('market_custom')
    await form.getByLabel('Chinese name', { exact: true }).fill('自定义')
    await form.getByLabel('English name', { exact: true }).fill('Custom')
    await form.getByRole('button', { name: 'Add', exact: true }).click()
    await expect.poll(() => Boolean(pending)).toBe(true)
    expect(pending!.request().postDataJSON()).toMatchObject({ code: 'market_custom', scope: 'marketplace' })
    if (destination === 'leave') await navigate(page, '/admin?tab=finance')
    if (destination === 'account') { current = user('next-operator'); await refreshSession(page) }
    // All reads from the new context finish before releasing the old command.
    if (destination === 'account') await expect(manager.getByRole('button', { name: 'Add', exact: true })).toBeEnabled()
    if (destination === 'leave') await expect(page.locator('.payment-operations-admin')).toBeVisible()
    const before = requests.filter(value => value.includes('/task-types?') || value.includes('/content-category?')).length
    await settle(page, pending!, { ...category, code: 'market_custom' })
    if (destination === 'stay') {
      await expect(manager.getByRole('status')).toHaveText('Saved')
      expect(requests.filter(value => value.includes('/task-types?') || value.includes('/content-category?')).length).toBe(before + 2)
    } else {
      await expect(page.getByText('Saved', { exact: true })).toHaveCount(0)
      expect(requests.filter(value => value.includes('/task-types?') || value.includes('/content-category?')).length).toBe(before)
    }
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

test('new category content searches clear the previously selected assignment', async ({ page }) => {
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/content-category' && url.searchParams.get('q')) return route.fulfill({ json: { items: [{ ...content, id: otherID, title: 'New search result' }], nextCursor: '' } })
  })
  await page.goto('/admin?tab=marketplaceCategories')
  const form = page.locator('.category-manager .category-delete').last()
  await chooseOption(form.locator('select').nth(0), id)
  await chooseOption(form.locator('select').nth(1), category.code)
  await expect(form.getByRole('button', { name: 'Save', exact: true })).toBeEnabled()
  await form.getByRole('searchbox', { name: 'Search content', exact: true }).fill('new')
  await form.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(form.getByRole('button', { name: 'Search', exact: true })).toBeEnabled()
  await expect(form.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

for (const status of [401, 403, 404]) {
  for (const source of ['category', 'webhook', 'delivery'] as const) {
    test(`${source} continuation HTTP ${status} clears private rows and actions`, async ({ page }) => {
      const path = source === 'category' ? '/api/v1/admin/content-category' : source === 'webhook' ? evidencePath : gapPath
      const { unexpected, errors } = await mockApp(page, (route, url) => {
        if (url.pathname === path && url.searchParams.get('cursor')) return route.fulfill(failure(status))
      })
      await page.goto(source === 'category' ? '/admin?tab=marketplaceCategories' : source === 'webhook' ? '/admin?tab=finance' : '/admin/deliveries')
      const section = page.locator(source === 'category' ? '.category-manager' : source === 'webhook' ? '.webhook-evidence' : '.delivery-evidence-inventory')
      const more = source === 'category' ? 'Load more' : source === 'webhook' ? 'Load more evidence' : 'Continue review'
      await section.getByRole('button', { name: more, exact: true }).click()
      await expect(section.getByRole('alert')).toBeVisible()
      if (source === 'category') {
        await expect(section.locator('select option').filter({ hasText: 'Private content candidate' })).toHaveCount(0)
        await expect(section.getByRole('button', { name: 'Add', exact: true })).toBeDisabled()
      } else {
        await expect(section.getByText(source === 'webhook' ? evidence.providerEventId : gap.title, { exact: true })).toHaveCount(0)
        await expect(section.getByRole(source === 'webhook' ? 'button' : 'link', { name: source === 'webhook' ? 'Review and recheck' : 'Inspect delivery', exact: true })).toHaveCount(0)
      }
      expect(unexpected).toEqual([])
      expect(errors).toEqual([])
    })
  }
  for (const mode of jobs) {
    test(`${mode.endpoint} rejected retry HTTP ${status} clears the job and its confirmation`, async ({ page }) => {
      const { unexpected, errors } = await mockApp(page, (route, url) => {
        if (url.pathname === `/api/v1/admin/data-rights/${mode.endpoint}/${id}/retry`) return route.fulfill(failure(status))
      })
      await page.goto('/admin?tab=dataRights')
      const queue = page.getByRole('region', { name: mode.title, exact: true })
      await queue.getByRole('button', { name: mode.retry, exact: true }).click()
      const drawer = page.getByRole('dialog', { name: mode.retry, exact: true })
      await drawer.getByLabel('Decision reason', { exact: true }).fill('Original failure reviewed before recovery.')
      await drawer.getByRole('checkbox').check()
      await drawer.getByRole('button', { name: mode.retry, exact: true }).click()
      await expect(drawer).toHaveCount(0)
      await expect(queue.getByRole('alert')).toBeVisible()
      await expect(queue.locator('.cleanup-job-list article')).toHaveCount(0)
      expect(unexpected).toEqual([])
      expect(errors).toEqual([])
    })
  }
}

for (const mode of jobs) {
  test(`${mode.endpoint} accepted retries remain confirmed after a temporary refresh failure`, async ({ page }) => {
    let accepted = false, writes = 0
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      const base = `/api/v1/admin/data-rights/${mode.endpoint}`
      if (url.pathname === `${base}/${id}/retry`) {
        expect(route.request().postDataJSON()).toEqual({ expectedAttempts: 20, reason: 'Original failure reviewed before recovery.', confirmed: true })
        accepted = true; writes++
        return route.fulfill({ status: 201, json: { ...job(mode.kind), id: otherID, status: 'queued', attempts: 0, retryOf: id, canRetry: false } })
      }
      if (url.pathname === base && accepted) return route.fulfill(failure(503))
    })
    await page.goto('/admin?tab=dataRights')
    const queue = page.getByRole('region', { name: mode.title, exact: true })
    await queue.getByRole('button', { name: mode.retry, exact: true }).click()
    const drawer = page.getByRole('dialog', { name: mode.retry, exact: true })
    await drawer.getByLabel('Decision reason', { exact: true }).fill('Original failure reviewed before recovery.')
    await drawer.getByRole('checkbox').check()
    await drawer.getByRole('button', { name: mode.retry, exact: true }).click()
    await expect(drawer).toHaveCount(0)
    await expect(queue.getByRole('status')).toContainText(/queued/i)
    await expect(queue.getByRole('alert')).toBeVisible()
    await expect(queue.getByText(`Replacement job · ${otherID}`, { exact: true })).toBeVisible()
    await expect(queue.getByRole('button', { name: mode.retry, exact: true })).toHaveCount(0)
    expect(writes).toBe(1)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

test('an accepted webhook recheck reports its result even if evidence refresh fails', async ({ page }) => {
  let accepted = false, writes = 0, paymentReads = 0
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === `${evidencePath}/${id}/recheck`) {
      expect(route.request().postDataJSON()).toEqual({ expectedVersion: 1, reason: 'Original provider evidence was reviewed.' })
      accepted = true; writes++
      return route.fulfill({ json: { ...evidence, state: 'admitted', version: 2 } })
    }
    if (url.pathname === evidencePath && accepted) return route.fulfill(failure(503))
    if (url.pathname === '/api/v1/admin/payments') { paymentReads++; return route.fulfill({ json: { items: [] } }) }
  })
  await page.goto('/admin?tab=finance')
  const section = page.locator('.webhook-evidence')
  await section.getByRole('button', { name: 'Review and recheck', exact: true }).click()
  await section.getByRole('textbox', { name: 'Review reason' }).fill('Original provider evidence was reviewed.')
  await section.getByRole('button', { name: 'Recheck original evidence', exact: true }).click()
  await expect(section.getByRole('alert')).toBeVisible()
  await expect(section.getByRole('status')).toContainText('entered normal event processing')
  await expect(section.getByRole('button', { name: 'Refresh evidence', exact: true })).toBeEnabled()
  await expect(section.getByRole('button', { name: 'Recheck original evidence', exact: true })).toHaveCount(0)
  await expect.poll(() => paymentReads).toBe(2)
  expect(writes).toBe(1)
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

for (const status of [401, 403, 404]) {
  test(`webhook recheck HTTP ${status} clears the reason and the selected evidence`, async ({ page }) => {
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === `${evidencePath}/${id}/recheck`) return route.fulfill(failure(status))
    })
    await page.goto('/admin?tab=finance')
    const section = page.locator('.webhook-evidence')
    await section.getByRole('button', { name: 'Review and recheck', exact: true }).click()
    await section.getByRole('textbox', { name: 'Review reason' }).fill('Original provider evidence was reviewed.')
    await section.getByRole('button', { name: 'Recheck original evidence', exact: true }).click()
    await expect(section.getByRole('alert')).toBeVisible()
    await expect(section.getByRole('textbox', { name: 'Review reason' })).toHaveCount(0)
    await expect(section.getByText(evidence.providerEventId, { exact: true })).toHaveCount(0)
    await section.getByRole('button', { name: 'Refresh evidence', exact: true }).click()
    await expect(section.getByText(evidence.providerEventId, { exact: true })).toBeVisible()
    await section.getByRole('button', { name: 'Review and recheck', exact: true }).click()
    await expect(section.getByRole('textbox', { name: 'Review reason' })).toHaveValue('')
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}

test('category access can be restored by an explicit read without replaying a mutation', async ({ page }) => {
  let denied = true
  const { unexpected, errors, requests } = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/admin/content-category' && denied) return route.fulfill(failure(403))
  })
  await page.goto('/admin?tab=marketplaceCategories')
  const manager = page.locator('.category-manager')
  await expect(manager.getByRole('alert')).toBeVisible()
  await expect(manager.getByRole('button', { name: 'Add', exact: true })).toBeDisabled()
  denied = false
  await manager.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(manager.getByRole('button', { name: 'Add', exact: true })).toBeEnabled()
  expect(requests.every(value => value.startsWith('GET '))).toBe(true)
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

test('assigning a category submits the selected content snapshot before clearing the form', async ({ page }) => {
  let writes = 0
  const { unexpected, errors } = await mockApp(page, (route, url) => {
    if (url.pathname === `/api/v1/admin/content-category/${id}`) {
      expect(route.request().method()).toBe('PATCH')
      expect(url.searchParams.get('scope')).toBe('marketplace')
      expect(route.request().postDataJSON()).toEqual({ category: category.code })
      writes++
      return route.fulfill({ json: { updated: true } })
    }
  })
  await page.goto('/admin?tab=marketplaceCategories')
  const form = page.locator('.category-manager .category-delete').last()
  await chooseOption(form.locator('select').nth(0), id)
  await chooseOption(form.locator('select').nth(1), category.code)
  await form.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.locator('.category-manager').getByRole('status')).toHaveText('Saved')
  await expect(form.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
  expect(writes).toBe(1)
  expect(unexpected).toEqual([])
  expect(errors).toEqual([])
})

for (const mode of jobs) {
  test(`${mode.endpoint} denied continuation clears the old jobs and cursor`, async ({ page }) => {
    const { unexpected, errors } = await mockApp(page, (route, url) => {
      if (url.pathname === `/api/v1/admin/data-rights/${mode.endpoint}` && url.searchParams.get('cursor')) return route.fulfill(failure(403))
    })
    await page.goto('/admin?tab=dataRights')
    const queue = page.getByRole('region', { name: mode.title, exact: true })
    await queue.getByRole('button', { name: 'Load more', exact: true }).click()
    await expect(queue.getByRole('alert')).toBeVisible()
    await expect(queue.locator('.cleanup-job-list article')).toHaveCount(0)
    await expect(queue.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)
    expect(unexpected).toEqual([])
    expect(errors).toEqual([])
  })
}
