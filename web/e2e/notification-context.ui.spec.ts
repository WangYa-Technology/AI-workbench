import { expect, test, type Page, type Route } from '@playwright/test'

const user = (id = 'seller-a') => ({ id, handle: id, email: `${id}@example.test`, displayName: id, role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: [] })
const preference = { kind: 'marketplace.payout_reviewed', inAppEnabled: true, version: 1, updatedAt: '2026-09-22T08:00:00Z' }
const delivery = { id: 'private-delivery-a', kind: preference.kind, status: 'delivered', attempts: 1, createdAt: preference.updatedAt }
type App = HTMLElement & { __vue_app__: { config: { globalProperties: { $pinia: { _s: Map<string, { ensure: (force: boolean) => Promise<unknown> }> }; $router: { push: (path: string) => Promise<unknown> } } } } }
async function session(page: Page) { await page.evaluate(async () => { await (document.querySelector('#app') as App).__vue_app__.config.globalProperties.$pinia._s.get('session')!.ensure(true) }) }
async function settle(page: Page, route: Route, json: unknown, status = 200) {
  const response = page.waitForResponse(r => r.url() === route.request().url() && r.request().method() === route.request().method())
  await route.fulfill({ json, status }); await (await response).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined) {
  const unexpected: string[] = []; const errors: string[] = []
  page.on('pageerror', cause => errors.push(cause.message))
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url()); const handled = handler(route, url)
    if (handled) return handled
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user() } })
    if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
    if (url.pathname === '/api/v1/meta') return route.fulfill({ json: {} })
    if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (url.pathname === '/api/v1/notification-preferences') return route.fulfill({ json: { items: [preference] } })
    if (url.pathname === '/api/v1/notification-deliveries') return route.fulfill({ json: { items: [delivery] } })
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 403, json: { error: { code: 'forbidden' } } })
  })
  return { unexpected, errors }
}

test('late preference reads cannot restore another seller’s settings or delivery evidence', async ({ page }) => {
  let current = user(); const held: Route[] = []
  const observed = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user: current } })
    if (['/api/v1/notification-preferences', '/api/v1/notification-deliveries'].includes(url.pathname)) {
      if (current.id === 'seller-a') { held.push(route); return Promise.resolve() }
      return route.fulfill({ json: { items: [] } })
    }
  })
  await page.goto('/notifications?view=preferences'); await expect.poll(() => held.length).toBe(2)
  current = user('seller-b'); await session(page)
  for (const route of held) await settle(page, route, { items: route.request().url().includes('notification-preferences') ? [preference] : [delivery] })
  await expect(page.getByRole('switch', { name: 'Payout review updated', exact: true })).toHaveCount(0)
  await expect(page.locator('[data-delivery-id="private-delivery-a"]')).toHaveCount(0)
  expect(observed).toEqual({ unexpected: [], errors: [] })
})

for (const status of [200, 403, 409]) {
  test(`old preference save HTTP ${status} cannot update a new seller or trigger reloads`, async ({ page }) => {
    let current = user(); let held: Route | undefined; let newReads = 0
    const observed = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/auth/session')) return route.fulfill({ json: { user: current } })
      if (route.request().method() === 'PUT') { held = route; return Promise.resolve() }
      if (current.id === 'seller-b' && ['/api/v1/notification-preferences', '/api/v1/notification-deliveries'].includes(url.pathname)) { newReads++; return route.fulfill({ json: { items: [] } }) }
    })
    await page.goto('/notifications?view=preferences')
    await page.getByRole('button', { name: /Purchases and billing/ }).click()
    await page.getByRole('switch', { name: 'Payout review updated', exact: true }).click()
    await expect.poll(() => !!held).toBe(true)
    current = user('seller-b'); await session(page); await expect.poll(() => newReads).toBe(2)
    await settle(page, held!, status === 200 ? { ...preference, inAppEnabled: false, version: 2 } : { error: { code: 'forbidden', message: 'Old request failed', retryable: false } }, status)
    expect(newReads).toBe(2)
    await expect(page.getByRole('switch', { name: 'Payout review updated', exact: true })).toHaveCount(0)
    await expect(page.getByText('Preferences saved', { exact: true })).toHaveCount(0)
    expect(observed).toEqual({ unexpected: [], errors: [] })
  })
}

test('current preference denial clears all private panels and permits an explicit read retry', async ({ page }) => {
  let deny = true
  const observed = await mockApp(page, (route, url) => {
    if (route.request().method() === 'PUT') return route.fulfill({ status: 403, json: { error: { code: 'forbidden', message: 'No access', retryable: false } } })
    if (url.pathname.endsWith('/notification-preferences') && !deny) return route.fulfill({ json: { items: [] } })
  })
  await page.goto('/notifications?view=preferences')
  await page.getByRole('button', { name: /Purchases and billing/ }).click()
  await page.getByRole('switch', { name: 'Payout review updated', exact: true }).click()
  await expect(page.locator('[data-delivery-id="private-delivery-a"]')).toHaveCount(0)
  await expect(page.getByRole('switch', { name: 'Payout review updated', exact: true })).toHaveCount(0)
  deny = false
  await page.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(page.locator('[data-delivery-id="private-delivery-a"]')).toBeVisible()
  expect(observed).toEqual({ unexpected: [], errors: [] })
})
