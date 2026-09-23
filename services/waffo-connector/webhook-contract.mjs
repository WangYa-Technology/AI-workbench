import { createHash } from 'node:crypto'
import { verifyWebhook } from '@waffo/pancake-ts'

export const webhookContractVersion = 'waffo-webhook-v1'

// Attest to the exact bytes and environment verified by the pinned SDK. The Go
// receiver parses those same bytes; it never trusts a replacement event object.
export function verifyWaffoWebhook(raw, signature, environment) {
  if (!['test', 'prod'].includes(environment)) throw new Error('Invalid verification environment')
  verifyWebhook(raw, signature, { environment })
  return { verification: {
    contractVersion: webhookContractVersion,
    environment,
    payloadSHA256: createHash('sha256').update(raw).digest('hex'),
  } }
}
