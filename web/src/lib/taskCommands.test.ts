import { beforeEach, describe, expect, it, vi } from 'vitest'
import { beginCommandSessionTransition, runTaskCommand, setTaskCommandActor } from './taskCommands'

beforeEach(() => { setTaskCommandActor(null); setTaskCommandActor('first-user') })
const uncertain = () => false

describe('task command retries', () => {
  it('retains a key when a response is lost, then starts a new operation after success', async () => {
    const keys: string[] = []
    const send = async (key: string) => { keys.push(key); if (keys.length === 1) throw new TypeError('Network error'); return 'ok' }
    await expect(runTaskCommand('/tasks', 'POST', { title: 'same' }, send, uncertain)).rejects.toThrow()
    await runTaskCommand('/tasks', 'POST', { title: 'same' }, send, uncertain)
    await runTaskCommand('/tasks', 'POST', { title: 'same' }, send, uncertain)
    expect(keys[0]).toBe(keys[1]); expect(keys[2]).not.toBe(keys[1])
  })
  it('does not share keys between users or tasks', async () => {
    const keys: string[] = []
    const fail = async (key: string) => { keys.push(key); throw new Error('timeout') }
    await runTaskCommand('/tasks/a/claim', 'POST', null, fail, uncertain).catch(() => {})
    await runTaskCommand('/tasks/b/claim', 'POST', null, fail, uncertain).catch(() => {})
    setTaskCommandActor('second-user')
    await runTaskCommand('/tasks/a/claim', 'POST', null, fail, uncertain).catch(() => {})
    expect(new Set(keys).size).toBe(3)
  })
  it('coalesces concurrent submissions', async () => {
    let calls = 0
    const send = async () => { calls++; await new Promise(resolve => setTimeout(resolve, 20)); return 'done' }
    const values = await Promise.all([1, 2].map(() => runTaskCommand('/tasks', 'POST', {}, send, uncertain)))
    expect(values).toEqual(['done', 'done']); expect(calls).toBe(1)
  })

  it.each(['success', 'definitive-error'] as const)('does not let an old %s erase a current unresolved key', async outcome => {
    const keys: string[] = []
    let release!: (value: string) => void
    let reject!: (error: Error) => void
    const old = runTaskCommand('/seller/products', 'POST', { title: 'Original submission' }, async key => {
      keys.push(key)
      return new Promise<string>((resolve, fail) => { release = resolve; reject = fail })
    }, () => true).then(value => ({ value }), error => ({ error }))
    await vi.waitFor(() => expect(release).toBeTypeOf('function'))
    // Same-account refresh changes the epoch without clearing pending keys.
    const finish = beginCommandSessionTransition()
    setTaskCommandActor('first-user')
    finish()
    await expect(runTaskCommand('/seller/products', 'POST', { title: 'Original submission' }, async key => {
      keys.push(key)
      throw new TypeError('Current response unknown')
    }, uncertain)).rejects.toThrow('Current response unknown')
    if (outcome === 'success') release('Old operation accepted')
    else reject(new Error('Old definitive response'))
    const result = await old
    await runTaskCommand('/seller/products', 'POST', { title: 'Original submission' }, async key => { keys.push(key); return 'recovered' }, uncertain)
    expect(new Set(keys).size).toBe(1)
    expect(result).toHaveProperty('error')
  })

  it('does not return another account private command result after a switch', async () => {
    let release!: (value: { privateReference: string }) => void
    const request = runTaskCommand('/seller/products', 'POST', { title: 'Original submission' },
      () => new Promise<{ privateReference: string }>(resolve => { release = resolve }), uncertain)
    await vi.waitFor(() => expect(release).toBeTypeOf('function'))
    setTaskCommandActor('another-user')
    release({ privateReference: 'original-account-only' })
    await expect(request).rejects.toThrow('Session changed')
  })

  it('rejects a response received while a session transition is still pending', async () => {
    let release!: (value: string) => void
    const request = runTaskCommand('/seller/products', 'POST', {}, () => new Promise<string>(resolve => { release = resolve }), uncertain)
    await vi.waitFor(() => expect(release).toBeTypeOf('function'))
    const finish = beginCommandSessionTransition()
    try {
      release('accepted under the old cookie')
      await expect(request).rejects.toThrow('Session changed')
    } finally { finish() }
  })
})
