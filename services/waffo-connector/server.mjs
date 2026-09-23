import http from 'node:http'
import { Buffer } from 'node:buffer'
import { WaffoPancake } from '@waffo/pancake-ts'
import { checkoutIdentity, createBoundCheckout, CheckoutContractError } from './checkout-contract.mjs'
import { lookupWaffoCheckout, CheckoutLookupContractError } from './checkout-lookup-contract.mjs'
import { createBoundRefund, RefundContractError } from './refund-contract.mjs'
import { waffoFetch, withWaffoRequestCancellation, waffoErrorStatus } from './transport.mjs'
import { verifyWaffoWebhook } from './webhook-contract.mjs'

const configuredEnvironment = (process.env.WAFFO_ENVIRONMENT || 'test').trim().toLowerCase()
if (!['test', 'prod'].includes(configuredEnvironment)) {
  throw new Error('WAFFO_ENVIRONMENT must be test or prod')
}
const env = configuredEnvironment
const merchantId = (process.env.WAFFO_MERCHANT_ID || '').trim()
const storeId = (process.env.WAFFO_STORE_ID || '').trim()
const checkoutLookupQuery = (process.env.WAFFO_CHECKOUT_LOOKUP_QUERY || '').trim()
const identity = checkoutIdentity(merchantId, storeId, env)
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

const client = new WaffoPancake({ merchantId, privateKey, environment: env, fetch: waffoFetch })

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
  if (res.destroyed || res.writableEnded) return
  const payload = JSON.stringify(value)
  res.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'no-store' })
  res.end(payload)
}

const server = http.createServer(withWaffoRequestCancellation(async (req, res) => {
  if (!authorized(req)) return send(res, 401, { error: 'unauthorized' })
  try {
    if (req.method === 'GET' && req.url === '/health') {
      return send(res, 200, { status: 'ready', environment: env })
    }
    if (req.method === 'POST' && req.url === '/checkout/identity') {
      return send(res, 200, identity)
    }
    if (req.method === 'POST' && req.url === '/checkout') {
      let input
      try { input = JSON.parse(await body(req)) } catch { return send(res, 422, { error: 'checkout_request_invalid' }) }
      const { request, result } = await createBoundCheckout(client, input, identity, {
        onetimeProductId: process.env.WAFFO_PRODUCT_ID_ONETIME,
        subscriptionProductId: process.env.WAFFO_PRODUCT_ID_SUBSCRIPTION,
      })
      return send(res, 200, { providerId: result.sessionId, checkoutUrl: result.checkoutUrl, expiresAt: result.expiresAt, liveMode: env === 'prod', paymentStatus: 'pending', status: 'open', checkoutIdentity: request.checkoutIdentity })
    }
    if (req.method === 'POST' && req.url === '/checkout/lookup') {
      let input
      try { input = JSON.parse(await body(req)) } catch { return send(res, 422, { error: 'checkout_lookup_request_invalid' }) }
      return send(res, 200, await lookupWaffoCheckout(client, input, identity, { query: checkoutLookupQuery }))
    }
    if (req.method === 'POST' && req.url === '/refund') {
      const input = JSON.parse(await body(req))
      return send(res, 200, await createBoundRefund(client, input, identity))
    }
    if (req.method === 'POST' && req.url === '/webhook/verify') {
      const raw = await body(req)
      return send(res, 200, verifyWaffoWebhook(raw, req.headers['x-waffo-signature'], env))
    }
    send(res, 404, { error: 'not_found' })
  } catch (error) {
    if (error instanceof CheckoutContractError) return send(res, error.status, { error: error.code })
    if (error instanceof CheckoutLookupContractError) return send(res, error.status, { error: error.code })
    if (error instanceof RefundContractError) return send(res, error.status, { error: error.code })
    send(res, waffoErrorStatus(error), { error: 'waffo_request_failed' })
  }
}))

server.listen(port, host, () => process.stdout.write(`Waffo connector listening on ${host}:${port}\n`))
