import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const fetchMock = vi.fn<typeof fetch>()
const storage = new Map<string, string>()

beforeEach(() => {
  vi.resetModules()
  storage.clear()
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  vi.stubGlobal('sessionStorage', {
    get length() { return storage.size },
    key: (index: number) => [...storage.keys()][index] ?? null,
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
  })
})
afterEach(() => vi.unstubAllGlobals())

const keys = () => fetchMock.mock.calls.map(([, init]) => new Headers(init?.headers).get('Idempotency-Key'))
const checkout = () => new Response(JSON.stringify({ paymentId: 'original-payment', checkoutUrl: 'https://checkout.example.test/original' }))

for (const kind of ['topup', 'subscription'] as const) {
  async function client(actor = 'buyer') {
    const commands = await import('../lib/taskCommands')
    commands.setTaskCommandActor(actor)
    const { api } = await import('./client')
    const send = (key?: string) => kind === 'topup' ? api.checkoutWalletTopup(2000, key) : api.checkoutSubscription('plan-one', key)
    return { ...commands, send }
  }

  it(`${kind}: recovers the original checkout after response loss, reload and denied reauthentication`, async () => {
    const first = await client()
    fetchMock.mockRejectedValueOnce(new TypeError('Response lost'))
    await expect(first.send()).rejects.toThrow('Response lost')
    expect(storage.size).toBe(1)
    // A new module graph discards in-memory keys, as a page reload would.
    vi.resetModules()
    const reloaded = await client()
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'permission_required', message: 'Denied', retryable: false } }), { status: 403 }))
    await expect(reloaded.send()).rejects.toMatchObject({ status: 403 })
    reloaded.setTaskCommandActor(null)
    reloaded.setTaskCommandActor('buyer')
    fetchMock.mockResolvedValueOnce(checkout())
    await expect(reloaded.send()).resolves.toMatchObject({ paymentId: 'original-payment' })
    expect(keys()[0]).toBeTruthy()
    expect(new Set(keys()).size).toBe(1)
    expect(storage.size).toBe(0)
    fetchMock.mockResolvedValueOnce(checkout())
    await reloaded.send()
    expect(keys()[3]).not.toBe(keys()[0])
  })

  it(`${kind}: coalesces concurrent clicks and keeps explicitly supplied keys`, async () => {
    const { send } = await client()
    let release!: (response: Response) => void
    fetchMock.mockImplementationOnce(() => new Promise(resolve => { release = resolve }))
    const first = send(), second = send()
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    release(checkout())
    expect(await first).toEqual(await second)
    fetchMock.mockResolvedValueOnce(checkout())
    await send('caller-owned-key')
    expect(keys()[1]).toBe('caller-owned-key')
  })

  it(`${kind}: prevents checkout dispatch during an identity transition`, async () => {
    const { send, beginCommandSessionTransition } = await client()
    const finish = beginCommandSessionTransition()
    try {
      await expect(send()).rejects.toThrow('Session changed')
      await expect(send('explicit-key')).rejects.toThrow('Session changed')
      expect(fetchMock).not.toHaveBeenCalled()
    } finally { finish() }
  })

  for (const responseOrder of ['old-first', 'retry-first']) {
    it(`${kind}: retains the pending original key when a stale response arrives ${responseOrder}`, async () => {
      const { send, setTaskCommandActor } = await client()
      let releaseOld!: (response: Response) => void
      let rejectRetry!: (error: Error) => void
      fetchMock.mockImplementationOnce(() => new Promise(resolve => { releaseOld = resolve }))
      const first = send().catch(error => error)
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
      setTaskCommandActor(null)
      setTaskCommandActor('buyer')
      fetchMock.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectRetry = reject }))
      const retry = send().catch(error => error)
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
      if (responseOrder === 'old-first') {
        releaseOld(checkout())
        await first
        rejectRetry(new TypeError('Retry response lost'))
      } else {
        rejectRetry(new TypeError('Retry response lost'))
        await retry
        releaseOld(checkout())
      }
      expect(await first).toMatchObject({ message: 'Session changed while preparing the request.' })
      await retry
      expect([...storage.values()]).toEqual([keys()[0]])
      fetchMock.mockResolvedValueOnce(checkout())
      await expect(send()).resolves.toMatchObject({ paymentId: 'original-payment' })
      expect(new Set(keys()).size).toBe(1)
      expect(storage.size).toBe(0)
    })
  }
}

it('isolates pending top-ups, plans, amounts and accounts without discarding the first account key', async () => {
  const { api } = await import('./client')
  const { setTaskCommandActor } = await import('../lib/taskCommands')
  setTaskCommandActor('buyer-one')
  fetchMock.mockRejectedValue(new TypeError('Response lost'))
  await expect(api.checkoutWalletTopup(2000)).rejects.toThrow()
  await expect(api.checkoutWalletTopup(2001)).rejects.toThrow()
  await expect(api.checkoutSubscription('plan-one')).rejects.toThrow()
  await expect(api.checkoutSubscription('plan-two')).rejects.toThrow()
  setTaskCommandActor('buyer-two')
  await expect(api.checkoutWalletTopup(2000)).rejects.toThrow()
  expect(new Set(keys()).size).toBe(5)
  setTaskCommandActor('buyer-one')
  await expect(api.checkoutWalletTopup(2000)).rejects.toThrow()
  expect(keys()[5]).toBe(keys()[0])
  expect(fetchMock.mock.calls.slice(0, 4).map(([, init]) => JSON.parse(String(init?.body)))).toEqual([
    { amountCents: 2000 }, { amountCents: 2001 }, { planId: 'plan-one' }, { planId: 'plan-two' },
  ])
})
