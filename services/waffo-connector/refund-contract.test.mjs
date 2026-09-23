import test from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync } from 'node:crypto'
import { WaffoPancake } from '@waffo/pancake-ts'
import { checkoutIdentity } from './checkout-contract.mjs'
import { createBoundRefund, normalizeRefundTicket, refundContractVersion } from './refund-contract.mjs'

const identity = checkoutIdentity('MER_AAAAAAAAAAAAAAAAAAAAAA', 'STO_BBBBBBBBBBBBBBBBBBBBBB', 'test')
function request() {
  return { refundContractVersion, paymentIdentity: { ...identity, endpoint: 'http://127.0.0.1:8091' },
    storeId: identity.storeId, buyerIdentity: 'buyer-original',
    paymentId: '11111111-1111-4111-8111-111111111111', operationId: '22222222-2222-4222-8222-222222222222',
    providerPaymentId: 'PAY_CCCCCCCCCCCCCCCCCCCCCC', amountCents: 1250, currency: 'USD', reason: 'Wrong deliverable' }
}
function ticket(input = request()) {
  return { ticket: { id: 'RFT_original', type: 'refund', subjectId: input.providerPaymentId,
    refundTicketMerchantExternalId: input.operationId, metadata: { hcaiPaymentId: input.paymentId },
    versionData: { requestedAmount: { amount: '12.50', currency: 'USD' } }, status: 'pending',
    privateReviewerNotes: 'must not leak', buyerEmail: 'private@example.test' } }
}

test('reject invalid requests and changed identity before any SDK call', async () => {
  const mutations = [
    r => delete r.refundContractVersion, r => { r.refundContractVersion = 'old' },
    r => { r.paymentIdentity.merchantId = 'MER_other' }, r => { r.paymentIdentity.storeId = 'STO_other' },
    r => { r.paymentIdentity.liveMode = true }, r => { r.paymentIdentity.requestVersion = 'old' },
    r => { r.storeId = 'STO_other' }, r => { r.buyerIdentity = ' ' }, r => { r.buyerIdentity = 'x'.repeat(129) },
    r => { r.operationId = '00000000-0000-0000-0000-000000000000' },
    r => { r.paymentId = [r.paymentId] }, r => { r.providerPaymentId = [r.providerPaymentId] },
    r => { r.providerPaymentId = 'PAY/unsafe' }, r => { r.amountCents = 0 }, r => { r.amountCents = 1.5 },
    r => { r.amountCents = 100000000 }, r => { r.amountCents = '1250' }, r => { r.currency = 'EUR' },
  ]
  let calls = 0
  const client = { auth: { issueSessionToken: () => { calls++; throw new Error('should not call') } } }
  for (const mutate of mutations) {
    const input = request(); mutate(input)
    await assert.rejects(createBoundRefund(client, input, identity), e => ['refund_request_invalid', 'refund_contract_mismatch'].includes(e.code))
  }
  assert.equal(calls, 0)
})

test('upstream fields must independently bind the refund to the exact operation', () => {
  const mutations = [
    r => { r.ticket.subjectId = 'PAY_other' }, r => { r.ticket.refundTicketMerchantExternalId = request().paymentId },
    r => { r.ticket.metadata.hcaiPaymentId = request().operationId }, r => delete r.ticket.metadata,
    r => { r.ticket.type = 'dispute' }, r => { r.ticket.status = 'invented' }, r => { r.ticket.id = 'unsafe/id' },
    r => { r.ticket.versionData.requestedAmount.amount = '12.51' },
    r => { r.ticket.versionData.requestedAmount.currency = 'EUR' }, r => delete r.ticket.versionData,
    r => { r.ticket.versionData.requestedAmount = null }, r => { r.ticket = null },
  ]
  for (const mutate of mutations) {
    const result = ticket(); mutate(result)
    assert.throws(() => normalizeRefundTicket(request(), result), e => e.code === 'refund_response_unverified')
  }
  for (const amount of ['1.25e1', ' 12.50', '12.500', 12.5, '-12.50', '012.50', 'NaN', '1000000', '0']) {
    const result = ticket(); result.ticket.versionData.requestedAmount.amount = amount
    assert.throws(() => normalizeRefundTicket(request(), result), e => e.code === 'refund_response_unverified')
  }
})

test('exact cents and allowed ticket states; no invented financial confirmation or private fields', () => {
  for (const [amount, cents] of [['0.01', 1], ['1', 100], ['12.5', 1250], ['999999.99', 99999999]]) {
    const input = { ...request(), amountCents: cents }, result = ticket(input)
    result.ticket.versionData.requestedAmount.amount = amount
    assert.equal(normalizeRefundTicket(input, result).amountCents, cents)
  }
  for (const status of ['pending', 'under_review', 'approved', 'rejected', 'returned', 'processing', 'succeeded', 'failed', 'cancelled']) {
    const result = ticket(); result.ticket.status = status
    const normalized = normalizeRefundTicket(request(), result)
    assert.deepEqual(Object.keys(normalized).sort(), ['providerId', 'providerPaymentId', 'operationId', 'amountCents', 'currency', 'status', 'paymentIdentity', 'refundContractVersion'].sort())
    assert.equal(normalized.status, status)
    assert.equal(normalized.operationId, request().operationId)
  }
})

test('pinned SDK sends one ticket with original buyer, amount and correlation; no automatic retry', async () => {
  const { privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048, privateKeyEncoding: { type: 'pkcs8', format: 'pem' }, publicKeyEncoding: { type: 'spki', format: 'pem' } })
  const calls = []
  let failTicket = false
  const client = new WaffoPancake({ merchantId: identity.merchantId, privateKey, environment: 'test', fetch: async (url, init) => {
    calls.push({ url, init, payload: JSON.parse(init.body) })
    if (url.includes('session')) return Response.json({ data: { token: 'session-fixture', expiresAt: new Date().toISOString() } })
    if (failTicket) throw new Error('response lost')
    return Response.json({ data: ticket() })
  } })
  const result = await createBoundRefund(client, request(), identity)
  assert.equal(result.providerId, 'RFT_original')
  assert.equal(calls.length, 2)
  assert.equal(calls[0].payload.storeId, identity.storeId)
  assert.equal(calls[0].payload.buyerIdentity, request().buyerIdentity)
  assert.equal(calls[1].payload.paymentId, request().providerPaymentId)
  assert.equal(calls[1].payload.refundTicketMerchantExternalId, request().operationId)
  assert.deepEqual(calls[1].payload.requestedAmount, { amount: '12.50', currency: 'USD' })
  assert.equal(calls[1].init.headers.Authorization, 'Bearer session-fixture')
  failTicket = true
  await assert.rejects(createBoundRefund(client, request(), identity), /response lost/)
  assert.equal(calls.length, 4)
})
