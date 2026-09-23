import { effectScope } from 'vue'
import { expect, it } from 'vitest'
import { useCursorDirectory } from './useCursorDirectory'

type Page = { items: { id: string }[]; nextCursor?: string }
function fixture() {
  const requests: { cursor?: string; resolve: (value: Page) => void; reject: (error: unknown) => void }[] = []
  const scope = effectScope()
  const directory = scope.run(() => useCursorDirectory((cursor?: string) => new Promise<Page>((resolve, reject) => requests.push({ cursor, resolve, reject }))))!
  return { directory, requests, scope }
}

it('keeps the latest refresh when initial pages return out of order', async () => {
  const { directory, requests, scope } = fixture()
  const first = directory.load()
  const second = directory.load()
  requests[1].resolve({ items: [{ id: 'current' }], nextCursor: 'current-cursor' })
  await second
  requests[0].resolve({ items: [{ id: 'old' }], nextCursor: 'old-cursor' })
  await first
  expect(directory.items.value).toEqual([{ id: 'current' }])
  expect(directory.nextCursor.value).toBe('current-cursor')
  scope.stop()
})

for (const failure of [false, true]) {
  it(`does not let an old continuation ${failure ? 'error' : 'result'} release the current loading state`, async () => {
    const { directory, requests, scope } = fixture()
    const initial = directory.load()
    requests[0].resolve({ items: [{ id: 'original' }], nextCursor: 'next' })
    await initial
    const old = directory.loadMore()
    const current = directory.load()
    if (failure) requests[1].reject(new Error('Old request failure'))
    else requests[1].resolve({ items: [{ id: 'old' }] })
    await old
    expect(directory.loading.value).toBe(true)
    expect(directory.items.value).toEqual([])
    expect(directory.nextCursor.value).toBe(null)
    requests[2].resolve({ items: [{ id: 'current' }] })
    await current
    expect(directory.items.value).toEqual([{ id: 'current' }])
    expect(directory.loading.value).toBe(false)
    scope.stop()
  })
}

it('allows only one continuation and retains its cursor after a retryable failure', async () => {
  const { directory, requests, scope } = fixture()
  const initial = directory.load()
  requests[0].resolve({ items: [{ id: 'first' }], nextCursor: 'next' })
  await initial
  const continuation = directory.loadMore()
  const failed = expect(continuation).rejects.toThrow('Retry')
  await directory.loadMore()
  expect(requests).toHaveLength(2)
  requests[1].reject(new Error('Retry'))
  await failed
  expect(directory.nextCursor.value).toBe('next')
  const retry = directory.loadMore()
  expect(requests[2].cursor).toBe('next')
  requests[2].resolve({ items: [{ id: 'first' }, { id: 'second' }] })
  await retry
  expect(directory.items.value).toEqual([{ id: 'first' }, { id: 'second' }])
  scope.stop()
})

it('disposal discards pending responses and clears private directory data', async () => {
  const { directory, requests, scope } = fixture()
  const pending = directory.load()
  scope.stop()
  requests[0].resolve({ items: [{ id: 'private' }], nextCursor: 'private-next' })
  await pending
  expect(directory.items.value).toEqual([])
  expect(directory.nextCursor.value).toBe(null)
  expect(directory.loading.value).toBe(false)
  await directory.load()
  expect(requests).toHaveLength(1)
})
