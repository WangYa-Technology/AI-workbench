import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

test('unresolved original payment keeps its command and closes the unused checkout window', async ({ page, context }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  const productID = '00000000-0000-4000-8000-000000000503'
  const original = await (await page.request.get(`/api/v1/products/${productID}`)).json()
  const keys: string[] = []
  await page.route('**/api/v1/meta', async route => {
    const response = await route.fetch()
    const meta = await response.json()
    await route.fulfill({ json: { ...meta, paymentProvider: { ...meta.paymentProvider, enabled: true } } })
  })
  await page.route(`**/api/v1/products/${productID}`, route => route.fulfill({ json: { ...original, ownedAssetId: undefined } }))
  await page.route(`**/api/v1/products/${productID}/checkout`, async route => {
    keys.push(route.request().headers()['idempotency-key'] || '')
    await route.fulfill({ status: 409, json: { error: { code: 'payment_reconciliation_required', message: 'Original payment unresolved', retryable: false } } })
  })
  await page.goto(`/market/assets/${productID}`)
  await page.locator('.license-accept input').check()
  const purchase = page.locator('.product-purchase-rail button[type="submit"]')
  await purchase.click()
  await expect(page.locator('.product-purchase-rail [role="alert"]')).toContainText('original payment needs verification')
  await expect.poll(() => context.pages().length).toBe(1)
  expect(keys).toHaveLength(1)
  await purchase.click()
  await expect.poll(() => keys.length).toBe(2)
  await expect.poll(() => context.pages().length).toBe(1)
  expect(keys[0]).not.toBe('')
  expect(keys[1]).toBe(keys[0])
})

test('changed offer reloads the terms and requires explicit acceptance again', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  const productID = '00000000-0000-4000-8000-000000000503'
  const original = await (await page.request.get(`/api/v1/products/${productID}`)).json()
  const before = { ...original, ownedAssetId: undefined, offerVersion: 'a'.repeat(64) }
  const after = { ...before, title: 'Revised licensed product', offerVersion: 'b'.repeat(64), license: { ...before.license, terms: 'Revised accepted terms require another review.' } }
  let offer = before
  let attempts = 0
  await page.route('**/api/v1/meta', async route => {
    const response = await route.fetch()
    const meta = await response.json()
    await route.fulfill({ json: { ...meta, paymentProvider: { ...meta.paymentProvider, enabled: true } } })
  })
  await page.route(`**/api/v1/products/${productID}`, route => route.fulfill({ json: offer }))
  await page.route(`**/api/v1/products/${productID}/checkout`, async route => {
    attempts++
    expect(route.request().postDataJSON()).toEqual({ licenseAccepted: true, offerVersion: before.offerVersion })
    offer = after
    await route.fulfill({ status: 409, json: { error: { code: 'product_offer_changed', message: 'Offer changed', retryable: false } } })
  })
  await page.goto(`/market/assets/${productID}`)
  await expect(page.getByRole('heading', { name: before.title, exact: true })).toBeVisible()
  const acceptance = page.locator('.license-accept input')
  const purchase = page.locator('.product-purchase-rail button[type="submit"]')
  await acceptance.check()
  await expect(purchase).toBeEnabled()
  await purchase.click()
  await expect(page.getByRole('heading', { name: after.title, exact: true })).toBeVisible()
  await expect(acceptance).not.toBeChecked()
  await expect(purchase).toBeDisabled()
  await expect(page.locator('.product-purchase-rail [role="alert"]')).toContainText('offer has changed')
  expect(attempts).toBe(1)
  await acceptance.check()
  await expect(purchase).toBeEnabled()
})

test('disabled checkout cannot grant an order or licensed Asset', async ({ page }) => {
  const handle = `purchase_${Date.now().toString(36)}`
  const registration = await page.request.post('/api/v1/auth/register', { data: {
    email: `${handle}@example.test`, password: `purchase-test-${handle}`, handle,
    displayName: 'Purchase Boundary', locale: 'en-US', timezone: 'UTC',
  } })
  expect(registration.status()).toBe(201)
  const productID = '00000000-0000-4000-8000-000000000503'
  await page.goto(`/market/assets/${productID}`)
  await expect(page.getByRole('heading', { name: 'Editorial architecture prompt system', exact: true, level: 1 })).toBeVisible()
  await expect(page.getByText('The payment gateway is not enabled, so product checkout is unavailable.', { exact: true })).toBeVisible()
  await page.locator('.license-accept input').check()
  await expect(page.locator('.product-purchase-rail button[type="submit"]')).toBeDisabled()
  const beforeOrders = await (await page.request.get('/api/v1/orders')).json()
  const beforeAssets = await (await page.request.get('/api/v1/assets')).json()
  const checkout = await page.request.post(`/api/v1/products/${productID}/checkout`, {
    headers: { 'Idempotency-Key': `disabled-checkout-${handle}` }, data: { licenseAccepted: true },
  })
  expect(checkout.status()).toBe(503)
  expect(await (await page.request.get('/api/v1/orders')).json()).toEqual(beforeOrders)
  expect(await (await page.request.get('/api/v1/assets')).json()).toEqual(beforeAssets)
  await page.request.post('/api/v1/auth/logout')
  expect((await page.request.post(`/api/v1/products/${productID}/checkout`, {
    headers: { 'Idempotency-Key': `anonymous-checkout-${handle}` }, data: { licenseAccepted: true },
  })).status()).toBe(401)
})

test('product navigation clears the previous offer on failure and ignores late responses', async ({ page }) => {
  const originalID = '00000000-0000-4000-8000-000000000503'
  const missingID = '00000000-0000-4000-8000-000000009999'
  const lateID = '00000000-0000-4000-8000-000000009998'
  const originalPath = `/market/assets/${originalID}`
  const title = 'Editorial architecture prompt system'
  const product = await (await page.request.get(`/api/v1/products/${originalID}`)).json()
  const navigate = async (path: string) => {
    await page.evaluate(async (target) => {
      const app = document.querySelector('#app') as Element & {
        __vue_app__: { config: { globalProperties: { $router: { push: (path: string) => Promise<void> } } } }
      }
      await app.__vue_app__.config.globalProperties.$router.push(target)
    }, path)
  }
  await page.goto(originalPath)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()

  let releaseFailure!: () => void
  const failureGate = new Promise<void>(resolve => { releaseFailure = resolve })
  await page.route(`**/api/v1/products/${missingID}`, async route => {
    await failureGate
    await route.fulfill({ status: 404, json: { error: { code: 'product_not_found', message: 'Product unavailable', retryable: false } } })
  })
  await navigate(`/market/assets/${missingID}`)
  await expect(page.locator('.product-purchase-rail')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: title, exact: true })).toHaveCount(0)
  releaseFailure()
  await expect(page.locator('.market-page [role="alert"]')).toBeVisible()
  await expect(page.locator('.product-detail-layout')).toHaveCount(0)

  await page.goBack()
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  await page.goForward()
  await expect(page.locator('.market-page [role="alert"]')).toBeVisible()
  await page.goBack()
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()

  let releaseLate!: () => void
  const lateGate = new Promise<void>(resolve => { releaseLate = resolve })
  await page.route(`**/api/v1/products/${lateID}`, async route => {
    await lateGate
    await route.fulfill({ json: { ...product, id: lateID, title: 'Late product response' } })
  })
  const requested = page.waitForRequest(`**/api/v1/products/${lateID}`)
  await navigate(`/market/assets/${lateID}`)
  await requested
  await expect(page.locator('.product-detail-layout')).toHaveCount(0)
  await navigate(originalPath)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  const received = page.waitForResponse(`**/api/v1/products/${lateID}`)
  releaseLate()
  await received
  await expect(page.getByRole('heading', { name: 'Late product response', exact: true })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
})

test('prepared checkout recovery links to orders and a proven closure requires new acceptance', async ({ page, context }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  const productID = '00000000-0000-4000-8000-000000000503'
  const original = await (await page.request.get(`/api/v1/products/${productID}`)).json()
  await page.route('**/api/v1/meta', async route => {
    const meta = await (await route.fetch()).json()
    await route.fulfill({ json: { ...meta, paymentProvider: { ...meta.paymentProvider, enabled: true } } })
  })
  await page.route(`**/api/v1/products/${productID}`, route => route.fulfill({ json: { ...original, ownedAssetId: undefined } }))
  const keys: string[] = []
  await page.route(`**/api/v1/products/${productID}/checkout`, route => {
    keys.push(route.request().headers()['idempotency-key'])
    return route.fulfill({ status: 409, json: { error: {
      code: keys.length === 2 ? 'payment_checkout_closed' : 'payment_checkout_preparation_failed', retryable: false,
    } } })
  })
  await page.goto(`/market/assets/${productID}`)
  const acceptance = page.locator('.license-accept input')
  const purchase = page.locator('.product-purchase-rail button[type="submit"]')
  await acceptance.check()
  await purchase.click()
  await expect(page.locator('.product-purchase-rail [role="alert"]')).toContainText('Delivery could not be prepared')
  await expect(page.locator('.product-purchase-rail a[href="/workspace/orders"]')).toBeVisible()
  await expect.poll(() => context.pages().length).toBe(1)
  await purchase.click()
  await expect(page.locator('.product-purchase-rail [role="alert"]')).toContainText('closed before payment started')
  await expect(acceptance).not.toBeChecked()
  await expect(purchase).toBeDisabled()
  expect(keys).toHaveLength(2)
  expect(keys[1]).toBe(keys[0])
  await acceptance.check()
  await purchase.click()
  await expect.poll(() => keys.length).toBe(3)
  expect(keys[2]).not.toBe(keys[0])
  await expect.poll(() => context.pages().length).toBe(1)
})
