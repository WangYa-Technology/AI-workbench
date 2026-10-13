import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, type ProductPaymentDisputeCommand } from './client'
import { setTaskCommandActor } from '../lib/taskCommands'
import { maskDisputeReference } from '../lib/productDisputes'

const fetchMock = vi.fn<typeof fetch>()
let actor: string
const command = (): ProductPaymentDisputeCommand => ({ action: 'route', route: 'finance', expectedVersion: 1, reason: 'Verified original provider evidence.', confirmed: true })
const keyAt = (n: number) => new Headers(fetchMock.mock.calls[n]?.[1]?.headers).get('Idempotency-Key')
const bodyAt = (n: number) => JSON.parse(String(fetchMock.mock.calls[n]?.[1]?.body))
beforeEach(() => { actor = crypto.randomUUID(); setTaskCommandActor(actor); fetchMock.mockReset(); vi.stubGlobal('fetch', fetchMock) })
afterEach(() => { setTaskCommandActor(null); vi.unstubAllGlobals() })

it('uses private reads, encoded paths and every directory filter', async () => {
  fetchMock.mockImplementation(async () => new Response('{}'))
  await api.listProductPaymentDisputes({ q: 'a & b', actionStatus: 'needs_response', reviewStatus: 'new', binding: 'unbound', mode: 'test', cursor: 'opaque+/', limit: 20 })
  const url = new URL(String(fetchMock.mock.calls[0][0]), 'https://example.test')
  expect(Object.fromEntries(url.searchParams)).toEqual({ q: 'a & b', actionStatus: 'needs_response', reviewStatus: 'new', binding: 'unbound', mode: 'test', cursor: 'opaque+/', limit: '20' })
  expect(url.pathname).toBe('/api/v1/admin/product-disputes')
  await api.getProductPaymentDispute('id/with?reserved')
  expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/admin/product-disputes/id%2Fwith%3Freserved')
  for (const [, init] of fetchMock.mock.calls) expect(init).toMatchObject({ cache: 'no-store', credentials: 'include' })
})

it.each(['network', '500', '403'])('retains the frozen command/key after %s and reauthentication', async failure => {
  if (failure === 'network') fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
  else fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'failed', message: 'Unconfirmed' } }), { status: Number(failure) }))
  const submitted = command(), original = { ...submitted }, id = crypto.randomUUID()
  const first = api.operateProductPaymentDispute(id, submitted)
  Object.assign(submitted, { action: 'escalate_recovery', route: 'collections', expectedVersion: 5, reason: 'Changed while hashing' })
  await expect(first).rejects.toThrow()
  setTaskCommandActor(null); setTaskCommandActor(actor)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ replayed: true, version: 7 })))
  await expect(api.operateProductPaymentDispute(id, original)).resolves.toMatchObject({ replayed: true, version: 7 })
  expect(bodyAt(0)).toEqual(original); expect(bodyAt(1)).toEqual(original)
  expect(keyAt(0)).toBeTruthy(); expect(keyAt(1)).toBe(keyAt(0))
  expect(fetchMock.mock.calls[0][0]).toBe(`/api/v1/admin/product-disputes/${id}/operations`)
})

it('isolates dispute, actor and all operation fields in retry identity', async () => {
  fetchMock.mockRejectedValue(new TypeError('Unknown result'))
  const id = crypto.randomUUID(), original = command()
  await expect(api.operateProductPaymentDispute(id, original)).rejects.toThrow()
  for (const changed of [{ action: 'request_evidence' as const }, { route: 'seller_support' as const }, { expectedVersion: 2 }, { reason: 'Another decision reason.' }, { evidenceReference: 'file_new_receipt' }]) {
    await expect(api.operateProductPaymentDispute(id, { ...original, ...changed })).rejects.toThrow()
  }
  await expect(api.operateProductPaymentDispute(crypto.randomUUID(), original)).rejects.toThrow()
  setTaskCommandActor(crypto.randomUUID())
  await expect(api.operateProductPaymentDispute(id, original)).rejects.toThrow()
  expect(new Set(fetchMock.mock.calls.map((_, i) => keyAt(i))).size).toBe(8)
})

it('does not dispatch an operation prepared before an actor change', async () => {
  const prepared = api.operateProductPaymentDispute(crypto.randomUUID(), command())
  setTaskCommandActor(crypto.randomUUID())
  await expect(prepared).rejects.toThrow('Session changed')
  expect(fetchMock).not.toHaveBeenCalled()
})

it.each(['https://user:pass@example.test/path?token=secret#fragment', 'provider://stripe/file_private', 'file_private#secret', 'person@example.test'])('never exposes or links URL credentials or private reference contents: %s', value => {
  expect(maskDisputeReference(value)).toBe('••••')
})
it('keeps at most a short opaque receipt suffix', () => {
  expect(maskDisputeReference('file_private_1234')).toBe('••••1234')
  expect(maskDisputeReference('abc')).toBe('••••')
  expect(maskDisputeReference()).toBe('')
})
