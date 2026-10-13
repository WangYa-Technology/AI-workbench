import type { Order } from '../api/client'

// Apply the same identity/version boundary to polling, list reads and commands.
export function mergeOrder(current: Order | null | undefined, incoming: Order): Order {
  if (!current) return incoming
  if (current.id !== incoming.id || current.paymentId !== incoming.paymentId) return current
  if (current.paymentVersion !== undefined && (!Number.isSafeInteger(incoming.paymentVersion)
    || (incoming.paymentVersion ?? 0) < current.paymentVersion)) return current
  // Historical orders have no payment version; a recorded refund is terminal.
  if (current.paymentVersion === undefined && ['refunded', 'test_refunded'].includes(current.status)
    && incoming.status !== current.status) return current
  return incoming
}

export function orderNeedsPolling(order: Order) {
  return ['payment_pending', 'payment_paid', 'refund_requested'].includes(order.status)
    && order.paymentStatus !== 'refund_failed'
}

export function refundFeedback(order: Order): { key: string; error: boolean } {
  if (order.status === 'test_refunded' && order.paymentMode === 'test') return { key: 'refundComplete', error: false }
  if (order.status === 'refunded') return { key: 'providerRefundComplete', error: false }
  if (order.paymentStatus === 'refund_failed') return { key: 'refundFailedFeedback', error: true }
  if (order.status === 'refund_requested') return { key: 'refundPending', error: false }
  return { key: 'refundUnconfirmed', error: true }
}
