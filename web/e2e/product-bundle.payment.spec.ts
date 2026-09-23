import { randomUUID } from 'node:crypto'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'
import { mockExternalCheckout, paymentFixtureState, signedEvent } from './helpers/payment'

// Application APIs and workers are real. Only the external payment provider
// and its hosted checkout are synthetic, on an isolated database/schema.
test('real bundle publication, purchase, downloads and confirmed refund', async ({ browser, baseURL }) => {
  test.setTimeout(180_000)
  const seller = await browser.newContext({ baseURL })
  const admin = await browser.newContext({ baseURL })
  const buyer = await browser.newContext({ baseURL })
  try {
    for (const [context, role] of [[seller, 'creator'], [admin, 'admin'], [buyer, 'publisher']] as const) {
      expect((await context.request.post('/api/v1/auth/login', { data: fixtureCredentials(role) })).ok()).toBeTruthy()
    }
    const page = await seller.newPage()
    const files = [] as { id: string; name: string; body: string }[]
    for (const name of ['workflow.txt', '使用说明.txt']) {
      const body = `Independent licensed original: ${name}`
      const upload = await seller.request.post('/api/v1/assets/uploads', { headers: { 'Idempotency-Key': randomUUID() }, multipart: {
        title: name, file: { name, mimeType: 'text/plain', buffer: Buffer.from(body) },
      } })
      expect(upload.ok(), await upload.text()).toBeTruthy()
      const asset = await upload.json()
      files.push({ id: asset.id, name, body })
      await expect.poll(async () => (await (await seller.request.get(`/api/v1/assets/${asset.id}`)).json()).scanStatus).toBe('clean')
    }
    await page.goto('/workspace/products/new')
    await page.getByLabel('Product title', { exact: true }).fill('Browser verified creative bundle')
    await page.getByLabel('Description', { exact: true }).fill('Two independently authored files for a complete payment journey.')
    await chooseOption(page.locator('.seller-product-form select').nth(2), files[0]!.id)
    await page.getByLabel('AI disclosure and rights information', { exact: true }).fill('Original authored materials with the required distribution rights.')
    await page.getByLabel('Delivered file label', { exact: true }).fill(files[0]!.name)
    await page.getByLabel('Price (USD)', { exact: true }).fill('19.00')
    await page.getByRole('button', { name: 'Add delivery file', exact: true }).click()
    await chooseOption(page.locator('.seller-product-file').nth(1).locator('select'), files[1]!.id)
    await page.getByLabel('File 2 · Download name', { exact: true }).fill(files[1]!.name)
    await page.getByRole('button', { name: 'Save draft', exact: true }).click()
    await expect(page).toHaveURL(/\/workspace\/products\/[0-9a-f-]{36}$/)
    const productId = new URL(page.url()).pathname.split('/').at(-1)!
    await page.getByRole('checkbox').last().check()
    await page.getByRole('button', { name: 'Submit for review', exact: true }).click()
    await expect(page.locator('.ui-card-actions').first()).toContainText('In review')

    const review = await admin.newPage()
    await review.goto(`/admin/products/${productId}`)
    for (const [index, file] of files.entries()) {
      const href = await review.getByRole('link', { name: `Review file ${index + 1} · ${file.name}`, exact: true }).getAttribute('href')
      expect(await (await admin.request.get(href!)).text()).toBe(file.body)
    }
    await review.getByLabel('Decision reason (visible to seller)', { exact: true }).fill('Verified both original files, their download names and license terms.')
    await review.getByRole('button', { name: 'Approve and publish', exact: true }).click()
    await review.getByRole('button', { name: 'Confirm decision', exact: true }).click()
    await expect(review.locator('.ui-card-actions').first()).toContainText('Approved')

    // Intercept the external origin before opening the popup; no browser request
    // is allowed to reach Stripe. Do not intercept the application API.
    await mockExternalCheckout(buyer)
    const initialFixture = await paymentFixtureState(buyer.request)
    const purchase = await buyer.newPage()
    await purchase.goto(`/market/assets/${productId}`)
    const checkoutButton = purchase.getByRole('button', { name: 'Continue to secure checkout', exact: true })
    await expect(checkoutButton).toBeDisabled()
    await purchase.locator('.license-accept').getByRole('checkbox').check()
    const checkoutResponse = purchase.waitForResponse(response => response.url().endsWith(`/products/${productId}/checkout`) && response.request().method() === 'POST')
    const popupPromise = purchase.waitForEvent('popup')
    await checkoutButton.click()
    const response = await checkoutResponse
    expect(response.ok(), await response.text()).toBeTruthy()
    const checkout = await response.json()
    expect(checkout.amountCents).toBe(1900)
    expect(checkout.realCharge).toBe(false)
    const popup = await popupPromise
    await popup.getByRole('button', { name: 'Confirm test payment', exact: true }).click()
    const returnPath = `/workspace/orders?payment=success&orderId=${checkout.orderId}&paymentId=${checkout.paymentId}`
    await popup.goto(returnPath)
    await expect(popup.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'pending')
    const before = await (await buyer.request.get(`/api/v1/orders/${checkout.orderId}`)).json()
    expect(before.status).toBe('payment_pending')
    expect(before.assetId).toBeFalsy()

    const paidFixture = await paymentFixtureState(buyer.request)
    const metadata = { hcai_payment_id: checkout.paymentId, hcai_resource_id: productId, hcai_purpose: 'product' }
    await signedEvent(buyer.request, 'checkout.session.completed', {
      id: paidFixture.lastCheckoutId, object: 'checkout.session', status: 'complete', payment_status: 'paid',
      amount_total: 1900, currency: 'usd', payment_intent: 'pi_test_paymentdrill001', metadata,
    })
    await expect(popup.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'fulfilled', { timeout: 30_000 })
    const order = await (await buyer.request.get(`/api/v1/orders/${checkout.orderId}`)).json()
    expect(order.events.filter((event: { toStatus: string }) => event.toStatus === 'fulfilled')).toHaveLength(1)
    expect(order.assetId).toBeTruthy()
    await popup.locator('.product-payment-primary').click()
    await expect(popup).toHaveURL(new RegExp(`/workspace/assets/${order.assetId}$`))
    const manifest = popup.locator('.ui-data-list[aria-label="Delivery files"]')
    await expect(manifest.locator('.ui-card-actions')).toHaveCount(2)
    await expect(popup.getByRole('link', { name: 'Use in Create', exact: true })).toHaveCount(0)
    const asset = await (await buyer.request.get(`/api/v1/assets/${order.assetId}`)).json()
    const packageURL = `/api/v1/assets/${order.assetId}/content`
    for (const [index, file] of files.entries()) {
      const downloadPromise = popup.waitForEvent('download')
      await manifest.getByRole('link', { name: `Download ${file.name}`, exact: true }).click()
      const download = await downloadPromise
      expect(download.suggestedFilename()).toBe(file.name)
      const bytes = await readFile((await download.path())!)
      expect(bytes.toString()).toBe(file.body)
      expect(createHash('sha256').update(bytes).digest('hex')).toBe(asset.provenance.purchase.delivery.files[index].sha256)
      expect((await seller.request.get(`${packageURL}?fileIndex=${index}`)).status()).toBe(403)
    }
    const downloadPromise = popup.waitForEvent('download')
    await popup.getByRole('link', { name: 'Download complete package', exact: true }).click()
    const zip = await readFile((await (await downloadPromise).path())!)
    expect(zip.subarray(0, 2).toString()).toBe('PK')
    expect(zip.length).toBe(asset.provenance.purchase.delivery.sizeBytes)
    expect(createHash('sha256').update(zip).digest('hex')).toBe(asset.provenance.purchase.delivery.sha256)
    for (const width of [390, 1308]) {
      await popup.setViewportSize({ width, height: 900 })
      await manifest.scrollIntoViewIfNeeded()
      expect(await popup.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await popup.screenshot({ path: `/tmp/hcai-real-bundle-purchase-${width}.png`, fullPage: true })
    }

    await popup.goto('/workspace/orders')
    const orderRow = popup.locator('.order-row').filter({ hasText: checkout.orderId })
    await orderRow.locator('.refund-form textarea').fill('The purchased files do not fit the requirements of my project.')
    await orderRow.getByRole('button', { name: 'Request payment gateway refund', exact: true }).click()
    await expect(orderRow).toHaveAttribute('data-order-status', 'refund_requested')
    expect((await buyer.request.get(`${packageURL}?fileIndex=1`)).ok()).toBeTruthy()
    await expect.poll(async () => (await paymentFixtureState(buyer.request)).refundCalls, { timeout: 30_000 }).toBe(initialFixture.refundCalls + 1)
    const fixture = await paymentFixtureState(buyer.request)
    expect(fixture.checkoutCalls).toBe(initialFixture.checkoutCalls + 1)
    expect(fixture.lastCheckoutReference).toBe(checkout.paymentId)
    expect(fixture.lastRefundPaymentIntent).toBe('pi_test_paymentdrill001')
    expect(fixture.lastRefundOperationId).toBeTruthy()
    await signedEvent(buyer.request, 'refund.updated', {
      id: fixture.lastRefundId, object: 'refund', status: 'succeeded', amount: 1900, currency: 'usd',
      payment_intent: 'pi_test_paymentdrill001', metadata: { ...metadata, hcai_refund_operation_id: fixture.lastRefundOperationId },
    })
    await expect.poll(async () => (await (await buyer.request.get(`/api/v1/orders/${checkout.orderId}`)).json()).status, { timeout: 30_000 }).toBe('refunded')
    const refunded = await (await buyer.request.get(`/api/v1/orders/${checkout.orderId}`)).json()
    expect(refunded.events.filter((event: { toStatus: string }) => event.toStatus === 'refunded')).toHaveLength(1)
    await popup.reload()
    await expect(orderRow).toHaveAttribute('data-order-status', 'refunded')
    await expect(orderRow.locator('.refund-form')).toHaveCount(0)
    await popup.goto(`/workspace/assets/${order.assetId}`)
    await expect(manifest.locator('.ui-card-actions')).toHaveCount(2)
    await expect(manifest.getByRole('link')).toHaveCount(0)
    await expect(popup.getByRole('link', { name: 'Download complete package', exact: true })).toHaveCount(0)
    for (const path of [packageURL, `${packageURL}?fileIndex=0`, `${packageURL}?fileIndex=1`]) {
      expect((await buyer.request.get(path)).status()).toBe(403)
    }
    for (const file of files) expect(await (await seller.request.get(`/api/v1/assets/${file.id}/content`)).text()).toBe(file.body)
  } finally {
    await seller.close()
    await admin.close()
    await buyer.close()
  }
})
