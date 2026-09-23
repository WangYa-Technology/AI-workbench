import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from './client'
import { setTaskCommandActor } from '../lib/taskCommands'

const fetchMock = vi.fn<typeof fetch>()
beforeEach(() => {
  setTaskCommandActor(null)
  setTaskCommandActor(crypto.randomUUID())
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => vi.unstubAllGlobals())
const keys = () => fetchMock.mock.calls.map(([, init]) => new Headers(init?.headers).get('Idempotency-Key'))

it('keeps an uncertain adjustment key, and issues a fresh key only after acknowledged success', async () => {
  const input = { deltaCents: 500, currency: 'USD' as const }
  fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
  await expect(api.adminAdjustFinance('target-account', input)).rejects.toThrow('Response lost')
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ operationId: 'original', replayed: true })))
  await expect(api.adminAdjustFinance('target-account', input)).resolves.toMatchObject({ operationId: 'original', replayed: true })
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ operationId: 'next', replayed: false })))
  await api.adminAdjustFinance('target-account', input)
  expect(keys()[0]).toBeTruthy()
  expect(keys()[1]).toBe(keys()[0])
  expect(keys()[2]).not.toBe(keys()[0])
})

it('snapshots the amount before hashing and keeps different targets, amounts and operators separate', async () => {
  fetchMock.mockRejectedValue(new TypeError('Response lost'))
  const input = { deltaCents: 500, currency: 'USD' as const }
  const first = api.adminAdjustFinance('target-account', input)
  input.deltaCents = 501
  await expect(first).rejects.toThrow()
  await expect(api.adminAdjustFinance('target-account', input)).rejects.toThrow()
  await expect(api.adminAdjustFinance('other-account', input)).rejects.toThrow()
  setTaskCommandActor('other-operator')
  await expect(api.adminAdjustFinance('target-account', input)).rejects.toThrow()
  expect(fetchMock.mock.calls[0]?.[1]?.body).toBe(JSON.stringify({ deltaCents: 500, currency: 'USD' }))
  expect(new Set(keys()).size).toBe(4)
})

it('coalesces simultaneous duplicate adjustments into one HTTP request', async () => {
  let release!: (response: Response) => void
  fetchMock.mockImplementation(() => new Promise(resolve => { release = resolve }))
  const input = { deltaCents: -50, currency: 'USD' as const }
  const first = api.adminAdjustFinance('target-account', input)
  const second = api.adminAdjustFinance('target-account', input)
  await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
  release(new Response(JSON.stringify({ operationId: 'one', replayed: false })))
  expect(await first).toEqual(await second)
  expect(fetchMock).toHaveBeenCalledTimes(1)
})

it('keeps a pending monetary key after a denied replay and same-account reauthentication', async () => {
  const actor = crypto.randomUUID()
  setTaskCommandActor(actor)
  const input = { deltaCents: 500, currency: 'USD' as const }
  fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
  await expect(api.adminAdjustFinance('target-account', input)).rejects.toThrow()
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'permission_required', message: 'Denied', retryable: false } }), { status: 403 }))
  await expect(api.adminAdjustFinance('target-account', input)).rejects.toMatchObject({ status: 403 })
  setTaskCommandActor(null)
  setTaskCommandActor(actor)
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ operationId: 'original', replayed: true })))
  await api.adminAdjustFinance('target-account', input)
  expect(keys()[0]).toBeTruthy()
  expect(keys()[1]).toBe(keys()[0])
  expect(keys()[2]).toBe(keys()[0])
})

for (const order of ['old-response-first', 'retry-fails-first']) {
  it(`does not lose the monetary key when an old login response arrives: ${order}`, async () => {
    const actor = crypto.randomUUID()
    setTaskCommandActor(actor)
    const storage = new Map<string, string>()
    vi.stubGlobal('sessionStorage', {
      get length() { return storage.size },
      key: (index: number) => [...storage.keys()][index] ?? null,
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
    })
    const input = { deltaCents: 500, currency: 'USD' as const }
    const operations = new Map<string, string>()
    let balanceChange = 0
    let releaseOld!: (response: Response) => void
    let failRetry!: (error: Error) => void
    let requests = 0
    fetchMock.mockImplementation(async (_url, init) => {
      const key = new Headers(init?.headers).get('Idempotency-Key')!
      const replayed = operations.has(key)
      if (!replayed) { operations.set(key, crypto.randomUUID()); balanceChange += input.deltaCents }
      const result = { operationId: operations.get(key), replayed }
      requests++
      if (requests === 1) return new Promise<Response>(resolve => { releaseOld = resolve })
      if (requests === 2) return new Promise<Response>((_resolve, reject) => { failRetry = reject })
      return new Response(JSON.stringify(result))
    })
    const first = api.adminAdjustFinance('target-account', input).then(value => ({ value }), error => ({ error }))
    await vi.waitFor(() => expect(requests).toBe(1))
    setTaskCommandActor(null)
    setTaskCommandActor(actor)
    const retry = api.adminAdjustFinance('target-account', input).catch(error => error)
    await vi.waitFor(() => expect(requests).toBe(2))
    const oldResponse = new Response(JSON.stringify({ operationId: operations.get(keys()[0]!), replayed: false }))
    if (order === 'old-response-first') {
      releaseOld(oldResponse)
      await first
      failRetry(new TypeError('Current retry response lost'))
    } else {
      failRetry(new TypeError('Current retry response lost'))
      await retry
      releaseOld(oldResponse)
    }
    const [oldResult] = await Promise.all([first, retry])
    expect([...storage.values()]).toEqual([keys()[0]])
    const recovered = await api.adminAdjustFinance('target-account', input)
    expect(new Set(keys()).size).toBe(1)
    expect(balanceChange).toBe(500)
    expect(recovered.operationId).toBe(operations.get(keys()[0]!))
    expect(recovered.replayed).toBe(true)
    expect(oldResult).toHaveProperty('error')
    expect(storage.size).toBe(0)
  })
}
