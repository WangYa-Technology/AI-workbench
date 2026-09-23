import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, type SellerBankPayoutInput } from './client'
import { setTaskCommandActor } from '../lib/taskCommands'

const fetchMock = vi.fn<typeof fetch>()
let actor: string
const input = (): SellerBankPayoutInput => ({ sourceTransferId: crypto.randomUUID(), reviewId: crypto.randomUUID(), expectedRevision: 1, amountCents: 1852, bankDestinationId: 'ba_original', reason: 'Authorize the original source and frozen bank.', confirmed: true })
const keyAt = (n: number) => new Headers(fetchMock.mock.calls[n]?.[1]?.headers).get('Idempotency-Key')
const bodyAt = (n: number) => JSON.parse(String(fetchMock.mock.calls[n]?.[1]?.body))
beforeEach(() => { actor = crypto.randomUUID(); setTaskCommandActor(actor); fetchMock.mockReset(); vi.stubGlobal('fetch', fetchMock) })
afterEach(() => { setTaskCommandActor(null); vi.unstubAllGlobals() })

it('freezes every confirmed field and recovers the same bank command after reauthentication', async () => {
  fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
  const submitted = input(); const original = { ...submitted }; const id = crypto.randomUUID()
  const first = api.submitSellerBankPayout(id, submitted)
  Object.assign(submitted, { sourceTransferId: crypto.randomUUID(), reviewId: crypto.randomUUID(), expectedRevision: 7, amountCents: 777, bankDestinationId: 'ba_other', reason: 'Changed after dispatch', confirmed: false })
  await expect(first).rejects.toThrow('Response lost')
  setTaskCommandActor(null); setTaskCommandActor(actor)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ replayed: true })))
  await expect(api.submitSellerBankPayout(id, original)).resolves.toMatchObject({ replayed: true })
  expect(bodyAt(0)).toEqual(original); expect(bodyAt(1)).toEqual(original)
  expect(keyAt(0)).toBeTruthy(); expect(keyAt(1)).toBe(keyAt(0))
  expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api/v1/admin/seller-payout-requests/${id}/bank-payout`)
})

it('isolates the operator, request and every confirmed bank field', async () => {
  fetchMock.mockRejectedValue(new TypeError('Unknown result'))
  const original = input(); const id = crypto.randomUUID()
  await expect(api.submitSellerBankPayout(id, original)).rejects.toThrow()
  for (const changed of [
    { sourceTransferId: crypto.randomUUID() }, { reviewId: crypto.randomUUID() }, { expectedRevision: 2 },
    { amountCents: 1853 }, { bankDestinationId: 'ba_changed' }, { reason: 'A different confirmed reason.' },
  ]) await expect(api.submitSellerBankPayout(id, { ...original, ...changed })).rejects.toThrow()
  await expect(api.submitSellerBankPayout(crypto.randomUUID(), original)).rejects.toThrow()
  setTaskCommandActor(crypto.randomUUID())
  await expect(api.submitSellerBankPayout(id, original)).rejects.toThrow()
  expect(fetchMock).toHaveBeenCalledTimes(9)
  expect(new Set(Array.from({ length: 9 }, (_, n) => keyAt(n))).size).toBe(9)
})

it('does not dispatch a bank command prepared under a previous actor', async () => {
  const prepared = api.submitSellerBankPayout(crypto.randomUUID(), input())
  setTaskCommandActor(crypto.randomUUID())
  await expect(prepared).rejects.toThrow('Session changed')
  expect(fetchMock).not.toHaveBeenCalled()
})

it('discards a late success without forgetting the original operator retry key', async () => {
  let finish!: (response: Response) => void
  fetchMock.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const id = crypto.randomUUID(); const original = input()
  const first = api.submitSellerBankPayout(id, original)
  await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce())
  setTaskCommandActor(crypto.randomUUID())
  finish(new Response(JSON.stringify({ privateResult: 'old-operator' })))
  await expect(first).rejects.toThrow('Session changed')
  setTaskCommandActor(actor)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ replayed: true })))
  await expect(api.submitSellerBankPayout(id, original)).resolves.toMatchObject({ replayed: true })
  expect(keyAt(1)).toBe(keyAt(0)); expect(bodyAt(1)).toEqual(original)
})

it('reads bank operation through the private session without caching or a write key', async () => {
  const id = crypto.randomUUID(); const operation = { request: { id }, canSubmit: false }
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(operation)))
  await expect(api.getSellerBankPayoutOperation(id)).resolves.toEqual(operation)
  expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api/v1/admin/seller-payout-requests/${id}/bank-payout`)
  expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ credentials: 'include', cache: 'no-store' })
  expect(keyAt(0)).toBeNull()
})

it('retains a separate immutable continuation key across a lost response and actor change', async () => {
  const id = crypto.randomUUID()
  const submitted = { commandId: crypto.randomUUID(), expectedJobId: crypto.randomUUID(), reason: 'Continue the verified stopped original task.', confirmed: true as const }
  const original = { ...submitted }
  fetchMock.mockRejectedValue(new TypeError('Resume response lost'))
  const first = api.resumeSellerBankPayout(id, submitted)
  submitted.expectedJobId = crypto.randomUUID(); submitted.reason = 'Changed after dispatch'
  await expect(first).rejects.toThrow('Resume response lost')
  setTaskCommandActor(null); setTaskCommandActor(actor)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ replayed: true })))
  await expect(api.resumeSellerBankPayout(id, original)).resolves.toMatchObject({ replayed: true })
  expect(bodyAt(0)).toEqual(original); expect(bodyAt(1)).toEqual(original)
  expect(keyAt(0)).toBeTruthy(); expect(keyAt(1)).toBe(keyAt(0))
  expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api/v1/admin/seller-payout-requests/${id}/bank-payout/resume`)
  await expect(api.resumeSellerBankPayout(id, { ...original, expectedJobId: crypto.randomUUID() })).rejects.toThrow()
  expect(keyAt(2)).not.toBe(keyAt(0))
  setTaskCommandActor(crypto.randomUUID())
  await expect(api.resumeSellerBankPayout(id, original)).rejects.toThrow()
  expect(keyAt(3)).not.toBe(keyAt(0))
})
