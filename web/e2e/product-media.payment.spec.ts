import { randomUUID } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { expect, test, type Locator, type Page } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'
import { mockExternalCheckout, paymentFixtureState, signedEvent } from './helpers/payment'
import { chooseOption } from './helpers/select'

const formats = [
  { ext: 'png', kind: 'image', mime: 'image/png' },
  { ext: 'jpg', kind: 'image', mime: 'image/jpeg' },
  { ext: 'mp4', kind: 'video', mime: 'video/mp4' },
  { ext: 'wav', kind: 'audio', mime: 'audio/wav' },
  { ext: 'mp3', kind: 'audio', mime: 'audio/mpeg' },
  { ext: 'txt', kind: 'document', mime: 'text/plain; charset=utf-8' },
]

async function verifyMedia(page: Page, renderer: Locator, kind: string, text: string) {
  await expect(renderer).toHaveAttribute('data-kind', kind)
  if (kind === 'image') {
    await expect.poll(() => renderer.locator('img').evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth === 160)).toBe(true)
  } else if (kind === 'document') {
    if (text.length > 65536) {
      await expect(renderer.locator('pre')).toHaveText(text.slice(0, 65536))
      expect((await renderer.locator('pre').textContent())?.length).toBe(65536)
      await expect(renderer.getByText('Showing the beginning of this file. Open or download the original to read the full content.', { exact: true })).toBeVisible()
      await expect(renderer).not.toContainText('END-OF-PAID-TEXT')
    } else await expect(renderer.locator('pre')).toHaveText(text)
  } else {
    const media = renderer.locator(kind)
    await expect.poll(() => media.evaluate((element: HTMLMediaElement) => element.readyState >= 2 && element.error === null)).toBe(true)
    await media.evaluate(async (element: HTMLMediaElement) => { element.muted = true; await element.play() })
    await expect.poll(() => media.evaluate((element: HTMLMediaElement) => element.currentTime)).toBeGreaterThan(0.1)
    if (kind === 'video') expect(await media.evaluate((element: HTMLVideoElement) => element.videoWidth)).toBe(160)
    await media.evaluate((element: HTMLMediaElement) => element.pause())
  }
  await expect(renderer.locator('.asset-media-unavailable')).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
}

for (const format of formats) {
  test(`real ${format.ext} preview, purchased playback, download and refund`, async ({ browser, baseURL }) => {
    test.setTimeout(90_000)
    const seller = await browser.newContext({ baseURL })
    const admin = await browser.newContext({ baseURL })
    const buyer = await browser.newContext({ baseURL })
    const visitor = await browser.newContext({ baseURL })
    try {
      for (const [context, role] of [[seller, 'creator'], [admin, 'admin'], [buyer, 'publisher']] as const) {
        expect((await context.request.post('/api/v1/auth/login', { data: fixtureCredentials(role) })).ok()).toBeTruthy()
      }
      const assets = [] as { id: string; bytes: Buffer }[]
      for (const role of ['original', 'preview']) {
        const bytes = format.ext === 'txt' ? Buffer.from(role === 'original'
          ? 'Original paid text\n' + 'a'.repeat(65536) + '\nEND-OF-PAID-TEXT'
          : 'Preview: Licensed text resource 文本预览。') :
          await readFile(fileURLToPath(new URL(`./fixtures/media/${role}.${format.ext}`, import.meta.url)))
        const response = await seller.request.post('/api/v1/assets/uploads', { headers: { 'Idempotency-Key': randomUUID() }, multipart: {
          title: `Media ${role} ${format.ext}`, file: { name: `${role}.${format.ext}`, mimeType: format.mime, buffer: bytes },
        } })
        expect(response.ok(), await response.text()).toBeTruthy()
        const asset = await response.json()
        expect(asset.kind).toBe(format.kind)
        expect(asset.mimeType).toBe(format.mime)
        assets.push({ id: asset.id, bytes })
        await expect.poll(async () => (await (await seller.request.get(`/api/v1/assets/${asset.id}`)).json()).scanStatus).toBe('clean')
      }
      const original = assets[0]!
      const preview = assets[1]!
      expect(original.bytes.equals(preview.bytes)).toBe(false)
      const listing = await seller.newPage()
      await listing.goto('/workspace/products/new')
      await listing.getByLabel('Product title', { exact: true }).fill(`Licensed ${format.ext} media`)
      await listing.getByLabel('Description', { exact: true }).fill('A private original with a separate public preview.')
      await chooseOption(listing.locator('.seller-product-form select').nth(2), original.id)
      await chooseOption(listing.locator('.seller-product-form select').nth(3), preview.id)
      await listing.getByLabel('Delivered file label', { exact: true }).fill(`original.${format.ext}`)
      await listing.getByLabel('AI disclosure and rights information', { exact: true }).fill('Procedurally generated test media owned by the seller.')
      await listing.getByRole('button', { name: 'Save draft', exact: true }).click()
      await expect(listing).toHaveURL(/\/workspace\/products\/[0-9a-f-]{36}$/)
      const productId = new URL(listing.url()).pathname.split('/').at(-1)!
      await listing.getByRole('checkbox').last().check()
      await listing.getByRole('button', { name: 'Submit for review', exact: true }).click()
      await expect(listing.locator('.ui-card-actions').first()).toContainText('In review')
      const review = await admin.newPage()
      await review.goto(`/admin/products/${productId}`)
      await review.getByLabel('Decision reason (visible to seller)', { exact: true }).fill('Independent original and public sample approved for browser delivery verification.')
      await review.getByRole('button', { name: 'Approve and publish', exact: true }).click()
      await review.getByRole('button', { name: 'Confirm decision', exact: true }).click()
      await expect(review.locator('.ui-card-actions').first()).toContainText('Approved')

      const publicProduct = await (await visitor.request.get(`/api/v1/products/${productId}`)).json()
      const publicBytes = await visitor.request.get(publicProduct.mediaUrl)
      expect(publicBytes.ok()).toBeTruthy()
      expect(await publicBytes.body()).toEqual(preview.bytes)
      expect((await visitor.request.get(`/api/v1/assets/${original.id}/content`)).status()).toBe(403)
      const publicPage = await visitor.newPage()
      await publicPage.goto(`/market/assets/${productId}`)
      await verifyMedia(publicPage, publicPage.locator('.product-detail-media .asset-renderer'), format.kind, preview.bytes.toString())

      await mockExternalCheckout(buyer)
      const purchase = await buyer.newPage()
      await purchase.goto(`/market/assets/${productId}`)
      await purchase.locator('.license-accept').getByRole('checkbox').check()
      const checkoutResponse = purchase.waitForResponse(response => response.url().endsWith(`/products/${productId}/checkout`) && response.request().method() === 'POST')
      const popupPromise = purchase.waitForEvent('popup')
      await purchase.getByRole('button', { name: 'Continue to secure checkout', exact: true }).click()
      const response = await checkoutResponse
      expect(response.ok(), await response.text()).toBeTruthy()
      const checkout = await response.json()
      const popup = await popupPromise
      await popup.getByRole('button', { name: 'Confirm test payment', exact: true }).click()
      const fixture = await paymentFixtureState(buyer.request)
      expect(fixture.lastCheckoutReference).toBe(checkout.paymentId)
      const paymentIntent = `pi_browser_${checkout.paymentId.replaceAll('-', '')}`
      const metadata = { hcai_payment_id: checkout.paymentId, hcai_resource_id: productId, hcai_purpose: 'product' }
      await signedEvent(buyer.request, 'checkout.session.completed', {
        id: fixture.lastCheckoutId, object: 'checkout.session', status: 'complete', payment_status: 'paid',
        amount_total: checkout.amountCents, currency: 'usd', payment_intent: paymentIntent, metadata,
      })
      await popup.goto(`/workspace/orders?payment=success&orderId=${checkout.orderId}&paymentId=${checkout.paymentId}`)
      await expect(popup.locator('.product-payment-status')).toHaveAttribute('data-payment-state', 'fulfilled', { timeout: 30_000 })
      const order = await (await buyer.request.get(`/api/v1/orders/${checkout.orderId}`)).json()
      await popup.locator('.product-payment-primary').click()
      await expect(popup).toHaveURL(new RegExp(`/workspace/assets/${order.assetId}$`))
      await verifyMedia(popup, popup.locator('.asset-detail-media .asset-renderer'), format.kind, original.bytes.toString())
      const downloadPromise = popup.waitForEvent('download')
      await popup.getByRole('link', { name: 'Download licensed file', exact: true }).click()
      const download = await downloadPromise
      expect(await readFile((await download.path())!)).toEqual(original.bytes)
      const contentURL = `/api/v1/assets/${order.assetId}/content`
      const range = await buyer.request.get(contentURL, { headers: { Range: 'bytes=0-15' } })
      expect(range.status()).toBe(206)
      expect(range.headers()['content-range']).toBe(`bytes 0-15/${original.bytes.length}`)
      expect(range.headers()['cache-control']).toContain('no-store')
      expect(await range.body()).toEqual(original.bytes.subarray(0, 16))
      expect((await visitor.request.get(contentURL)).status()).toBe(403)

      await popup.goto('/workspace/orders')
      const row = popup.locator('.order-row').filter({ hasText: checkout.orderId })
      await row.locator('.refund-form textarea').fill('This licensed media does not fit the requirements of my project.')
      await row.getByRole('button', { name: 'Request payment gateway refund', exact: true }).click()
      await expect(row).toHaveAttribute('data-order-status', 'refund_requested')
      expect((await buyer.request.get(contentURL)).ok()).toBeTruthy()
      await expect.poll(async () => (await paymentFixtureState(buyer.request)).lastRefundPaymentIntent, { timeout: 30_000 }).toBe(paymentIntent)
      const refund = await paymentFixtureState(buyer.request)
      await signedEvent(buyer.request, 'refund.updated', { id: refund.lastRefundId, object: 'refund', status: 'succeeded',
        amount: checkout.amountCents, currency: 'usd', payment_intent: paymentIntent,
        metadata: { ...metadata, hcai_refund_operation_id: refund.lastRefundOperationId },
      })
      await expect.poll(async () => (await (await buyer.request.get(`/api/v1/orders/${checkout.orderId}`)).json()).status, { timeout: 30_000 }).toBe('refunded')
      expect((await buyer.request.get(contentURL, { headers: { Range: 'bytes=0-15' } })).status()).toBe(403)
      await popup.goto(`/workspace/assets/${order.assetId}`)
      await expect(popup.getByRole('link', { name: 'Download licensed file', exact: true })).toHaveCount(0)
      if (format.kind === 'document') {
        await expect(popup.locator('.asset-document [role="alert"]')).toHaveText('This media result is unavailable.')
        await expect(popup.locator('.asset-document pre')).toHaveCount(0)
      } else await expect(popup.locator('.asset-media-unavailable')).toBeVisible()
      expect(await (await visitor.request.get(publicProduct.mediaUrl)).body()).toEqual(preview.bytes)
    } finally {
      await seller.close()
      await admin.close()
      await buyer.close()
      await visitor.close()
    }
  })
}
