import http from 'node:http'
import { Buffer } from 'node:buffer'
import { WaffoPancake, verifyWebhook } from '@waffo/pancake-ts'

const configuredEnvironment = (process.env.WAFFO_ENVIRONMENT || 'test').trim().toLowerCase()
if (!['test', 'prod'].includes(configuredEnvironment)) {
  throw new Error('WAFFO_ENVIRONMENT must be test or prod')
}
const env = configuredEnvironment
const merchantId = (process.env.WAFFO_MERCHANT_ID || '').trim()
const privateKey = process.env.WAFFO_PRIVATE_KEY ||
  (process.env.WAFFO_PRIVATE_KEY_BASE64 ? Buffer.from(process.env.WAFFO_PRIVATE_KEY_BASE64, 'base64').toString('utf8') : '')
const token = (process.env.WAFFO_CONNECTOR_TOKEN || '').trim()
const addr = process.env.WAFFO_CONNECTOR_ADDR || '127.0.0.1:8091'
const [host, portText] = addr.includes(':') ? addr.lastIndexOf(':') > 0 ? [addr.slice(0, addr.lastIndexOf(':')), addr.slice(addr.lastIndexOf(':') + 1)] : ['127.0.0.1', addr] : ['127.0.0.1', addr]
const port = Number(portText) || 8091

if (!merchantId || !privateKey || !token) {
  throw new Error('WAFFO_MERCHANT_ID, WAFFO_PRIVATE_KEY(_BASE64), and WAFFO_CONNECTOR_TOKEN are required')
}
if (token.length < 16) {
  throw new Error('WAFFO_CONNECTOR_TOKEN must contain at least 16 characters')
}

const client = new WaffoPancake({ merchantId, privateKey, environment: env })

function authorized(req) {
  return req.headers.authorization === `Bearer ${token}`
}

async function body(req) {
  const chunks = []
  let size = 0
  for await (const chunk of req) {
    size += chunk.length
    if (size > 1024 * 1024) throw new Error('request too large')
    chunks.push(chunk)
  }
  return Buffer.concat(chunks).toString('utf8')
}

function send(res, status, value) {
  const payload = JSON.stringify(value)
  res.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'no-store' })
  res.end(payload)
}

function errorStatus(error) {
  if (String(error?.message || '').toLowerCase().includes('signature') || String(error?.message || '').toLowerCase().includes('timestamp')) return 401
  const status = Number(error?.status)
  if (status === 401 || status === 403) return status
  return status >= 400 && status < 500 ? 422 : 502
}

const server = http.createServer(async (req, res) => {
  if (!authorized(req)) return send(res, 401, { error: 'unauthorized' })
  try {
    if (req.method === 'GET' && req.url === '/health') {
      return send(res, 200, { status: 'ready', environment: env })
    }
    if (req.method === 'POST' && req.url === '/checkout') {
      const input = JSON.parse(await body(req))
      const productType = input.productType === 'subscription' ? 'subscription' : 'onetime'
      const productId = input.productId || (productType === 'subscription' ? process.env.WAFFO_PRODUCT_ID_SUBSCRIPTION : process.env.WAFFO_PRODUCT_ID_ONETIME)
      if (!productId) return send(res, 422, { error: 'waffo_product_id_required' })
      const result = await client.checkout.authenticated.create({
        productId,
        currency: String(input.currency || 'USD').toUpperCase(),
        buyerIdentity: String(input.buyerIdentity || input.paymentId),
        buyerEmail: input.buyerEmail || undefined,
        successUrl: input.successUrl || undefined,
        orderMerchantExternalId: String(input.orderMerchantExternalId || input.paymentId),
        priceSnapshot: {
          amount: (Number(input.amountCents) / 100).toFixed(2),
          taxCategory: 'digital_goods',
        },
        metadata: {
          hcaiPaymentId: String(input.paymentId),
          hcaiResourceId: String(input.resourceId),
          hcaiPurpose: String(input.purpose),
        },
        expiresInSeconds: Number(input.expiresInSeconds || 2700),
      })
      return send(res, 200, { providerId: result.sessionId, checkoutUrl: result.checkoutUrl, expiresAt: result.expiresAt, liveMode: env === 'prod', paymentStatus: 'pending', status: 'open' })
    }
    if (req.method === 'POST' && req.url === '/refund') {
      const input = JSON.parse(await body(req))
      if (!input.providerPaymentId) return send(res, 422, { error: 'waffo_payment_id_required' })
      const session = await client.auth.issueSessionToken({
        storeId: input.storeId || process.env.WAFFO_STORE_ID,
        buyerIdentity: String(input.buyerIdentity || input.paymentId),
      })
      const customer = client.customer(session.token)
      const result = await customer.createRefundTicket({
        paymentId: String(input.providerPaymentId),
        reason: String(input.reason || 'Requested by customer'),
        requestedAmount: { amount: (Number(input.amountCents) / 100).toFixed(2), currency: String(input.currency || 'USD').toUpperCase() },
        refundTicketMerchantExternalId: String(input.operationId || input.paymentId),
        metadata: { hcaiPaymentId: String(input.paymentId) },
      })
      return send(res, 200, { providerId: result.ticket.id, providerPaymentId: String(input.providerPaymentId), amountCents: Number(input.amountCents), currency: String(input.currency || 'USD').toUpperCase(), status: result.ticket.status })
    }
    if (req.method === 'POST' && req.url === '/webhook/verify') {
      const raw = await body(req)
      const event = verifyWebhook(raw, req.headers['x-waffo-signature'], { environment: env })
      return send(res, 200, { event })
    }
    send(res, 404, { error: 'not_found' })
  } catch (error) {
    send(res, errorStatus(error), { error: 'waffo_request_failed' })
  }
})

server.listen(port, host, () => process.stdout.write(`Waffo connector listening on ${host}:${port}\n`))
