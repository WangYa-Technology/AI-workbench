import test from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync } from 'node:crypto'
import { WaffoPancake } from '@waffo/pancake-ts'
import { acceptsCheckoutIdentity, acceptsPaymentIdentity, checkoutIdentity, createBoundCheckout, validateCheckoutRequest } from './checkout-contract.mjs'

const currentIdentity = checkoutIdentity('MER_AAAAAAAAAAAAAAAAAAAAAA', 'STO_BBBBBBBBBBBBBBBBBBBBBB', 'test')

function validCheckoutInput() {
  return {
    paymentId: '11111111-1111-4111-8111-111111111111',
    resourceId: '22222222-2222-4222-8222-222222222222',
    purpose: 'product',
    amountCents: 1250,
    currency: 'USD',
    productId: 'PROD_' + 'A'.repeat(22),
    productType: 'onetime',
    buyerIdentity: 'buyer-fixture',
    buyerEmail: 'buyer@example.test',
    successUrl: 'https://app.example.test/success',
    cancelUrl: 'https://app.example.test/cancel',
    orderMerchantExternalId: 'order-fixture',
    expiresInSeconds: 2700,
    checkoutIdentity: { ...currentIdentity, endpoint: 'https://connector.example.test' },
  }
}

test('pinned SDK changes checkout idempotency keys across minute boundaries even for the same order', async t => {
  const { privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048, privateKeyEncoding: { type: 'pkcs8', format: 'pem' }, publicKeyEncoding: { type: 'spki', format: 'pem' } })
  const calls = []
  const client = new WaffoPancake({ merchantId: 'MER_AAAAAAAAAAAAAAAAAAAAAA', privateKey, environment: 'test', fetch: async (url, init) => {
    calls.push({ url, body: init.body, key: init.headers['X-Idempotency-Key'] })
    return Response.json({ data: url.endsWith('/create-session')
      ? { sessionId: 'CHK_fixture', checkoutUrl: 'https://checkout.example.test/fixture', expiresAt: '2030-01-01T00:00:00Z' }
      : { token: 'fixture', expiresAt: '2030-01-01T00:00:00Z' } })
  } })
  let now = 1800000059000
  t.mock.method(Date, 'now', () => now)
  const request = { productId: 'PROD_BBBBBBBBBBBBBBBBBBBBBB', buyerIdentity: 'buyer-fixture', currency: 'USD',
    orderMerchantExternalId: 'original-order', priceSnapshot: { amount: '19.00', taxCategory: 'digital_goods' } }
  await client.checkout.authenticated.create(request)
  now += 1000
  await client.checkout.authenticated.create(request)
  for (const endpoint of ['/create-session', '/issue-session-token']) {
    const requests = calls.filter(c => c.url.endsWith(endpoint))
    assert.equal(requests.length, 2)
    assert.equal(requests[0].body, requests[1].body)
    assert.ok(requests[0].key)
    assert.notEqual(requests[0].key, requests[1].key)
  }
})

test('product checkout requires the original merchant and serializer at dispatch', () => {
  const current = checkoutIdentity('MER_original', 'STO_original', 'test')
  const request = { purpose: 'product', checkoutIdentity: { ...current, endpoint: 'https://connector.example.test' } }
  assert.equal(acceptsCheckoutIdentity(request, current), true)
  assert.equal(acceptsCheckoutIdentity({ purpose: 'product' }, current), false)
  assert.equal(acceptsCheckoutIdentity({ purpose: ' Product ' }, current), false)
  for (const key of Object.keys(current)) {
    const changed = { ...current, [key]: key === 'liveMode' ? true : `${current[key]}-changed` }
    assert.equal(acceptsCheckoutIdentity(request, changed), false, key)
    const missing = { ...request.checkoutIdentity }
    delete missing[key]
    assert.equal(acceptsCheckoutIdentity({ ...request, checkoutIdentity: missing }, current), false, `missing ${key}`)
  }
  assert.equal(acceptsCheckoutIdentity(request, checkoutIdentity('MER_original', '', 'test')), false)
  assert.equal(acceptsCheckoutIdentity({ purpose: 'subscription' }, current), true)
})

test('refund dispatch cannot use a connector that changed after identity lookup', () => {
  const original = checkoutIdentity('MER_original', 'STO_original', 'test')
  assert.equal(acceptsPaymentIdentity(original, original), true)
  assert.equal(acceptsPaymentIdentity(undefined, original), false)
  assert.equal(acceptsPaymentIdentity(original, checkoutIdentity('MER_other', 'STO_original', 'test')), false)
  assert.equal(acceptsPaymentIdentity(original, checkoutIdentity('MER_original', 'STO_other', 'test')), false)
  assert.equal(acceptsPaymentIdentity(original, checkoutIdentity('MER_original', 'STO_original', 'prod')), false)
})

test('checkout validation rejects malformed values before the SDK can be called', async () => {
  const mutations = [
    input => { input.amountCents = '1250' },
    input => { input.amountCents = NaN },
    input => { input.amountCents = Infinity },
    input => { input.productType = 'unknown' },
    input => { input.productId = null },
    input => { input.paymentId = {} },
    input => { input.resourceId = '00000000-0000-0000-0000-000000000000' },
    input => { input.currency = 'EUR' },
    input => { input.buyerIdentity = {} },
    input => { input.buyerEmail = 'not-an-email' },
    input => { input.successUrl = 'http://payments.example.test/return' },
    input => { input.orderMerchantExternalId = 'x'.repeat(129) },
    input => { input.expiresInSeconds = null },
    input => { input.checkoutIdentity = [] },
  ]
  let calls = 0
  const client = { checkout: { authenticated: { create: async () => { calls++; throw new Error('SDK must not be called') } } } }
  for (const mutate of mutations) {
    const input = validCheckoutInput()
    mutate(input)
    await assert.rejects(createBoundCheckout(client, input, currentIdentity), e => e.code === 'checkout_request_invalid')
  }
  assert.equal(calls, 0)
})

test('checkout validation keeps explicit defaults and identity conflicts distinguishable', async () => {
  const input = validCheckoutInput()
  delete input.productId
  delete input.productType
  delete input.expiresInSeconds
  delete input.currency
  const normalized = validateCheckoutRequest(input, currentIdentity, { onetimeProductId: 'PROD_' + 'B'.repeat(22) })
  assert.equal(normalized.productId, 'PROD_' + 'B'.repeat(22))
  assert.equal(normalized.productType, 'onetime')
  assert.equal(normalized.currency, 'USD')
  assert.equal(normalized.expiresInSeconds, 2700)

  const conflict = validCheckoutInput()
  conflict.checkoutIdentity.merchantId = 'MER_CHANGED'
  await assert.rejects(createBoundCheckout({ checkout: { authenticated: { create: async () => { throw new Error('SDK must not be called') } } } }, conflict, currentIdentity), e => e.code === 'checkout_identity_mismatch' && e.status === 409)
})
