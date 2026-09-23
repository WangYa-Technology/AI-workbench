// Keep the SDK pin and this version in sync with the Go runtime. Changes to
// checkout defaults or serialization require a new request version.
export function checkoutIdentity(merchantId, storeId, environment) {
  return Object.freeze({
    provider: 'waffo_pancake', merchantId, storeId, liveMode: environment === 'prod',
    apiVersion: 'pancake-ts-0.19.1', requestVersion: 'waffo-product-checkout-v1',
  })
}

export function acceptsCheckoutIdentity(input, current) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) return false
  if (String(input.purpose || '').trim().toLowerCase() !== 'product' && !input.checkoutIdentity) return true
  return acceptsPaymentIdentity(input.checkoutIdentity, current)
}

export function acceptsPaymentIdentity(expected, current) {
  return !!expected && !!current && !!current.merchantId && !!current.storeId &&
    Object.entries(current).every(([key, value]) => expected[key] === value)
}

export class CheckoutContractError extends Error {
  constructor(code, status = 422) {
    super(code)
    this.name = 'CheckoutContractError'
    this.code = code
    this.status = status
  }
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const productId = /^[A-Z]{2,5}_[0-9A-Za-z]{22}$/
const purposes = new Set(['product', 'task', 'wallet_topup', 'subscription'])
const productTypes = new Set(['onetime', 'subscription'])

function isPlainObject(value) {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function requiredString(value, max, code = 'checkout_request_invalid') {
  if (typeof value !== 'string' || !value.trim() || value.length > max) throw new CheckoutContractError(code)
  return value.trim()
}

function optionalString(value, max) {
  if (value === undefined) return undefined
  if (value === null || typeof value !== 'string' || value.length > max) throw new CheckoutContractError('checkout_request_invalid')
  const normalized = value.trim()
  return normalized || undefined
}

function nonNilUUID(value) {
  return typeof value === 'string' && uuid.test(value) && !/^0+$/.test(value.replaceAll('-', ''))
}

function validReturnURL(value) {
  if (typeof value !== 'string' || value.length > 2048 || /[\u0000-\u001f\u007f]/.test(value)) return false
  let parsed
  try { parsed = new URL(value) } catch { return false }
  if (parsed.username || parsed.password || parsed.hash || !parsed.hostname) return false
  if (parsed.protocol === 'https:') return true
  return parsed.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]', '::1'].includes(parsed.hostname.toLowerCase())
}

function validEmail(value) {
  return value === undefined || (typeof value === 'string' && value.length <= 320 &&
    (!value.trim() || /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value.trim())))
}

function requiredReturnURL(value) {
  if (typeof value !== 'string' || !value.trim() || !validReturnURL(value.trim())) {
    throw new CheckoutContractError('checkout_request_invalid')
  }
  return value.trim()
}

export function validateCheckoutRequest(input, current, defaults = {}) {
  if (!isPlainObject(input)) throw new CheckoutContractError('checkout_request_invalid')
  for (const key of ['paymentId', 'resourceId']) {
    if (!nonNilUUID(input[key])) throw new CheckoutContractError('checkout_request_invalid')
  }
  if (typeof input.purpose !== 'string' || !purposes.has(input.purpose.trim().toLowerCase())) {
    throw new CheckoutContractError('checkout_request_invalid')
  }
  const purpose = input.purpose.trim().toLowerCase()
  const productType = input.productType === undefined ? 'onetime' :
    requiredString(input.productType, 32).toLowerCase()
  if (!productTypes.has(productType)) throw new CheckoutContractError('checkout_request_invalid')
  const amountCents = input.amountCents === undefined ? null : input.amountCents
  if (!Number.isSafeInteger(amountCents) || amountCents < 50 || amountCents > 99999999) {
    throw new CheckoutContractError('checkout_request_invalid')
  }
  const currency = input.currency === undefined ? 'USD' : requiredString(input.currency, 3).toUpperCase()
  if (currency !== 'USD') throw new CheckoutContractError('checkout_request_invalid')
  const configuredProduct = productType === 'subscription' ? defaults.subscriptionProductId : defaults.onetimeProductId
  const productValue = input.productId === undefined ? configuredProduct : input.productId
  if (productValue === undefined) throw new CheckoutContractError('waffo_product_id_required')
  const resolvedProductId = requiredString(productValue, 128)
  if (!productId.test(resolvedProductId)) throw new CheckoutContractError('checkout_request_invalid')
  const buyerIdentity = input.buyerIdentity === undefined ? input.paymentId : requiredString(input.buyerIdentity, 128)
  if (!validEmail(input.buyerEmail)) throw new CheckoutContractError('checkout_request_invalid')
  const buyerEmail = optionalString(input.buyerEmail, 320)
  const successUrl = requiredReturnURL(input.successUrl)
  const cancelUrl = requiredReturnURL(input.cancelUrl)
  const orderMerchantExternalId = input.orderMerchantExternalId === undefined || input.orderMerchantExternalId === '' ? input.paymentId :
    requiredString(input.orderMerchantExternalId, 128)
  const expiresInSeconds = input.expiresInSeconds === undefined ? 2700 : input.expiresInSeconds
  if (!Number.isSafeInteger(expiresInSeconds) || expiresInSeconds <= 0 || expiresInSeconds > 86400) {
    throw new CheckoutContractError('checkout_request_invalid')
  }
  if (input.checkoutIdentity !== undefined && !isPlainObject(input.checkoutIdentity)) {
    throw new CheckoutContractError('checkout_request_invalid')
  }
  if (!acceptsCheckoutIdentity({ ...input, purpose }, current)) {
    throw new CheckoutContractError('checkout_identity_mismatch', 409)
  }
  return Object.freeze({
    paymentId: input.paymentId,
    resourceId: input.resourceId,
    purpose,
    productType,
    productId: resolvedProductId,
    amountCents,
    currency,
    buyerIdentity,
    buyerEmail,
    successUrl,
    cancelUrl,
    orderMerchantExternalId,
    expiresInSeconds,
    checkoutIdentity: input.checkoutIdentity,
  })
}

export async function createBoundCheckout(client, input, current, defaults = {}) {
  const request = validateCheckoutRequest(input, current, defaults)
  const result = await client.checkout.authenticated.create({
    productId: request.productId,
    currency: request.currency,
    buyerIdentity: request.buyerIdentity,
    buyerEmail: request.buyerEmail,
    successUrl: request.successUrl,
    orderMerchantExternalId: request.orderMerchantExternalId,
    priceSnapshot: { amount: (request.amountCents / 100).toFixed(2), taxCategory: 'digital_goods' },
    metadata: { hcaiPaymentId: request.paymentId, hcaiResourceId: request.resourceId, hcaiPurpose: request.purpose },
    expiresInSeconds: request.expiresInSeconds,
  })
  return { request, result }
}
