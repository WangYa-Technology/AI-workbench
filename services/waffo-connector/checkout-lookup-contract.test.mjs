import test from 'node:test'
import assert from 'node:assert/strict'
import { lookupWaffoCheckout, CheckoutLookupContractError } from './checkout-lookup-contract.mjs'

const paymentId = '11111111-1111-4111-8111-111111111111'
const resourceId = '22222222-2222-4222-8222-222222222222'
const orderExternalId = '33333333-3333-4333-8333-333333333333'
const buyerIdentity = '44444444-4444-4444-8444-444444444444'
const storeId = '55555555-5555-4555-8555-555555555555'
const identity = { provider: 'waffo_pancake', merchantId: 'MER_test', storeId, liveMode: false }
const input = { paymentId, resourceId, orderMerchantExternalId: orderExternalId, buyerIdentity, storeId, amountCents: 1250, currency: 'USD', liveMode: false, createdAfter: '2026-09-22T09:55:00.000Z', createdBefore: '2026-09-22T10:05:00.000Z' }
const query = 'query ($storeId: String!, $orderExternalId: String!, $createdAfter: DateTime!, $createdBefore: DateTime!, $cursor: String, $lookupContractVersion: String!) { payments(filter: { orderMerchantExternalId: { eq: $orderExternalId } }) { providerPaymentId providerCheckoutId orderMerchantExternalId buyerIdentity storeId status paymentStatus amountCents currency createdAt expiresAt } pageInfo { hasNextPage endCursor } lookupContractVersion }'

function row(overrides = {}) {
  return { providerPaymentId: 'PAY_test_123', providerCheckoutId: 'ORD_test_123', orderMerchantExternalId: orderExternalId, buyerIdentity, storeId, status: 'succeeded', paymentStatus: 'succeeded', amountCents: 1250, currency: 'USD', createdAt: '2026-09-22T10:00:00.000Z', expiresAt: '2026-09-22T11:00:00.000Z', ...overrides }
}

function response(payments, pageInfo = { hasNextPage: false, endCursor: null }, version = 'waffo-product-checkout-lookup-v1') {
  return { data: { lookupContractVersion: version, payments, pageInfo }, errors: [] }
}

test('normalizes one exactly bound paid order without executing a mutation', async () => {
  let request
  const client = { graphql: { query: async value => { request = value; return response([row()]) } } }
  const result = await lookupWaffoCheckout(client, input, identity, { query })
  assert.equal(result.outcome, 'found')
  assert.deepEqual(result.matches, ['ORD_test_123'])
  assert.equal(result.observation.providerPaymentId, 'PAY_test_123')
  assert.equal(result.observation.amountCents, 1250)
  assert.equal(request.variables.storeId, storeId)
  assert.equal(request.variables.orderExternalId, orderExternalId)
  assert.equal(request.variables.createdAfter, '2026-09-22T09:55:00.000Z')
  assert.equal(request.variables.createdBefore, '2026-09-22T10:05:00.000Z')
  assert.equal(request.variables.lookupContractVersion, 'waffo-product-checkout-lookup-v1')
  assert.equal(request.variables.cursor, null)
  assert.doesNotMatch(request.query, /\bmutation\b/i)
})

test('ambiguous, incomplete and not found results fail closed', async () => {
  const client = payments => ({ graphql: { query: async () => response(payments) } })
  assert.equal((await lookupWaffoCheckout(client([row(), row({ providerPaymentId: 'PAY_test_456', providerCheckoutId: 'ORD_test_456' })]), input, identity, { query })).outcome, 'ambiguous')
  assert.equal((await lookupWaffoCheckout(client([]), input, identity, { query })).outcome, 'not_found')
  assert.equal((await lookupWaffoCheckout(client([row({ buyerIdentity: '55555555-5555-4555-8555-555555555555' })]), input, identity, { query })).outcome, 'incomplete')
})

test('missing schema-reviewed query and mutations are rejected', async () => {
  const client = { graphql: { query: async () => ({}) } }
  await assert.rejects(lookupWaffoCheckout(client, input, identity), error => error instanceof CheckoutLookupContractError && error.code === 'checkout_lookup_not_configured' && error.status === 503)
  await assert.rejects(lookupWaffoCheckout(client, input, identity, { query: 'mutation { deleteEverything }' }), error => error instanceof CheckoutLookupContractError && error.code === 'checkout_lookup_not_configured')
})

test('identity, amount and store mismatches become incomplete instead of a payment match', async () => {
  const client = { graphql: { query: async () => response([row({ storeId: '66666666-6666-4666-8666-666666666666', amountCents: 9900 })]) } }
  const result = await lookupWaffoCheckout(client, input, identity, { query })
  assert.equal(result.outcome, 'incomplete')
})

test('requires the exact response contract and complete page info', async () => {
  const invalid = [
    response([row()], { hasNextPage: false, endCursor: null }, 'wrong-version'),
    { data: { lookupContractVersion: 'waffo-product-checkout-lookup-v1', payments: [row()] }, errors: [] },
    response([row()], { hasNextPage: true, endCursor: null }),
  ]
  for (const payload of invalid) {
    const client = { graphql: { query: async () => payload } }
    await assert.rejects(lookupWaffoCheckout(client, input, identity, { query }), error => error.code === 'checkout_lookup_incomplete' && error.status === 409)
  }
})

test('follows bounded pagination and forwards the cursor', async () => {
  const requests = []
  const client = { graphql: { query: async value => {
    requests.push(value)
    if (requests.length === 1) return response([row({ providerCheckoutId: 'ORD_other', orderMerchantExternalId: '66666666-6666-4666-8666-666666666666' })], { hasNextPage: true, endCursor: 'cursor-1' })
    return response([row()])
  } } }
  const result = await lookupWaffoCheckout(client, input, identity, { query })
  assert.equal(result.outcome, 'found')
  assert.equal(result.pages, 2)
  assert.equal(result.scanned, 2)
  assert.equal(requests[1].variables.cursor, 'cursor-1')
})

test('rejects repeated cursors and unbounded result sets', async () => {
  const repeated = { graphql: { query: async () => response([], { hasNextPage: true, endCursor: 'same' }) } }
  await assert.rejects(lookupWaffoCheckout(repeated, input, identity, { query }), error => error.code === 'checkout_lookup_incomplete')

  const oversized = { graphql: { query: async () => response(Array.from({ length: 101 }, () => row())) } }
  await assert.rejects(lookupWaffoCheckout(oversized, input, identity, { query }), error => error.code === 'checkout_lookup_incomplete')

  let calls = 0
  const tenPages = { graphql: { query: async () => {
    calls++
    return response(Array.from({ length: 100 }, () => row({ providerCheckoutId: `ORD_${calls}` })), { hasNextPage: true, endCursor: `cursor-${calls}` })
  } } }
  await assert.rejects(lookupWaffoCheckout(tenPages, input, identity, { query }), error => error.code === 'checkout_lookup_incomplete')
  assert.equal(calls, 10)
})

test('requires explicit cents and valid timestamps inside the lookup window', async () => {
  for (const overrides of [
    { amountCents: undefined, snapshotAmountDetails: { total: '12.50', currency: 'USD' } },
    { expiresAt: undefined },
    { createdAt: '2026-09-22T11:00:00.000Z' },
  ]) {
    const client = { graphql: { query: async () => response([row(overrides)]) } }
    assert.equal((await lookupWaffoCheckout(client, input, identity, { query })).outcome, 'incomplete')
  }
  await assert.rejects(lookupWaffoCheckout({ graphql: { query: async () => response([]) } }, { ...input, createdBefore: '2026-09-24T10:00:00.000Z' }, identity, { query }), error => error.code === 'checkout_lookup_request_invalid')
})
