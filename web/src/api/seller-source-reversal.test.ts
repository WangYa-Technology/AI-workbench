import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, type SellerSourceReversalInput, type SellerSourceClosureInput } from './client'
import { setTaskCommandActor } from '../lib/taskCommands'

const fetchMock = vi.fn<typeof fetch>()
let actor: string
const keyAt = (n: number) => new Headers(fetchMock.mock.calls[n]?.[1]?.headers).get('Idempotency-Key')
const bodyAt = (n: number) => JSON.parse(String(fetchMock.mock.calls[n]?.[1]?.body))
beforeEach(() => { actor = crypto.randomUUID(); setTaskCommandActor(actor); fetchMock.mockReset(); vi.stubGlobal('fetch', fetchMock) })
afterEach(() => { setTaskCommandActor(null); vi.unstubAllGlobals() })

for (const kind of ['return', 'close'] as const) {
  const input = (): SellerSourceReversalInput | SellerSourceClosureInput => kind === 'return'
    ? { sourceTransferId: crypto.randomUUID(), expectedUpdatedAt: '2026-09-23T00:00:00Z', bankCommandId: null, bankResultId: null, reason: 'Return the original source after bank verification.', confirmed: true }
    : { readId: crypto.randomUUID(), expectedUpdatedAt: '2026-09-23T00:00:00Z', reason: 'Consume the accepted full return after verification.', confirmed: true }
  const submit = (id: string, body: SellerSourceReversalInput | SellerSourceClosureInput) => kind === 'return'
    ? api.submitSellerSourceReversal(id, body as SellerSourceReversalInput)
    : api.closeSellerSourceReversal(id, body as SellerSourceClosureInput)

  it(`${kind}: freezes confirmed fields and preserves the retry key across reauthentication`, async () => {
    const id = crypto.randomUUID(), body = input(), original = { ...body }
    fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
    const first = submit(id, body)
    body.reason = 'Mutated after confirmation'; body.expectedUpdatedAt = '2030-01-01T00:00:00Z'
    await expect(first).rejects.toThrow('Response lost')
    setTaskCommandActor(null); setTaskCommandActor(actor)
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ replayed: true })))
    await expect(submit(id, original)).resolves.toMatchObject({ replayed: true })
    expect(bodyAt(0)).toEqual(original); expect(bodyAt(1)).toEqual(original)
    expect(keyAt(0)).toBeTruthy(); expect(keyAt(1)).toBe(keyAt(0))
    expect(fetchMock.mock.calls[0]?.[0]).toBe(kind === 'return'
      ? `/api/v1/admin/seller-payout-requests/${id}/source-reversal`
      : `/api/v1/admin/seller-source-reversals/${id}/close`)
  })

  it(`${kind}: does not send a command after the preparing actor changes`, async () => {
    const first = submit(crypto.randomUUID(), input())
    setTaskCommandActor(crypto.randomUUID())
    await expect(first).rejects.toThrow('Session changed')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it(`${kind}: discards late private success while retaining the original retry identity`, async () => {
    let finish!: (response: Response) => void
    fetchMock.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const id = crypto.randomUUID(), body = input()
    const first = submit(id, body)
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce())
    setTaskCommandActor(crypto.randomUUID())
    finish(new Response(JSON.stringify({ privateResult: 'old finance actor' })))
    await expect(first).rejects.toThrow('Session changed')
    setTaskCommandActor(actor)
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ replayed: true })))
    await expect(submit(id, body)).resolves.toMatchObject({ replayed: true })
    expect(keyAt(1)).toBe(keyAt(0))
  })

  it(`${kind}: isolates every changed confirmation and finance actor`, async () => {
    const id = crypto.randomUUID(), original = input()
    fetchMock.mockRejectedValue(new TypeError('Unknown outcome'))
    await expect(submit(id, original)).rejects.toThrow()
    const changes = kind === 'return'
      ? [{ sourceTransferId: crypto.randomUUID() }, { bankCommandId: crypto.randomUUID() }, { bankResultId: crypto.randomUUID() }]
      : [{ readId: crypto.randomUUID() }]
    for (const change of [...changes, { expectedUpdatedAt: '2026-09-23T01:00:00Z' }, { reason: 'Different explicitly confirmed reason.' }]) {
      await expect(submit(id, { ...original, ...change })).rejects.toThrow()
    }
    await expect(submit(crypto.randomUUID(), original)).rejects.toThrow()
    setTaskCommandActor(crypto.randomUUID())
    await expect(submit(id, original)).rejects.toThrow()
    const count = fetchMock.mock.calls.length
    expect(new Set(Array.from({ length: count }, (_, n) => keyAt(n))).size).toBe(count)
  })
}

it('reads source return evidence without a write key or response caching', async () => {
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ canSubmit: false, canClose: false })))
  await api.getSellerSourceReversalOperation(crypto.randomUUID())
  expect(fetchMock.mock.calls[0]?.[1]?.cache).toBe('no-store')
  expect(fetchMock.mock.calls[0]?.[1]?.method || 'GET').toBe('GET')
  expect(keyAt(0)).toBeNull()
})
