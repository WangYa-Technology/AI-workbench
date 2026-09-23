import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash, createSign, generateKeyPairSync } from 'node:crypto'
import { verifyWaffoWebhook, webhookContractVersion } from './webhook-contract.mjs'

test('SDK signature verification binds exact raw bytes and the configured environment', () => {
  const { privateKey, publicKey } = generateKeyPairSync('rsa', {
    modulusLength: 2048, privateKeyEncoding: { type: 'pkcs8', format: 'pem' }, publicKeyEncoding: { type: 'spki', format: 'pem' },
  })
  const previousTestKey = process.env.WAFFO_WEBHOOK_TEST_PUBLIC_KEY
  const previousProdKey = process.env.WAFFO_WEBHOOK_PROD_PUBLIC_KEY
  process.env.WAFFO_WEBHOOK_TEST_PUBLIC_KEY = publicKey
  // A separate valid key makes the environment mismatch deterministic without
  // relying on production keys, external credentials or network requests.
  process.env.WAFFO_WEBHOOK_PROD_PUBLIC_KEY = generateKeyPairSync('rsa', {
    modulusLength: 2048, publicKeyEncoding: { type: 'spki', format: 'pem' }, privateKeyEncoding: { type: 'pkcs8', format: 'pem' },
  }).publicKey
  const payload = JSON.stringify({ id: 'delivery_fixture', mode: 'test', data: { buyerEmail: 'private@example.test' } })
  const header = (raw, timestamp = Date.now()) => {
    const signature = createSign('RSA-SHA256').update(`${timestamp}.${raw}`).sign(privateKey, 'base64')
    return `t=${timestamp},v1=${signature}`
  }
  try {
    const signed = header(payload)
    assert.deepEqual(verifyWaffoWebhook(payload, signed, 'test'), { verification: {
      contractVersion: webhookContractVersion, environment: 'test', payloadSHA256: createHash('sha256').update(payload).digest('hex'),
    } })
    assert.throws(() => verifyWaffoWebhook(payload + ' ', signed, 'test'))
    assert.throws(() => verifyWaffoWebhook(payload, signed, 'prod'))
    assert.throws(() => verifyWaffoWebhook(payload, header(payload, Date.now() - 3600_000), 'test'))
    assert.throws(() => verifyWaffoWebhook(payload, '', 'test'))
    assert.throws(() => verifyWaffoWebhook(payload, signed, 'invalid'))
    const malformed = '{broken'
    assert.throws(() => verifyWaffoWebhook(malformed, header(malformed), 'test'))
  } finally {
    if (previousTestKey === undefined) delete process.env.WAFFO_WEBHOOK_TEST_PUBLIC_KEY
    else process.env.WAFFO_WEBHOOK_TEST_PUBLIC_KEY = previousTestKey
    if (previousProdKey === undefined) delete process.env.WAFFO_WEBHOOK_PROD_PUBLIC_KEY
    else process.env.WAFFO_WEBHOOK_PROD_PUBLIC_KEY = previousProdKey
  }
})
