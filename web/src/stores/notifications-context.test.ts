import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, APIError, type Notification, type User } from '../api/client'
import { useSessionStore } from './session'
import { useNotificationsStore } from './notifications'

const user: User = { id: 'seller-a', email: 'seller@example.test', displayName: 'Seller', handle: 'seller', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', emailVerified: true, permissions: [] }
const notice = (id = 'private-a'): Notification => ({ id, kind: 'marketplace.payout_reviewed', title: 'Payout review updated', body: 'Open your request.', targetPath: '/workspace/payouts/request-a', createdAt: '2026-09-22T08:00:00Z' })
const pageOf = (id = 'private-a') => ({ items: [notice(id)], unreadCount: 3, nextCursor: 'next-page' })
function deferred<T>() {
  let resolve!: (value: T) => void; let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
beforeEach(() => { setActivePinia(createPinia()); const session = useSessionStore(); session.user = { ...user }; session.initialized = true })
afterEach(() => vi.restoreAllMocks())

it.each(['replacement', 'logout', 'same-account'] as const)('discards pending inbox and count responses after %s', async change => {
  const pending = deferred<ReturnType<typeof pageOf>>()
  vi.spyOn(api, 'listNotifications').mockReturnValue(pending.promise)
  const store = useNotificationsStore(); const session = useSessionStore()
  const loading = store.load(); const counting = store.refreshCount()
  session.user = change === 'logout' ? null : { ...user, id: change === 'replacement' ? 'seller-b' : user.id }
  expect(store.items).toEqual([]); expect(store.loading).toBe(false)
  pending.resolve(pageOf()); await Promise.all([loading, counting])
  expect(store.items).toEqual([]); expect(store.unreadCount).toBe(0); expect(store.nextCursor).toBe('')
})

it('keeps a newer filter loading while the older request finishes', async () => {
  const old = deferred<ReturnType<typeof pageOf>>(); const fresh = deferred<ReturnType<typeof pageOf>>()
  vi.spyOn(api, 'listNotifications').mockReturnValueOnce(old.promise).mockReturnValueOnce(fresh.promise)
  const store = useNotificationsStore()
  const first = store.load({ readState: 'all' }); const second = store.load({ readState: 'unread' })
  old.resolve(pageOf('old')); await first
  expect(store.items).toEqual([]); expect(store.loading).toBe(true)
  fresh.resolve(pageOf('new')); await second
  expect(store.items.map(item => item.id)).toEqual(['new']); expect(store.loading).toBe(false)
})

it('retains existing rows on pagination failure and rejects a stale filter continuation', async () => {
  const read = vi.spyOn(api, 'listNotifications').mockResolvedValueOnce(pageOf()).mockRejectedValue(new TypeError('Unavailable'))
  const store = useNotificationsStore(); await store.load({ readState: 'unread' })
  await store.loadMore({ readState: 'all' }); expect(read).toHaveBeenCalledTimes(1)
  await store.loadMore({ readState: 'unread' })
  expect(store.items.map(item => item.id)).toEqual(['private-a']); expect(store.nextCursor).toBe('next-page'); expect(store.loading).toBe(false)
})

it('access denial clears private data and invalidates a pending badge response', async () => {
  const count = deferred<ReturnType<typeof pageOf>>()
  vi.spyOn(api, 'listNotifications').mockResolvedValueOnce(pageOf()).mockReturnValueOnce(count.promise).mockRejectedValueOnce(new APIError(403, { code: 'forbidden', message: 'Access denied', retryable: false }))
  const store = useNotificationsStore(); await store.load(); const badge = store.refreshCount()
  await store.loadMore(); count.resolve(pageOf()); await badge
  expect(store.accessDenied).toBe(true); expect(store.items).toEqual([]); expect(store.unreadCount).toBe(0)
})

it('old mark-read acknowledgement cannot rewrite a new account or request its count', async () => {
  const marked = deferred<Notification>()
  const read = vi.spyOn(api, 'listNotifications').mockResolvedValue(pageOf())
  vi.spyOn(api, 'markNotificationRead').mockReturnValue(marked.promise)
  const store = useNotificationsStore(); await store.load()
  const pending = store.markRead(store.items[0])
  useSessionStore().user = { ...user, id: 'seller-b' }
  marked.resolve({ ...notice(), readAt: '2026-09-22T09:00:00Z' }); await pending
  expect(store.items).toEqual([]); expect(read).toHaveBeenCalledTimes(1)
})

it('concurrent read acknowledgements update both rows without resurrecting an older list', async () => {
  vi.spyOn(api, 'listNotifications').mockResolvedValue({ items: [notice('one'), notice('two')], unreadCount: 0 })
  const one = deferred<Notification>(); const two = deferred<Notification>()
  vi.spyOn(api, 'markNotificationRead').mockReturnValueOnce(one.promise).mockReturnValueOnce(two.promise)
  const store = useNotificationsStore(); await store.load()
  const first = store.markRead(store.items[0]); const second = store.markRead(store.items[1])
  one.resolve({ ...notice('one'), readAt: '2026-09-22T09:00:00Z' }); await first
  two.resolve({ ...notice('two'), readAt: '2026-09-22T09:00:00Z' }); await second
  expect(store.items.every(item => !!item.readAt)).toBe(true)
})

it('leaving inbox clears private rows while retaining the global unread count', async () => {
  vi.spyOn(api, 'listNotifications').mockResolvedValue(pageOf())
  const store = useNotificationsStore(); await store.load(); store.clearView()
  expect(store.items).toEqual([]); expect(store.unreadCount).toBe(3)
})
