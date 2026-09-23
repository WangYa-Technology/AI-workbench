import { expect, it, vi } from 'vitest'
import { createScopedApi } from './scopedApi'
import { CommandSessionChangedError } from './taskCommands'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

it('keeps method arguments, returned data and the client receiver', async () => {
  const client = { prefix: 'client', async read(id: number, options: { cursor: string }) { return `${this.prefix}:${id}:${options.cursor}` } }
  const scoped = createScopedApi(client, () => true)
  await expect(scoped.read(7, { cursor: 'next' })).resolves.toBe('client:7:next')
})

it('does not dispatch a request after its view is invalidated', async () => {
  const read = vi.fn(async () => 'private')
  await expect(createScopedApi({ read }, () => false).read()).rejects.toBeInstanceOf(CommandSessionChangedError)
  expect(read).not.toHaveBeenCalled()
})

for (const failure of [false, true]) {
  it(`rejects a stale ${failure ? 'failure' : 'success'} before it can start a follow-up request`, async () => {
    let active = true
    const pending = deferred<string>()
    const refresh = vi.fn(async () => 'updated')
    const scoped = createScopedApi({ command: () => pending.promise, refresh }, () => active)
    const result = scoped.command().then(() => scoped.refresh())
    const check = expect(result).rejects.toBeInstanceOf(CommandSessionChangedError)
    active = false
    if (failure) pending.reject(new Error('Private failure'))
    else pending.resolve('Private result')
    await check
    expect(refresh).not.toHaveBeenCalled()
  })
}

it('preserves current request failures for normal error handling', async () => {
  const failure = new Error('Current failure')
  const scoped = createScopedApi({ read: async () => { throw failure } }, () => true)
  await expect(scoped.read()).rejects.toBe(failure)
})

it('reports current errors before invalidating sibling requests and follow-up commands', async () => {
  let active = true
  const pending = deferred<string>()
  const failure = new Error('Access denied')
  const report = vi.fn(() => { active = false })
  const write = vi.fn(async () => 'submitted')
  const scoped = createScopedApi({ read: () => pending.promise, denied: async () => { throw failure }, write }, () => active, report)
  const oldRead = scoped.read()
  await expect(scoped.denied()).rejects.toBeInstanceOf(CommandSessionChangedError)
  expect(report).toHaveBeenCalledExactlyOnceWith(failure)
  pending.resolve('private')
  await expect(oldRead).rejects.toBeInstanceOf(CommandSessionChangedError)
  await expect(scoped.write()).rejects.toBeInstanceOf(CommandSessionChangedError)
  expect(write).not.toHaveBeenCalled()
  expect(report).toHaveBeenCalledTimes(1)
})

it('does not report an obsolete error to the new workspace', async () => {
  let active = true
  const pending = deferred<string>()
  const report = vi.fn()
  const result = createScopedApi({ read: () => pending.promise }, () => active, report).read()
  active = false
  pending.reject(new Error('Old access denial'))
  await expect(result).rejects.toBeInstanceOf(CommandSessionChangedError)
  expect(report).not.toHaveBeenCalled()
})
