import { execFileSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { mockExternalCheckout, paymentFixtureState, signedEvent } from './helpers/payment'
import { chooseOption } from './helpers/select'

const productId = '00000000-0000-4000-8000-000000000503'
function sql(statement: string) {
  const target = new URL(process.env.E2E_SQL_URL || '')
  const name = process.env.E2E_OWNED_DATABASE || ''
  if (!/^hcai_e2e_[0-9a-f]{32}$/.test(name) || target.pathname !== `/${name}` || !['127.0.0.1', 'localhost'].includes(target.hostname)) throw new Error('SQL requires owned disposable database')
  return execFileSync('psql', [target.toString(), '-X', '-qAt', '-v', 'ON_ERROR_STOP=1'], {
    input: statement, encoding: 'utf8', env: { ...process.env, PGOPTIONS: '-c search_path=test_app' },
  }).trim()
}
function id(value: string) {
  expect(value).toMatch(/^[0-9a-f-]{36}$/)
  return `'${value}'`
}
async function register(request: APIRequestContext) {
  const handle = 'buyer_' + randomUUID().replaceAll('-', '').slice(0, 16)
  const credentials = { email: `${handle}@example.test`, password: 'isolated-password-2026' }
  const response = await request.post('/api/v1/auth/register', { data: { ...credentials, handle, displayName: handle, locale: 'en-US', timezone: 'UTC' } })
  expect(response.status(), await response.text()).toBe(201)
  return credentials
}
async function noOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
}

test('empty private workspaces, failed reads, keyboard login return and session switch use real auth', async ({ page }, info) => {
  const credentials = await register(page.request)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/workspace/payouts')
  await expect(page.getByText('No payout requests yet', { exact: true })).toBeVisible()
  expect((await (await page.request.get('/api/v1/seller/payout-requests')).json()).items).toEqual([])
  await noOverflow(page)
  await page.screenshot({ path: info.outputPath('empty-payouts-mobile.png'), fullPage: true })
  await page.route('**/api/v1/seller/funds', route => route.abort('connectionfailed'))
  await page.reload()
  await expect(page.locator('.seller-payouts-page [role="alert"]')).toBeVisible()
  await page.unroute('**/api/v1/seller/funds')
  await page.getByRole('button', { name: 'Reload', exact: true }).first().click()
  await expect(page.getByText('No payout requests yet', { exact: true })).toBeVisible()
  await page.goto('/admin/product-disputes')
  await expect(page.getByText('Finance access required', { exact: true })).toBeVisible()
  expect((await page.request.get('/api/v1/admin/product-disputes')).status()).toBe(403)
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Sign out', exact: true }).click()
  await expect(page.getByLabel('Email', { exact: true })).toBeVisible()
  expect((await page.request.get('/api/v1/orders')).status()).toBe(401)
  await page.goto(`/market/assets/${productId}`)
  const signin = page.locator('.market-auth-prompt').getByRole('link', { name: 'Sign in', exact: true })
  await signin.focus()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/auth\?auth=login/)
  await page.getByLabel('Email', { exact: true }).fill(credentials.email)
  await page.locator('.account-form button[type="submit"]').press('Enter')
  await page.locator('input[autocomplete="current-password"]').fill(credentials.password)
  await page.locator('.account-form button[type="submit"]').press('Enter')
  await expect(page).toHaveURL(`/market/assets/${productId}`)
  const accept = page.locator('.license-accept input')
  await accept.focus()
  await page.keyboard.press('Space')
  await expect(accept).toBeChecked()
  await expect(page.getByRole('button', { name: 'Continue to secure checkout', exact: true })).toBeEnabled()
  await noOverflow(page)
  await page.screenshot({ path: info.outputPath('keyboard-login-return.png'), fullPage: true })
  const session = await (await page.request.get('/api/v1/auth/session')).json()
  expect(sql(`SELECT count(*) FROM orders WHERE buyer_id=${id(session.user.id)}`)).toBe('0')
})

test('lost checkout and payout responses recover one real order and reservation; dispute operations retain evidence', async ({ browser, baseURL }, info) => {
  test.setTimeout(150_000)
  const buyer = await browser.newContext({ baseURL })
  const seller = await browser.newContext({ baseURL })
  const finance = await browser.newContext({ baseURL })
  const visitor = await browser.newContext({ baseURL })
  const settings = JSON.parse(sql('SELECT row_to_json(s) FROM product_settlement_settings s'))
  const windows = JSON.parse(sql('SELECT json_agg(json_build_object(\'code\',code,\'days\',refund_window_days)) FROM licenses')) as { code: string; days: number }[]
  try {
    await register(buyer.request)
    expect((await seller.request.post('/api/v1/auth/login', { data: fixtureCredentials('studio') })).ok()).toBeTruthy()
    expect((await finance.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })).ok()).toBeTruthy()
    // Configure policy BEFORE creating the order. Never force a financial output/state.
    sql("UPDATE product_settlement_settings SET hold_days=0,payout_mode='seller_payout'; UPDATE licenses SET refund_window_days=0")
    const page = await buyer.newPage()
    await mockExternalCheckout(buyer)
    await page.goto(`/market/assets/${productId}`)
    await page.locator('.license-accept input').check()
    const before = await paymentFixtureState(buyer.request)
    const keys: string[] = []
    let checkout!: { orderId: string; paymentId: string; amountCents: number }
    await page.route(`**/api/v1/products/${productId}/checkout`, async route => {
      keys.push(route.request().headers()['idempotency-key']!)
      const response = await route.fetch()
      expect(response.ok(), await response.text()).toBeTruthy()
      checkout = await response.json()
      if (keys.length === 1) await route.abort('connectionfailed')
      else await route.fulfill({ response })
    })
    await page.getByRole('button', { name: 'Continue to secure checkout', exact: true }).click()
    await expect(page.locator('.product-purchase-rail [role="alert"]')).toBeVisible()
    expect(sql(`SELECT count(*) FROM orders WHERE id=${id(checkout.orderId)}`)).toBe('1')
    expect(sql(`SELECT status FROM orders WHERE id=${id(checkout.orderId)}`)).toBe('payment_pending')
    await page.screenshot({ path: info.outputPath('checkout-response-lost.png'), fullPage: true })
    const popupPromise = page.waitForEvent('popup')
    await page.getByRole('button', { name: 'Continue to secure checkout', exact: true }).click()
    const popup = await popupPromise
    await expect(popup.getByRole('button', { name: 'Confirm test payment', exact: true })).toBeVisible()
    await expect.poll(() => keys.length).toBe(2)
    expect(keys[0]).toBeTruthy()
    expect(keys[1]).toBe(keys[0])
    expect((await paymentFixtureState(buyer.request)).checkoutCalls - before.checkoutCalls).toBe(1)
    const returnURL = `/workspace/orders?payment=success&orderId=${checkout.orderId}&paymentId=${checkout.paymentId}`
    await popup.goto(returnURL)
    await expect(popup.locator('.product-payment-status')).not.toHaveAttribute('data-payment-state', 'fulfilled')
    expect((await (await buyer.request.get(`/api/v1/orders/${checkout.orderId}`)).json()).assetId).toBeFalsy()
    expect((await visitor.request.get(`/api/v1/orders/${checkout.orderId}`)).status()).toBe(401)
    expect((await seller.request.get(`/api/v1/orders/${checkout.orderId}`)).status()).toBe(404)
    const fixture = await paymentFixtureState(buyer.request)
    const pi = `pi_browser_${checkout.paymentId.replaceAll('-', '')}`
    const charge = `ch_browser_${checkout.paymentId.replaceAll('-', '')}`
    await signedEvent(buyer.request, 'checkout.session.completed', {
      id: fixture.lastCheckoutId, object: 'checkout.session', status: 'complete', payment_status: 'paid',
      amount_total: checkout.amountCents, currency: 'usd', payment_intent: pi, charge,
      metadata: { hcai_payment_id: checkout.paymentId, hcai_resource_id: productId, hcai_purpose: 'product' },
    })
    // Reload loses component memory; durable server evidence must recover the order.
    await popup.reload()
    await expect(popup.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'fulfilled', { timeout: 30_000 })
    const settlementQuery = `SELECT row_to_json(s) FROM product_settlements s WHERE payment_id=${id(checkout.paymentId)}`
    await expect.poll(() => JSON.parse(sql(settlementQuery) || '{}').status, { timeout: 30_000 }).toBe('available')
    const settlement = JSON.parse(sql(settlementQuery))
    expect(sql(`SELECT count(*) FROM seller_ledger_entries WHERE settlement_id=${id(settlement.id)} AND entry_type='settlement_credit'`)).toBe('1')
    const sales = await seller.newPage()
    await sales.setViewportSize({ width: 390, height: 844 })
    await sales.goto(`/workspace/sales/${checkout.orderId}`)
    await expect(sales.locator('.seller-sales-page')).toContainText('Editorial architecture prompt system')
    await expect(sales.locator('.seller-sales-page')).toContainText('Test')
    await noOverflow(sales)
    await sales.screenshot({ path: info.outputPath('seller-sale-mobile.png'), fullPage: true })
    // Synthetic destination evidence only; no source transfer/bank payout is executed.
    sql(`INSERT INTO payment_destinations(provider,user_id,destination_id,status,charges_enabled,payouts_enabled,details_submitted,original_merchant_id,original_store_id,original_live_mode,original_endpoint,original_api_version,original_request_version,verified_at)
      SELECT 'stripe',${id(settlement.seller_id)},'acct_test_goal9','verified',true,true,true,identity->>'merchantId',COALESCE(identity->>'storeId',''),false,identity->>'endpoint',identity->>'apiVersion',identity->>'requestVersion',now() FROM product_checkout_requests WHERE payment_id=${id(checkout.paymentId)}`)
    await sales.goto('/workspace/payouts')
    await chooseOption(sales.locator('.seller-payouts-page select').first(), settlement.id)
    const payoutKeys: string[] = []
    let payout!: { id: string; status: string }
    await sales.route('**/api/v1/seller/payout-requests', async route => {
      if (route.request().method() !== 'POST') return route.continue()
      payoutKeys.push(route.request().headers()['idempotency-key']!)
      const response = await route.fetch()
      expect(response.ok(), await response.text()).toBeTruthy()
      payout = await response.json()
      if (payoutKeys.length === 1) await route.abort('connectionfailed')
      else await route.fulfill({ response })
    })
    await sales.getByRole('button', { name: 'Submit request', exact: true }).click()
    await expect(sales.getByRole('button', { name: 'Retry original request', exact: true })).toBeVisible()
    await sales.getByRole('button', { name: 'Retry original request', exact: true }).click()
    await expect(sales.locator('.seller-payout-row')).toContainText(payout.id)
    expect(payoutKeys).toHaveLength(2)
    expect(payoutKeys[1]).toBe(payoutKeys[0])
    expect(sql(`SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=${id(payout.id)} AND entry_type='payout_reservation'`)).toBe('1')
    expect((await buyer.request.get(`/api/v1/seller/payout-requests/${payout.id}`)).status()).toBe(404)
    await sales.screenshot({ path: info.outputPath('payout-recovered-mobile.png'), fullPage: true })
    await noOverflow(sales)
    await sales.getByRole('button', { name: 'Cancel request', exact: true }).click()
    await sales.getByRole('button', { name: 'Confirm cancellation', exact: true }).click()
    await expect(sales.locator('.seller-payout-row')).toContainText('Cancelled')
    expect(sql(`SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=${id(payout.id)} AND entry_type='payout_release'`)).toBe('1')
    expect(sql('SELECT count(*) FROM seller_payout_transfers')).toBe('0')
    expect(sql(`SELECT status FROM seller_payout_requests WHERE id=${id(payout.id)}`)).toBe('cancelled')

    await signedEvent(buyer.request, 'charge.dispute.created', { id: `dp_${checkout.paymentId.replaceAll('-', '')}`, object: 'dispute',
      payment_intent: pi, charge, amount: checkout.amountCents, currency: 'usd', status: 'needs_response', reason: 'fraudulent', network_reason_code: 'visa_10_4',
      evidence_details: { due_by: Math.floor(Date.now() / 1000) + 86400 } })
    await expect.poll(() => sql(`SELECT count(*) FROM product_payment_disputes WHERE payment_id=${id(checkout.paymentId)}`)).toBe('1')
    const dispute = sql(`SELECT id FROM product_payment_disputes WHERE payment_id=${id(checkout.paymentId)}`)
    expect(sql(`SELECT status FROM product_settlements WHERE id=${id(settlement.id)}`)).toBe('dispute_hold')
    const operations = await finance.newPage()
    await operations.setViewportSize({ width: 390, height: 844 })
    await operations.goto(`/admin/product-disputes/${dispute}`)
    await expect(operations.locator('.product-dispute-detail')).toContainText('Bound to order')
    await chooseOption(operations.locator('.product-dispute-operation-form select').first(), 'request_evidence')
    await operations.getByLabel('Operation reason', { exact: true }).fill('Request delivery evidence for this isolated synthetic dispute.')
    const operationKeys: string[] = []
    await operations.route(`**/api/v1/admin/product-disputes/${dispute}/operations`, async route => {
      operationKeys.push(route.request().headers()['idempotency-key']!)
      const response = await route.fetch()
      expect(response.ok(), await response.text()).toBeTruthy()
      if (operationKeys.length === 1) await route.abort('connectionfailed')
      else await route.fulfill({ response })
    })
    await operations.getByRole('button', { name: 'Review operation', exact: true }).click()
    await operations.getByRole('button', { name: 'Confirm operation', exact: true }).click()
    await expect(operations.getByRole('button', { name: 'Retry original operation', exact: true })).toBeVisible()
    await operations.getByRole('button', { name: 'Retry original operation', exact: true }).click()
    await expect(operations.getByText(/Original operation recovered. Current version/)).toBeVisible()
    expect(operationKeys).toHaveLength(2)
    expect(operationKeys[1]).toBe(operationKeys[0])
    const detail = await (await finance.request.get(`/api/v1/admin/product-disputes/${dispute}`)).json()
    expect(detail.reviewStatus).toBe('evidence_requested')
    expect(detail.operations).toHaveLength(1)
    expect(detail.events).toHaveLength(1) // duplicate webhook retained once
    await noOverflow(operations)
    await operations.screenshot({ path: info.outputPath('dispute-evidence-mobile.png'), fullPage: true })
    expect((await buyer.request.get(`/api/v1/admin/product-disputes/${dispute}`)).status()).toBe(403)
    await info.attach('financial-evidence.json', { contentType: 'application/json', body: Buffer.from(JSON.stringify({ orderId: checkout.orderId, paymentId: checkout.paymentId, settlementId: settlement.id, grossCents: settlement.gross_amount_cents, feeCents: settlement.fee_cents, netCents: settlement.net_amount_cents, payoutId: payout.id, payoutStatus: 'cancelled', reservationCount: 1, releaseCount: 1, providerTransfers: 0, disputeId: dispute, disputeOperations: detail.operations.length, liveMode: false }, null, 2)) })
    // Switch the actual cookie and reload: prior finance data must disappear.
    expect((await operations.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    await operations.reload()
    await expect(operations.getByText('Finance access required', { exact: true })).toBeVisible()
    await expect(operations.locator('.product-dispute-detail')).toHaveCount(0)
  } finally {
    sql(`UPDATE product_settlement_settings SET hold_days=${Number(settings.hold_days)},payout_mode='${settings.payout_mode === 'seller_payout' ? 'seller_payout' : 'automatic'}'`)
    for (const window of windows) {
      expect(window.code).toMatch(/^[a-z0-9-]+$/)
      sql(`UPDATE licenses SET refund_window_days=${Number(window.days)} WHERE code='${window.code}'`)
    }
    await Promise.all([buyer.close(), seller.close(), finance.close(), visitor.close()])
  }
})
