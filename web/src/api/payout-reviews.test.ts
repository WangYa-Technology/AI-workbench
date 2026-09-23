import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, type SellerPayoutReviewInput } from './client'
import { setTaskCommandActor } from '../lib/taskCommands'

const fetchMock = vi.fn<typeof fetch>()
let actor: string
const input = (): SellerPayoutReviewInput => ({ expectedRevision: 0, settlementId: crypto.randomUUID(), amountCents: 1852, bankDestinationId: 'ba_original', decision: 'approved', reason: 'Verified original settlement and bank.', sellerMessage: 'Your reservation was reviewed. No bank payout was sent.' })
const keyAt = (n: number) => new Headers(fetchMock.mock.calls[n]?.[1]?.headers).get('Idempotency-Key')
const bodyAt = (n: number) => JSON.parse(String(fetchMock.mock.calls[n]?.[1]?.body))
beforeEach(() => { actor = crypto.randomUUID(); setTaskCommandActor(actor); fetchMock.mockReset(); vi.stubGlobal('fetch', fetchMock) })
afterEach(() => { setTaskCommandActor(null); vi.unstubAllGlobals() })

it('freezes every review field and recovers the same key after reauthentication', async () => {
  fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
  const submitted = input(); const original = { ...submitted }; const id = crypto.randomUUID()
  const first = api.reviewSellerPayout(id, submitted)
  Object.assign(submitted, { expectedRevision: 7, settlementId: crypto.randomUUID(), amountCents: 777, bankDestinationId: 'ba_other', decision: 'rejected', reason: 'Changed after dispatch', sellerMessage: 'Altered public explanation' })
  await expect(first).rejects.toThrow('Response lost')
  setTaskCommandActor(null); setTaskCommandActor(actor)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ replayed: true })))
  await expect(api.reviewSellerPayout(id, original)).resolves.toMatchObject({ replayed: true })
  expect(bodyAt(0)).toEqual(original); expect(bodyAt(1)).toEqual(original)
  expect(keyAt(0)).toBeTruthy(); expect(keyAt(1)).toBe(keyAt(0))
})

it('isolates request, actor, revision and decision in retry keys', async () => {
  fetchMock.mockRejectedValue(new TypeError('Unknown result'))
  const original = input(); const id = crypto.randomUUID()
  await expect(api.reviewSellerPayout(id, original)).rejects.toThrow()
  await expect(api.reviewSellerPayout(crypto.randomUUID(), original)).rejects.toThrow()
  await expect(api.reviewSellerPayout(id, { ...original, expectedRevision: 1 })).rejects.toThrow()
  await expect(api.reviewSellerPayout(id, { ...original, decision: 'rejected' })).rejects.toThrow()
  setTaskCommandActor(crypto.randomUUID())
  await expect(api.reviewSellerPayout(id, original)).rejects.toThrow()
  expect(new Set(Array.from({ length: 5 }, (_, n) => keyAt(n))).size).toBe(5)
})

it('does not dispatch a review prepared under a previous actor', async () => {
  const prepared = api.reviewSellerPayout(crypto.randomUUID(), input())
  setTaskCommandActor(crypto.randomUUID())
  await expect(prepared).rejects.toThrow('Session changed')
  expect(fetchMock).not.toHaveBeenCalled()
})

const fundingInput = () => ({ reviewId: crypto.randomUUID(), expectedRevision: 1, settlementId: crypto.randomUUID(), amountCents: 1852, bankDestinationId: 'ba_original', reason: 'Authorize the reviewed original source.', confirmed: true as const })

it('freezes funding approval, amount and bank before hashing and retries the same command after reauthentication', async () => {
  fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
  const submitted = fundingInput(); const original = { ...submitted }; const id = crypto.randomUUID()
  const first = api.admitSellerPayoutFunding(id, submitted)
  Object.assign(submitted, { reviewId: crypto.randomUUID(), expectedRevision: 9, settlementId: crypto.randomUUID(), amountCents: 777, bankDestinationId: 'ba_other', reason: 'Edited while dispatching', confirmed: false })
  await expect(first).rejects.toThrow('Response lost')
  setTaskCommandActor(null); setTaskCommandActor(actor)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ replayed: true })))
  await expect(api.admitSellerPayoutFunding(id, original)).resolves.toMatchObject({ replayed: true })
  expect(bodyAt(0)).toEqual(original); expect(bodyAt(1)).toEqual(original)
  expect(keyAt(0)).toBeTruthy(); expect(keyAt(1)).toBe(keyAt(0))
  expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api/v1/admin/seller-payout-requests/${id}/funding`)
})

it('isolates funding retries by operator, request and every confirmed field', async () => {
  fetchMock.mockRejectedValue(new TypeError('Unknown result'))
  const original = fundingInput(); const id = crypto.randomUUID()
  await expect(api.admitSellerPayoutFunding(id, original)).rejects.toThrow()
  for (const changed of [
    { reviewId: crypto.randomUUID() }, { expectedRevision: 2 }, { settlementId: crypto.randomUUID() },
    { amountCents: 1853 }, { bankDestinationId: 'ba_changed' }, { reason: 'A different confirmed reason.' },
  ]) await expect(api.admitSellerPayoutFunding(id, { ...original, ...changed })).rejects.toThrow()
  await expect(api.admitSellerPayoutFunding(crypto.randomUUID(), original)).rejects.toThrow()
  setTaskCommandActor(crypto.randomUUID())
  await expect(api.admitSellerPayoutFunding(id, original)).rejects.toThrow()
  expect(fetchMock).toHaveBeenCalledTimes(9)
  expect(new Set(Array.from({ length: 9 }, (_, n) => keyAt(n))).size).toBe(9)
})

it('never sends funding prepared before a session transition', async () => {
  const prepared = api.admitSellerPayoutFunding(crypto.randomUUID(), fundingInput())
  setTaskCommandActor(crypto.randomUUID())
  await expect(prepared).rejects.toThrow('Session changed')
  expect(fetchMock).not.toHaveBeenCalled()
})
