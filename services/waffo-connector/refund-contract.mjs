import { acceptsPaymentIdentity } from './checkout-contract.mjs'

export const refundContractVersion = 'waffo-product-refund-v1'
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const nonNilUUID = value => typeof value === 'string' && uuid.test(value) && value !== '00000000-0000-0000-0000-000000000000'
const providerId = /^[A-Za-z0-9_-]{6,255}$/
const ticketStatuses = new Set(['pending', 'under_review', 'approved', 'rejected', 'returned', 'processing', 'succeeded', 'failed', 'cancelled'])

export class RefundContractError extends Error {
  constructor(code, status) { super(code); this.code = code; this.status = status }
}

function amountCents(value) {
  if (typeof value !== 'string' || !/^(0|[1-9]\d{0,5})(\.\d{1,2})?$/.test(value)) return null
  const [whole, fraction = ''] = value.split('.')
  const cents = Number(whole) * 100 + Number(fraction.padEnd(2, '0'))
  return cents > 0 && cents <= 99999999 ? cents : null
}

export function validateRefundRequest(input, current) {
  if (!input || input.refundContractVersion !== refundContractVersion ||
      !acceptsPaymentIdentity(input.paymentIdentity, current) || input.storeId !== current.storeId) {
    throw new RefundContractError('refund_contract_mismatch', 409)
  }
  if (!nonNilUUID(input.paymentId) || !nonNilUUID(input.operationId) ||
      typeof input.providerPaymentId !== 'string' || !providerId.test(input.providerPaymentId) || !Number.isSafeInteger(input.amountCents) ||
      input.amountCents < 1 || input.amountCents > 99999999 || input.currency !== 'USD' ||
      typeof input.buyerIdentity !== 'string' || !input.buyerIdentity.trim() || input.buyerIdentity.length > 128) {
    throw new RefundContractError('refund_request_invalid', 422)
  }
}

// The SDK returns a ticket, not a confirmed movement of funds. Only bind fields
// actually returned by Waffo; reflecting the submitted amount proves nothing.
export function normalizeRefundTicket(input, result) {
  const ticket = result?.ticket
  const amount = ticket?.versionData?.requestedAmount
  const cents = amountCents(amount?.amount)
  if (!ticket || typeof ticket.id !== 'string' || !providerId.test(ticket.id) || ticket.type !== 'refund' ||
      ticket.subjectId !== input.providerPaymentId || ticket.refundTicketMerchantExternalId !== input.operationId ||
      ticket.metadata?.hcaiPaymentId !== input.paymentId || !ticketStatuses.has(ticket.status) ||
      cents !== input.amountCents || amount?.currency !== input.currency) {
    throw new RefundContractError('refund_response_unverified', 409)
  }
  return {
    providerId: ticket.id, providerPaymentId: ticket.subjectId,
    operationId: ticket.refundTicketMerchantExternalId,
    amountCents: cents, currency: amount.currency, status: ticket.status,
    paymentIdentity: input.paymentIdentity, refundContractVersion,
  }
}

export async function createBoundRefund(client, input, current) {
  validateRefundRequest(input, current)
  const session = await client.auth.issueSessionToken({ storeId: input.paymentIdentity.storeId, buyerIdentity: input.buyerIdentity })
  const result = await client.customer(session.token).createRefundTicket({
    paymentId: input.providerPaymentId,
    reason: String(input.reason || 'Requested by customer'),
    requestedAmount: { amount: (input.amountCents / 100).toFixed(2), currency: input.currency },
    refundTicketMerchantExternalId: input.operationId,
    metadata: { hcaiPaymentId: input.paymentId },
  })
  return normalizeRefundTicket(input, result)
}
