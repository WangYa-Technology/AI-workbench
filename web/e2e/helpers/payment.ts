import { createHmac, randomUUID } from 'node:crypto'
import { expect, type APIRequestContext, type BrowserContext } from '@playwright/test'

export async function paymentFixtureState(request: APIRequestContext) {
  const response = await request.get(`http://127.0.0.1:${process.env.E2E_FIXTURE_PORT ?? '18084'}/__fixture/state`)
  expect(response.ok()).toBeTruthy()
  return await response.json() as {
    checkoutCalls: number; refundCalls: number; lastCheckoutId: string; lastRefundId: string
    lastCheckoutReference: string; lastRefundPaymentIntent: string; lastRefundOperationId: string
  }
}

export async function mockExternalCheckout(context: BrowserContext) {
  await context.route('https://checkout.stripe.com/**', route => route.fulfill({
    contentType: 'text/html', body: '<!doctype html><title>Test checkout</title><h1>Simulated payment provider</h1><button onclick="this.textContent=\'Confirmed\'">Confirm test payment</button>',
  }))
}

export async function signedEvent(request: APIRequestContext, type: string, object: object) {
  const timestamp = Math.floor(Date.now() / 1000)
  const body = JSON.stringify({ id: `evt_browser_${randomUUID().replaceAll('-', '')}`, object: 'event',
    api_version: '2026-02-25.clover', created: timestamp, livemode: false, type, data: { object } })
  const signature = createHmac('sha256', 'whsec_payment_drill_secret').update(`${timestamp}.${body}`).digest('hex')
  for (let delivery = 0; delivery < 2; delivery++) {
    const response = await request.post('/api/v1/payments/webhooks/stripe', {
      data: body, headers: { 'Content-Type': 'application/json', 'Stripe-Signature': `t=${timestamp},v1=${signature}` },
    })
    expect(response.ok(), await response.text()).toBeTruthy()
  }
}
