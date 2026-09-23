import { expect, test, type Page, type Route } from '@playwright/test'
import { chooseOption } from './helpers/select'

const firstID = '00000000-0000-4000-8000-000000007901'
const secondID = '00000000-0000-4000-8000-000000007902'
const sellerID = '00000000-0000-4000-8000-000000007903'
const license = { code: 'hcai-personal-v1', name: 'Fixture license', version: '1', summary: 'Fixture terms', terms: 'Fixture terms', refundWindowDays: 7, allowsCommercial: false, allowsDerivatives: true, allowsRedistribution: false, attributionRequired: true }
const user = (id: string, review = false) => ({ id, handle: id, email: 'fixture@example.test', displayName: id, role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', permissions: review ? ['admin:content'] : [] })
const product = (id = firstID, extra = {}) => ({ id, title: `Product ${id}`, description: 'Public description', category: 'market_asset', productType: 'asset', priceCents: 1900, currency: 'USD', offerVersion: `offer-${id}`, mediaUrl: '', mediaKind: 'image', includedFiles: ['Original'], compatibility: 'PNG', aiDisclosure: 'Original', seller: { id: sellerID, handle: 'seller', displayName: 'Seller' }, license, ...extra })
const sellerProduct = (extra = {}) => ({ id: firstID, sellerId: sellerID, title: 'Private review title', description: 'Private review description', version: 1, status: 'draft', reviewStatus: 'pending', productType: 'asset', category: 'market_asset', assetId: firstID, priceCents: 1900, currency: 'USD', licenseCode: license.code, aiDisclosure: 'Original', includedFiles: ['original.png'], compatibility: 'PNG', ...extra })
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
  const response = page.waitForResponse(r => r.url() === route.request().url())
  await route.fulfill({ json, status })
  await (await response).finished()
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
}
const denied = (code = 'forbidden') => ({ error: { code, message: 'Fixture failure', retryable: false } })
async function mockApp(page: Page, handler: (route: Route, url: URL) => Promise<void> | undefined) {
  const unexpected: string[] = []
  await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url())
    const handled = handler(route, url)
    if (handled) return handled
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(firstID) } })
    if (url.pathname === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
    if (url.pathname === '/api/v1/meta') return route.fulfill({ json: { paymentProvider: { enabled: true, provider: 'stripe', liveMode: false } } })
    if (url.pathname === '/api/v1/task-types') return route.fulfill({ json: { items: [{ code: 'market_asset', nameEn: 'Assets', nameZh: '素材' }] } })
    if (url.pathname === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
    if (url.pathname === '/api/v1/orders' || url.pathname === '/api/v1/assets') return route.fulfill({ json: { items: [], total: 0 } })
    if (url.pathname === '/api/v1/seller/licenses') return route.fulfill({ json: { items: [license] } })
    if (url.pathname === '/api/v1/products') return route.fulfill({ json: { items: [], total: 0, categoryCounts: {} } })
    if (url.pathname === `/api/v1/products/${firstID}` || url.pathname === `/api/v1/products/${secondID}`) return route.fulfill({ json: product(url.pathname.split('/').at(-1)!) })
    unexpected.push(`${route.request().method()} ${url.pathname}`)
    return route.fulfill({ status: 500, json: denied('unexpected_response') })
  })
  return unexpected
}

for (const initial of ['owned', 'accepted']) {
  test(`same-route account change clears ${initial} purchase context`, async ({ page }) => {
    let actor = firstID
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(actor) } })
      if (url.pathname === `/api/v1/products/${firstID}`) return route.fulfill({ json: product(firstID, actor === firstID && initial === 'owned' ? { ownedAssetId: firstID } : {}) })
    })
    await page.goto(`/market/assets/${firstID}`)
    if (initial === 'owned') await expect(page.locator(`.product-purchase-rail a[href="/workspace/assets/${firstID}"]`)).toBeVisible()
    else await page.locator('.license-accept input').check()
    actor = secondID
    await refreshSession(page)
    await expect(page.locator('.license-accept input')).toBeVisible()
    await expect(page.locator('.license-accept input')).not.toBeChecked()
    await expect(page.locator('.product-purchase-rail button[type="submit"]')).toBeDisabled()
    await expect(page.locator(`.product-purchase-rail a[href="/workspace/assets/${firstID}"]`)).toHaveCount(0)
    expect(unexpected).toEqual([])
  })
}

test('a late product detail cannot bring back a preceding account entitlement', async ({ page }) => {
  let actor = firstID
  let old: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(actor) } })
    if (url.pathname === `/api/v1/products/${firstID}`) {
      if (actor === firstID) { old = route; return Promise.resolve() }
      return route.fulfill({ status: 404, json: denied('product_not_found') })
    }
  })
  await page.goto(`/market/assets/${firstID}`)
  await expect.poll(() => Boolean(old)).toBe(true)
  actor = secondID
  await refreshSession(page)
  await settle(page, old!, product(firstID, { title: 'Stale entitlement', ownedAssetId: firstID }))
  await expect(page.locator('main [role="alert"]')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Stale entitlement', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('account changes invalidate old catalog pages and cursors', async ({ page }) => {
  let actor = firstID
  let old: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(actor) } })
    if (url.pathname === '/api/v1/products') {
      if (url.searchParams.has('cursor')) { old = route; return Promise.resolve() }
      return route.fulfill({ json: { items: [product(firstID, { title: actor === firstID ? 'Original directory' : 'Current directory' })], total: 2, categoryCounts: {}, nextCursor: actor === firstID ? 'original' : undefined } })
    }
  })
  await page.goto('/market')
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect.poll(() => Boolean(old)).toBe(true)
  actor = secondID
  await refreshSession(page)
  await settle(page, old!, { items: [product(secondID, { title: 'Stale continuation' })], total: 2, categoryCounts: {} })
  await expect(page.getByRole('heading', { name: 'Current directory', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Stale continuation', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('reopening the legacy preview editor starts an independent candidate request', async ({ page }) => {
  const pending: Route[] = []
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === `/api/v1/products/${firstID}`) return route.fulfill({ json: product(firstID, { seller: { id: firstID, handle: 'seller', displayName: 'Seller' } }) })
    if (url.pathname === '/api/v1/assets') { pending.push(route); return Promise.resolve() }
  })
  await page.goto(`/market/assets/${firstID}`)
  await page.getByRole('button', { name: 'Set preview', exact: true }).click()
  await expect.poll(() => pending.length).toBe(1)
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await page.getByRole('button', { name: 'Set preview', exact: true }).click()
  await expect.poll(() => pending.length).toBe(2)
  await settle(page, pending[0]!, { items: [{ id: firstID, title: 'Stale private candidate' }], nextCursor: 'stale' })
  await expect(page.getByRole('button', { name: 'Save public preview', exact: true })).toBeDisabled()
  await settle(page, pending[1]!, { items: [{ id: secondID, title: 'Current candidate' }] })
  await page.getByRole('combobox', { name: 'Public preview', exact: true }).click()
  await expect(page.getByRole('option', { name: 'Current candidate', exact: true })).toBeVisible()
  await expect(page.getByRole('option', { name: 'Stale private candidate', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

test('managed listings offer seller management without the legacy preview form', async ({ page }) => {
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === `/api/v1/products/${firstID}`) return route.fulfill({ json: product(firstID, { listingManaged: true, seller: { id: firstID, handle: 'seller', displayName: 'Seller' } }) })
  })
  await page.goto(`/market/assets/${firstID}`)
  await expect(page.getByRole('link', { name: 'Manage product', exact: true })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Public preview', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Save public preview', exact: true })).toHaveCount(0)
  expect(unexpected).toEqual([])
})

async function mockPopups(page: Page) {
  await page.addInitScript(() => {
    const popups: { closed: boolean; location: { href: string } }[] = []
    Object.assign(window, { fixturePopups: popups })
    window.open = () => {
      const popup = { opener: null, closed: false, location: { href: 'about:blank' }, close() { this.closed = true } }
      popups.push(popup)
      return popup as unknown as Window
    }
  })
}
async function popups(page: Page) {
  return page.evaluate(() => (window as unknown as { fixturePopups: { closed: boolean; location: { href: string } }[] }).fixturePopups)
}
async function buy(page: Page) {
  await page.locator('.license-accept input').check()
  await page.locator('.product-purchase-rail button[type="submit"]').click()
}
for (const destination of ['stay', 'other-product', 'unmount'] as const) {
  test(`checkout completion respects its original product context: ${destination}`, async ({ page }) => {
    await mockPopups(page)
    let command: Route | undefined
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname.endsWith('/checkout')) { command = route; return Promise.resolve() }
    })
    await page.goto(`/market/assets/${firstID}`)
    await buy(page)
    await expect.poll(() => Boolean(command)).toBe(true)
    if (destination !== 'stay') await navigate(page, destination === 'unmount' ? '/workspace/orders' : `/market/assets/${secondID}`)
    await settle(page, command!, { checkoutUrl: 'https://checkout.fixture.test/never-open', paymentId: firstID })
    expect((await popups(page))[0]).toMatchObject({ closed: destination !== 'stay', location: { href: destination === 'stay' ? 'https://checkout.fixture.test/never-open' : 'about:blank' } })
    expect(unexpected).toEqual([])
  })
}

test('an old checkout completion cannot unlock the new product checkout', async ({ page }) => {
  await mockPopups(page)
  const pending: Route[] = []
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname.endsWith('/checkout')) { pending.push(route); return Promise.resolve() }
  })
  await page.goto(`/market/assets/${firstID}`)
  await buy(page)
  await expect.poll(() => pending.length).toBe(1)
  await navigate(page, `/market/assets/${secondID}`)
  await page.locator('.license-accept input').check()
  await expect(page.locator('.product-purchase-rail button[type="submit"]')).toBeEnabled()
  await page.locator('.product-purchase-rail button[type="submit"]').click()
  await expect.poll(() => pending.length).toBe(2)
  await settle(page, pending[0]!, { checkoutUrl: 'https://checkout.fixture.test/old', paymentId: firstID })
  await expect(page.locator('.product-purchase-rail button[type="submit"]')).toBeDisabled()
  await settle(page, pending[1]!, { checkoutUrl: 'https://checkout.fixture.test/current', paymentId: secondID })
  expect(await popups(page)).toMatchObject([{ closed: true, location: { href: 'about:blank' } }, { closed: false, location: { href: 'https://checkout.fixture.test/current' } }])
  expect(unexpected).toEqual([])
})

test('failed loading for a new seller account cannot reveal the previous draft', async ({ page }) => {
  let actor = firstID
  let fail = true
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(actor) } })
    if (url.pathname === '/api/v1/seller/licenses' && actor === secondID && fail) return route.fulfill({ status: 503, json: denied('unexpected_response') })
  })
  await page.goto('/workspace/products/new')
  await page.getByLabel('Product title', { exact: true }).fill('Private draft from previous account')
  actor = secondID
  await refreshSession(page)
  await expect(page.locator('.seller-products-page [role="alert"]')).toBeVisible()
  await expect(page.locator('.seller-product-form')).toHaveCount(0)
  fail = false
  await page.locator('.seller-products-page [role="alert"] button').click()
  await expect(page.getByLabel('Product title', { exact: true })).toHaveValue('')
  expect(unexpected).toEqual([])
})

test('revoking review permission closes the pending decision and requires fresh data on regrant', async ({ page }) => {
  let authorized = true
  let title = 'Original private review'
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(firstID, authorized) } })
    if (url.pathname === `/api/v1/admin/products/${firstID}`) return route.fulfill({ json: sellerProduct({ title }) })
  })
  await page.goto(`/admin/products/${firstID}`)
  await page.getByLabel('Decision reason (visible to seller)', { exact: true }).fill('Checked original file and rights')
  await page.getByRole('button', { name: 'Approve and publish', exact: true }).click()
  await expect(page.getByRole('alertdialog')).toBeVisible()
  authorized = false
  await refreshSession(page)
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  await expect(page.locator('.seller-product-form')).toHaveCount(0)
  title = 'Fresh authorized review'
  authorized = true
  await refreshSession(page)
  await expect(page.getByLabel('Product title', { exact: true })).toHaveValue(title)
  await expect(page.getByLabel('Decision reason (visible to seller)', { exact: true })).toHaveValue('')
  expect(unexpected).toEqual([])
})

test('review data received after revocation cannot reappear when permission returns', async ({ page }) => {
  let authorized = true
  let reads = 0
  let old: Route | undefined
  const unexpected = await mockApp(page, (route, url) => {
    if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(firstID, authorized) } })
    if (url.pathname === `/api/v1/admin/products/${firstID}`) {
      if (++reads === 1) { old = route; return Promise.resolve() }
      return route.fulfill({ json: sellerProduct({ title: 'Fresh review', version: 2 }) })
    }
  })
  await page.goto(`/admin/products/${firstID}`)
  await expect.poll(() => Boolean(old)).toBe(true)
  authorized = false
  await refreshSession(page)
  await settle(page, old!, sellerProduct({ title: 'Stale private review' }))
  authorized = true
  await refreshSession(page)
  await expect(page.getByLabel('Product title', { exact: true })).toHaveValue('Fresh review')
  expect(reads).toBe(2)
  expect(unexpected).toEqual([])
})

for (const action of ['checkout', 'preview'] as const) {
  for (const revisit of [false, true]) {
    test(`${action} conflict refresh keeps feedback within its original visit: revisit=${revisit}`, async ({ page }) => {
      await mockPopups(page)
      let reads = 0
      let refresh: Route | undefined
      const own = action === 'preview' ? { seller: { id: firstID, handle: 'seller', displayName: 'Seller' } } : {}
      const unexpected = await mockApp(page, (route, url) => {
        if (url.pathname === `/api/v1/products/${firstID}`) {
          if (++reads === 2) { refresh = route; return Promise.resolve() }
          return route.fulfill({ json: product(firstID, { ...own, title: reads === 1 ? 'Initial offer' : 'New visit' }) })
        }
        if (url.pathname.endsWith(`/${action}`)) return route.fulfill({ status: 409, json: denied('product_offer_changed') })
      })
      await page.goto(`/market/assets/${firstID}`)
      if (action === 'checkout') await buy(page)
      else {
        await page.getByRole('button', { name: 'Set preview', exact: true }).click()
        await page.getByRole('button', { name: 'Save public preview', exact: true }).click()
      }
      await expect.poll(() => Boolean(refresh)).toBe(true)
      if (revisit) {
        await navigate(page, `/market/assets/${firstID}?visit=new`)
        await expect(page.getByRole('heading', { name: 'New visit', exact: true })).toBeVisible()
      }
      await settle(page, refresh!, product(firstID, { ...own, title: 'Refreshed offer' }))
      if (revisit) await expect(page.locator('.product-purchase-rail [role="alert"]')).toHaveCount(0)
      else {
        await expect(page.locator('.product-purchase-rail [role="alert"]')).toContainText('This offer has changed.')
        await expect(page.locator('.license-accept input')).not.toBeChecked()
      }
      expect(unexpected).toEqual([])
    })
  }
}

for (const leave of [false, true]) {
  test(`saving a public preview applies only to its original view: leave=${leave}`, async ({ page }) => {
    let command: Route | undefined
    let reads = 0
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === `/api/v1/products/${firstID}`) {
        reads++
        return route.fulfill({ json: product(firstID, { seller: { id: firstID, handle: 'seller', displayName: 'Seller' } }) })
      }
      if (url.pathname.endsWith('/preview')) { command = route; return Promise.resolve() }
    })
    await page.goto(`/market/assets/${firstID}`)
    await page.getByRole('button', { name: 'Set preview', exact: true }).click()
    await page.getByRole('button', { name: 'Save public preview', exact: true }).click()
    await expect.poll(() => Boolean(command)).toBe(true)
    expect(command!.request().postDataJSON()).toEqual({ previewAssetId: null, offerVersion: `offer-${firstID}` })
    if (leave) {
      await navigate(page, `/market/assets/${secondID}`)
      await page.locator('.license-accept input').check()
    }
    await settle(page, command!, { previewAssetId: null, offerVersion: 'updated-offer' })
    if (leave) {
      await expect(page).toHaveURL(new RegExp(`/market/assets/${secondID}$`))
      await expect(page.locator('.license-accept input')).toBeChecked()
      expect(reads).toBe(1)
    } else {
      await expect(page.getByRole('button', { name: 'Set preview', exact: true })).toBeVisible()
      expect(reads).toBe(2)
    }
    expect(unexpected).toEqual([])
  })

  test(`creating a seller draft respects its original account: switch=${leave}`, async ({ page }) => {
    let actor = firstID
    let command: Route | undefined
    const accepted = sellerProduct({ title: 'New owned product', sellerId: firstID, reviewStatus: 'draft' })
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(actor) } })
      if (url.pathname === '/api/v1/assets' && url.searchParams.get('purpose') === 'product_source') return route.fulfill({ json: { items: [{ id: firstID, title: 'Owned original' }] } })
      if (url.pathname === '/api/v1/seller/products' && route.request().method() === 'POST') { command = route; return Promise.resolve() }
      if (url.pathname === `/api/v1/seller/products/${firstID}`) return route.fulfill({ json: accepted })
    })
    await page.goto('/workspace/products/new')
    await page.getByLabel('Product title', { exact: true }).fill('New owned product')
    await page.getByLabel('Description', { exact: true }).fill('Private original description')
    await chooseOption(page.locator('.seller-product-form select').nth(2), firstID)
    await page.getByLabel('AI disclosure and rights information', { exact: true }).fill('Original and reviewed by the owner')
    await page.getByLabel('Delivered file label', { exact: true }).fill('original.png')
    await page.getByRole('button', { name: 'Save draft', exact: true }).click()
    await expect.poll(() => Boolean(command)).toBe(true)
    expect(command!.request().postDataJSON().draft).toMatchObject({ title: 'New owned product', assetId: firstID, priceCents: 1900 })
    if (leave) { actor = secondID; await refreshSession(page) }
    await settle(page, command!, accepted)
    if (leave) {
      await expect(page).toHaveURL(/\/workspace\/products\/new$/)
      await expect(page.getByLabel('Product title', { exact: true })).toHaveValue('')
    } else {
      await expect(page).toHaveURL(new RegExp(`/workspace/products/${firstID}$`))
      await expect(page.getByLabel('Product title', { exact: true })).toHaveValue('New owned product')
    }
    expect(unexpected).toEqual([])
  })

  test(`a completed review command respects its original authorization: regrant=${leave}`, async ({ page }) => {
    let authorized = true
    let title = 'Review before decision'
    let command: Route | undefined
    const unexpected = await mockApp(page, (route, url) => {
      if (url.pathname === '/api/v1/auth/session') return route.fulfill({ json: { user: user(firstID, authorized) } })
      if (url.pathname === `/api/v1/admin/products/${firstID}`) return route.fulfill({ json: sellerProduct({ title }) })
      if (url.pathname.endsWith('/approve')) { command = route; return Promise.resolve() }
    })
    await page.goto(`/admin/products/${firstID}`)
    await page.getByLabel('Decision reason (visible to seller)', { exact: true }).fill('Verified the private original and rights')
    await page.getByRole('button', { name: 'Approve and publish', exact: true }).click()
    await page.getByRole('button', { name: 'Confirm decision', exact: true }).click()
    await expect.poll(() => Boolean(command)).toBe(true)
    if (leave) {
      authorized = false
      await refreshSession(page)
      await expect(page.getByRole('alertdialog')).toHaveCount(0)
      title = 'Fresh authorized review'
      authorized = true
      await refreshSession(page)
      await expect(page.getByLabel('Product title', { exact: true })).toHaveValue(title)
    }
    await settle(page, command!, sellerProduct({ title: 'Completed original decision', version: 2, reviewStatus: 'approved', status: 'active' }))
    await expect(page.getByLabel('Product title', { exact: true })).toHaveValue(leave ? title : 'Completed original decision')
    expect(unexpected).toEqual([])
  })
}
