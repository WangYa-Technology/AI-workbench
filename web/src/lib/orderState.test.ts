import { describe, expect, it } from 'vitest'
import type { Order } from '../api/client'
import { mergeOrder, orderNeedsPolling, refundFeedback } from './orderState'

const order: Order = {
  id: 'order-1', productId: 'product-1', productTitle: 'Frozen product', paymentId: 'payment-1',
  status: 'fulfilled', paymentStatus: 'paid', paymentVersion: 4, amountCents: 1900, currency: 'USD',
  licenseCode: 'commercial', licenseName: 'Commercial', licenseVersion: '1', licenseTerms: 'Terms',
  refundWindowDays: 7, canRequestRefund: true, canCloseCheckout: false, checkoutClosedBeforePayment: false,
  paymentMode: 'stripe', realCharge: false, createdAt: '2026-09-20T10:00:00Z', events: [],
}
describe('order snapshots', () => {
  it('rejects stale or missing versions across refund, closure and refresh', () => {
    const latest = { ...order, status: 'refunded' as const, paymentVersion: 6 }
    for (const next of [order, { ...order, paymentVersion: undefined }, { ...order, paymentVersion: NaN }]) {
      expect(mergeOrder(latest, next)).toBe(latest)
    }
    expect(mergeOrder(order, latest)).toBe(latest)
  })
  it('never merges a different order or payment identity', () => {
    for (const patch of [{ id: 'other' }, { paymentId: 'other' }, { paymentId: undefined }]) {
      expect(mergeOrder(order, { ...order, ...patch, paymentVersion: 9 })).toBe(order)
    }
  })
  it('allows historical refunds but does not restore unversioned refunded orders', () => {
    const legacy = { ...order, paymentId: undefined, paymentVersion: undefined, paymentMode: 'test' as const }
    const refunded = { ...legacy, status: 'test_refunded' as const }
    expect(mergeOrder(legacy, refunded)).toBe(refunded)
    expect(mergeOrder(refunded, legacy)).toBe(refunded)
  })
  it('accepts same-version capability changes without inventing state ranks', () => {
    const next = { ...order, canRequestRefund: false }
    expect(mergeOrder(order, next)).toBe(next)
  })
})
describe('refund feedback and follow-up', () => {
  it.each([
    ['refund_requested', 'refund_pending', 'stripe', 'refundPending', false],
    ['refunded', 'refunded', 'stripe', 'providerRefundComplete', false],
    ['test_refunded', undefined, 'test', 'refundComplete', false],
    ['fulfilled', 'refund_failed', 'stripe', 'refundFailedFeedback', true],
    ['fulfilled', 'paid', 'stripe', 'refundUnconfirmed', true],
    ['payment_pending', 'checkout_pending', 'stripe', 'refundUnconfirmed', true],
  ] as const)('%s/%s/%s has truthful feedback', (status, paymentStatus, paymentMode, key, error) => {
    expect(refundFeedback({ ...order, status, paymentStatus, paymentMode })).toEqual({ key, error })
  })
  it('polls a pending refund but stops on completion and failure', () => {
    expect(orderNeedsPolling({ ...order, status: 'refund_requested', paymentStatus: 'refund_pending' })).toBe(true)
    expect(orderNeedsPolling({ ...order, status: 'refund_requested', paymentStatus: 'refund_failed' })).toBe(false)
    expect(orderNeedsPolling({ ...order, status: 'refunded' })).toBe(false)
  })
})
