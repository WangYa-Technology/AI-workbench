import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from './client'
import { setTaskCommandActor } from '../lib/taskCommands'

const fetchMock = vi.fn<typeof fetch>()
let actor: string
beforeEach(() => {
  actor = crypto.randomUUID()
  setTaskCommandActor(actor)
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => { setTaskCommandActor(null); vi.unstubAllGlobals() })
const keyAt = (index: number) => new Headers(fetchMock.mock.calls[index]?.[1]?.headers).get('Idempotency-Key')
const bodyAt = (index: number) => JSON.parse(String(fetchMock.mock.calls[index]?.[1]?.body))

it('freezes settlement and amount before hashing and preserves unknown request keys through reauthentication', async () => {
  fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
  const input = { settlementId: crypto.randomUUID(), amountCents: 1852 }
  const original = { ...input }
  const first = api.createSellerPayoutRequest(input)
  input.settlementId = crypto.randomUUID(); input.amountCents = 9999
  await expect(first).rejects.toThrow('Response lost')
  setTaskCommandActor(null); setTaskCommandActor(actor)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ id: 'original-request' })))
  await expect(api.createSellerPayoutRequest(original)).resolves.toMatchObject({ id: 'original-request' })
  expect(bodyAt(0)).toEqual(original); expect(bodyAt(1)).toEqual(original)
  expect(keyAt(0)).toBeTruthy(); expect(keyAt(1)).toBe(keyAt(0))
})

it('binds equal amount reservations to different settlement keys and isolates actors', async () => {
  fetchMock.mockRejectedValue(new TypeError('Unknown result'))
  const first = { settlementId: crypto.randomUUID(), amountCents: 1852 }
  const second = { ...first, settlementId: crypto.randomUUID() }
  await expect(api.createSellerPayoutRequest(first)).rejects.toThrow()
  await expect(api.createSellerPayoutRequest(second)).rejects.toThrow()
  setTaskCommandActor(crypto.randomUUID())
  await expect(api.createSellerPayoutRequest(first)).rejects.toThrow()
  expect(new Set([keyAt(0), keyAt(1), keyAt(2)]).size).toBe(3)
})

it('does not dispatch a prepared reservation after the command actor changes', async () => {
  const pending = api.createSellerPayoutRequest({ settlementId: crypto.randomUUID(), amountCents: 1852 })
  setTaskCommandActor(crypto.randomUUID())
  await expect(pending).rejects.toThrow('Session changed')
  expect(fetchMock).not.toHaveBeenCalled()
})

it('reads funds through the private session without owner filters or caching', async () => {
  const funds = { sellerId: actor, accounts: [], unresolvedRecords: 0, asOf: '2026-09-22T08:00:00Z' }
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(funds)))
  await expect(api.getSellerFunds()).resolves.toEqual(funds)
  expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/seller/funds')
  expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ credentials: 'include', cache: 'no-store' })
})
