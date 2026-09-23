const SAFE_ID = /^[A-Za-z0-9_-]{3,255}$/
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
export const CHECKOUT_LOOKUP_CONTRACT_VERSION = 'waffo-product-checkout-lookup-v1'
const MAX_LOOKUP_WINDOW_MS = 24 * 60 * 60 * 1000
const MAX_PAGE_SIZE = 100

export class CheckoutLookupContractError extends Error {
  constructor(code, status = 422) {
    super(code)
    this.name = 'CheckoutLookupContractError'
    this.code = code
    this.status = status
  }
}

function fail(code = 'checkout_lookup_response_invalid', status = 422) {
  throw new CheckoutLookupContractError(code, status)
}

function cents(value) {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 1) return null
  return value
}

function parseTime(value) {
  if (typeof value !== 'string' || !value.trim()) return null
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? null : parsed.toISOString()
}

function inputTime(value) {
  if (typeof value !== 'string' || !value.trim()) return null
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? null : parsed
}

function normalizePayment(row, input, identity) {
  if (!row || typeof row !== 'object') return null
  const providerPaymentId = String(row.providerPaymentId || '').trim()
  const providerCheckoutId = String(row.providerCheckoutId || '').trim()
  const externalId = String(row.orderMerchantExternalId || '').trim()
  const buyerIdentity = String(row.buyerIdentity || '').trim()
  const storeId = String(row.storeId || '').trim()
  const currency = String(row.snapshotAmountDetails?.currency || row.currency || '').trim().toUpperCase()
  const amountCents = cents(row.amountCents)
  const createdAt = parseTime(row.createdAt)
  const expiresAt = parseTime(row.expiresAt)
  const status = String(row.status || '').trim().toLowerCase()
  const paymentStatus = String(row.paymentStatus || status).trim().toLowerCase()
  const createdAtMs = createdAt ? Date.parse(createdAt) : NaN
  const expiresAtMs = expiresAt ? Date.parse(expiresAt) : NaN
  if (!SAFE_ID.test(providerPaymentId) || !SAFE_ID.test(providerCheckoutId) || !UUID.test(externalId) || !UUID.test(buyerIdentity) || storeId !== identity.storeId || externalId !== input.orderMerchantExternalId || buyerIdentity !== input.buyerIdentity || currency !== input.currency || amountCents !== input.amountCents || !createdAt || !expiresAt || !Number.isFinite(createdAtMs) || !Number.isFinite(expiresAtMs) || expiresAtMs < createdAtMs || createdAtMs < input.createdAfter.getTime() || createdAtMs > input.createdBefore.getTime()) return null
  if (!['succeeded', 'paid', 'complete', 'completed'].includes(status) || !['succeeded', 'paid', 'complete', 'completed'].includes(paymentStatus)) return null
  return {
    providerCheckoutId,
    providerPaymentId,
    providerChargeId: '',
    status: 'complete',
    paymentStatus: 'paid',
    amountReceived: amountCents,
    amountCents,
    currency,
    liveMode: identity.liveMode,
    expiresAt,
  }
}

/**
 * Executes a deployment-supplied, schema-reviewed read-only GraphQL query.
 * The query must return `data.payments` rows using the canonical aliases below:
 * providerPaymentId, providerCheckoutId, orderMerchantExternalId,
 * buyerIdentity, storeId, status, paymentStatus, amountCents, currency,
 * createdAt and expiresAt. The response must also expose the exact
 * lookupContractVersion and pageInfo fields. No mutation is accepted.
 */
export async function lookupWaffoCheckout(client, input, identity, options = {}) {
  const createdAfter = inputTime(input?.createdAfter)
  const createdBefore = inputTime(input?.createdBefore)
  if (!client?.graphql?.query || !input || !identity || identity.provider !== 'waffo_pancake' || !UUID.test(input.paymentId) || !UUID.test(input.resourceId) || !UUID.test(input.orderMerchantExternalId) || !UUID.test(input.buyerIdentity) || !UUID.test(input.storeId) || input.storeId !== identity.storeId || !Number.isSafeInteger(input.amountCents) || input.amountCents < 50 || input.currency !== 'USD' || input.liveMode !== identity.liveMode || !createdAfter || !createdBefore || createdBefore <= createdAfter || createdBefore - createdAfter > MAX_LOOKUP_WINDOW_MS) fail('checkout_lookup_request_invalid')
  const query = String(options.query || '').trim()
  if (!query || /\bmutation\b/i.test(query) || query.length > 20000) fail('checkout_lookup_not_configured', 503)
  let cursor = null
  let pages = 0
  let scanned = 0
  const rows = []
  const seenCursors = new Set()
  for (;;) {
    let response
    try {
      response = await client.graphql.query({ query, variables: {
        storeId: identity.storeId,
        orderExternalId: input.orderMerchantExternalId,
        createdAfter: createdAfter.toISOString(),
        createdBefore: createdBefore.toISOString(),
        cursor,
        lookupContractVersion: CHECKOUT_LOOKUP_CONTRACT_VERSION,
      } })
    } catch {
      fail('checkout_lookup_unavailable', 503)
    }
    if (!response || (response.errors != null && (!Array.isArray(response.errors) || response.errors.length > 0))) fail('checkout_lookup_incomplete', 409)
    const data = response.data
    if (!data || data.lookupContractVersion !== CHECKOUT_LOOKUP_CONTRACT_VERSION || !Array.isArray(data.payments)) fail('checkout_lookup_incomplete', 409)
    const pageInfo = data.pageInfo
    if (!pageInfo || typeof pageInfo !== 'object' || typeof pageInfo.hasNextPage !== 'boolean' || (pageInfo.hasNextPage && (typeof pageInfo.endCursor !== 'string' || !pageInfo.endCursor.trim()))) fail('checkout_lookup_incomplete', 409)
    if (data.payments.length > MAX_PAGE_SIZE) fail('checkout_lookup_incomplete', 409)
    pages++
    scanned += data.payments.length
    if (pages > 10 || scanned > 1000) fail('checkout_lookup_incomplete', 409)
    rows.push(...data.payments)
    if (!pageInfo.hasNextPage) break
    if (pages >= 10 || scanned >= 1000) fail('checkout_lookup_incomplete', 409)
    const next = pageInfo.endCursor.trim()
    if (seenCursors.has(next) || next === cursor) fail('checkout_lookup_incomplete', 409)
    seenCursors.add(next)
    cursor = next
  }
  const matches = rows.filter(row => String(row?.orderMerchantExternalId || '').trim() === input.orderMerchantExternalId)
  if (matches.length === 0) return { outcome: 'not_found', pages, scanned, matches: [] }
  if (matches.length > 1) return { outcome: 'ambiguous', pages, scanned, matches: matches.map(row => String(row?.providerCheckoutId || '').trim()).filter(Boolean).slice(0, 2) }
  const normalizedInput = { ...input, createdAfter, createdBefore }
  const observation = normalizePayment(matches[0], normalizedInput, identity)
  if (!observation) return { outcome: 'incomplete', pages, scanned, matches: [] }
  return { outcome: 'found', pages, scanned, matches: [observation.providerCheckoutId], observation }
}
