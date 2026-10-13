import { afterEach, expect, it, vi } from 'vitest'
import { pollAssetScan } from './assetScan'
afterEach(() => vi.useRealTimers())
for (const scanStatus of ['clean', 'review', 'rejected']) it(`recovers transient failures and observes ${scanStatus} after 15 seconds`, async () => {
  vi.useFakeTimers()
  let calls = 0
  const read = vi.fn(async () => {
    calls++
    if (calls === 2) throw new Error('temporary read failure')
    return { scanStatus: calls > 24 ? scanStatus : 'pending' }
  })
  const update = vi.fn(), paused = vi.fn()
  const done = pollAssetScan({ signal: new AbortController().signal, read, update, paused })
  await vi.advanceTimersByTimeAsync(20_000)
  await done
  expect(update).toHaveBeenLastCalledWith({ scanStatus })
  expect(paused).not.toHaveBeenCalled()
})
it('pauses at budget and can be explicitly retried', async () => {
  vi.useFakeTimers()
  const controller = new AbortController(), paused = vi.fn(), update = vi.fn()
  const options = { signal: controller.signal, read: async () => ({ scanStatus: 'pending' }), update, paused, budgetMs: 2000 }
  const done = pollAssetScan(options)
  await vi.advanceTimersByTimeAsync(3000); await done
  expect(paused).toHaveBeenCalledOnce()
  await vi.advanceTimersByTimeAsync(5000)
  const retried = pollAssetScan({ ...options, read: async () => ({ scanStatus: 'clean' }) })
  await vi.advanceTimersByTimeAsync(750); await retried
  expect(update).toHaveBeenLastCalledWith({ scanStatus: 'clean' })
})
it('aborts a pending request and prevents late state writes on navigation', async () => {
  vi.useFakeTimers()
  const controller = new AbortController(), update = vi.fn(), paused = vi.fn()
  let finish!: (value: { scanStatus: string }) => void
  let requestSignal!: AbortSignal
  const done = pollAssetScan({ signal: controller.signal, read: signal => { requestSignal = signal; return new Promise(resolve => { finish = resolve }) }, update, paused })
  await vi.advanceTimersByTimeAsync(750)
  controller.abort()
  expect(requestSignal.aborted).toBe(true)
  finish({ scanStatus: 'clean' }); await done
  expect(update).not.toHaveBeenCalled(); expect(paused).not.toHaveBeenCalled()
  expect(vi.getTimerCount()).toBe(0)
})
it('times out hung HTTP reads and stops unauthorized reads', async () => {
  vi.useFakeTimers()
  const paused = vi.fn(), update = vi.fn()
  const done = pollAssetScan({ signal: new AbortController().signal, read: signal => new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new Error('timeout')))), update, paused, budgetMs: 2000 })
  await vi.advanceTimersByTimeAsync(3000); await done
  expect(paused).toHaveBeenCalledOnce(); expect(update).not.toHaveBeenCalled()
  const read = vi.fn(async () => { throw { status: 403 } })
  const denied = pollAssetScan({ signal: new AbortController().signal, read, update, paused })
  await vi.advanceTimersByTimeAsync(2000); await denied
  expect(read).toHaveBeenCalledOnce()
})
