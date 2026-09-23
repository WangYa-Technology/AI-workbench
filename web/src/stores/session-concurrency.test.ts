import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, APIError, type User } from '../api/client'
import { useSessionStore } from './session'

const buyer: User = { id: 'buyer', email: 'buyer@fixture.test', handle: 'buyer', displayName: 'Buyer', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', emailVerified: true, permissions: [] }
const seller: User = { ...buyer, id: 'seller', email: 'seller@fixture.test', handle: 'seller', displayName: 'Seller', role: 'creator' }
const sessionOf = (user: User) => ({ user, authentication: 'session' })
const credentials = { email: seller.email, password: 'fixture-password' }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.restoreAllMocks())

describe('session response ordering at marketplace identity boundaries', () => {
  it.each(['logout', 'clear'] as const)('does not restore a buyer from a session probe after %s', async action => {
    const read = deferred<ReturnType<typeof sessionOf>>()
    vi.spyOn(api, 'session').mockReturnValue(read.promise)
    vi.spyOn(api, 'logout').mockResolvedValue(undefined)
    const store = useSessionStore()
    store.user = buyer
    const pending = store.ensure(true)
    if (action === 'logout') await store.logout()
    else store.clear()
    read.resolve(sessionOf(buyer))
    const result = await pending
    expect(store.user).toBeNull()
    expect(result).toBeNull()
    expect(store.initialized).toBe(true)
    expect(store.loading).toBe(false)
  })

  it.each(['success', 'unauthorized', 'network'] as const)('ignores an old probe %s after a seller signs in', async outcome => {
    const read = deferred<ReturnType<typeof sessionOf>>()
    vi.spyOn(api, 'session').mockReturnValue(read.promise)
    vi.spyOn(api, 'login').mockResolvedValue(sessionOf(seller))
    const store = useSessionStore()
    const pending = store.ensure()
    await store.login(credentials)
    if (outcome === 'success') read.resolve(sessionOf(buyer))
    else read.reject(outcome === 'unauthorized'
      ? new APIError(401, { code: 'authentication_required', message: 'Old cookie expired', retryable: false })
      : new TypeError('Old connection failed'))
    const result = await pending
    expect(store.user?.id).toBe(seller.id)
    expect(store.error).toBe('')
    expect(result).toBeNull()
  })

  it('does not let an obsolete probe release the current probe or its loading state', async () => {
    const old = deferred<ReturnType<typeof sessionOf>>()
    const fresh = deferred<ReturnType<typeof sessionOf>>()
    const request = vi.spyOn(api, 'session').mockReturnValueOnce(old.promise).mockReturnValue(fresh.promise)
    const store = useSessionStore()
    const first = store.ensure()
    store.clear()
    const second = store.ensure(true)
    old.resolve(sessionOf(buyer))
    await first
    const busyWhileFreshPending = store.loading
    const third = store.ensure(true)
    fresh.resolve(sessionOf(seller))
    await Promise.all([second, third])
    expect(busyWhileFreshPending).toBe(true)
    expect(request).toHaveBeenCalledTimes(2)
    expect(store.user?.id).toBe(seller.id)
  })

  it('does not publish a previous buyer profile after switching to the seller', async () => {
    const profile = deferred<{ user: User }>()
    vi.spyOn(api, 'updateProfile').mockReturnValue(profile.promise)
    vi.spyOn(api, 'login').mockResolvedValue(sessionOf(seller))
    const store = useSessionStore()
    store.user = buyer
    store.initialized = true
    const old = store.updateProfile({ displayName: 'Changed buyer', locale: 'en-US', timezone: 'UTC' })
    await vi.waitFor(() => expect(api.updateProfile).toHaveBeenCalledTimes(1))
    const login = store.login(credentials)
    profile.resolve({ user: { ...buyer, displayName: 'Changed buyer' } })
    const [oldResult] = await Promise.all([old, login])
    expect(oldResult).toBeNull()
    expect(store.user?.id).toBe(seller.id)
  })

  it('orders cookie-changing login and logout requests and suppresses the superseded login result', async () => {
    const signingIn = deferred<ReturnType<typeof sessionOf>>()
    const events: string[] = []
    vi.spyOn(api, 'login').mockImplementation(async () => {
      events.push('login sent')
      const response = await signingIn.promise
      events.push('login cookie received')
      return response
    })
    vi.spyOn(api, 'logout').mockImplementation(async () => { events.push('logout sent') })
    const store = useSessionStore()
    const login = store.login(credentials)
    await vi.waitFor(() => expect(api.login).toHaveBeenCalledTimes(1))
    const logout = store.logout()
    signingIn.resolve(sessionOf(seller))
    const [loginResult] = await Promise.all([login, logout])
    expect(events).toEqual(['login sent', 'login cookie received', 'logout sent'])
    expect(loginResult).toBeNull()
    expect(store.user).toBeNull()
    expect(store.loading).toBe(false)
  })

  it('waits for the current authentication before probing or returning an old identity', async () => {
    const signingIn = deferred<ReturnType<typeof sessionOf>>()
    vi.spyOn(api, 'login').mockReturnValue(signingIn.promise)
    const read = vi.spyOn(api, 'session').mockResolvedValue(sessionOf(buyer))
    const store = useSessionStore()
    store.user = buyer
    store.initialized = true
    const login = store.login(credentials)
    const ensure = store.ensure(true)
    signingIn.resolve(sessionOf(seller))
    const [, ensured] = await Promise.all([login, ensure])
    expect(read).not.toHaveBeenCalled()
    expect(ensured?.id).toBe(seller.id)
    expect(store.user?.id).toBe(seller.id)
  })

  it('does not share a pending session request across different Pinia instances', async () => {
    const firstRead = deferred<ReturnType<typeof sessionOf>>()
    const secondRead = deferred<ReturnType<typeof sessionOf>>()
    const request = vi.spyOn(api, 'session').mockReturnValueOnce(firstRead.promise).mockReturnValueOnce(secondRead.promise)
    const first = useSessionStore(createPinia())
    const second = useSessionStore(createPinia())
    const pending = [first.ensure(), second.ensure()]
    firstRead.resolve(sessionOf(buyer))
    secondRead.resolve(sessionOf(seller))
    await Promise.all(pending)
    expect(request).toHaveBeenCalledTimes(2)
    expect(first.user?.id).toBe(buyer.id)
    expect(second.user?.id).toBe(seller.id)
  })

  it('keeps the latest mutation loading and error state when an earlier login fails', async () => {
    const first = deferred<ReturnType<typeof sessionOf>>()
    const last = deferred<ReturnType<typeof sessionOf>>()
    const request = vi.spyOn(api, 'login').mockReturnValueOnce(first.promise).mockReturnValueOnce(last.promise)
    const store = useSessionStore()
    const old = store.login({ ...credentials, email: buyer.email })
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1))
    const current = store.login(credentials)
    first.reject(new TypeError('Obsolete connection failed'))
    await old
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(2))
    const loading = store.loading
    const error = store.error
    last.resolve(sessionOf(seller))
    await current
    expect(loading).toBe(true)
    expect(error).toBe('')
    expect(store.user?.id).toBe(seller.id)
    expect(store.loading).toBe(false)
  })

  it('clear invalidates an authentication already in flight without restoring its user', async () => {
    const login = deferred<ReturnType<typeof sessionOf>>()
    const request = vi.spyOn(api, 'login').mockReturnValue(login.promise)
    const store = useSessionStore()
    const pending = store.login(credentials)
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1))
    store.clear()
    login.resolve(sessionOf(seller))
    await expect(pending).resolves.toBeNull()
    expect(store.user).toBeNull()
    expect(store.loading).toBe(false)
  })

  it('does not send a queued sign-in that was superseded by local revocation', async () => {
    const first = deferred<ReturnType<typeof sessionOf>>()
    const request = vi.spyOn(api, 'login').mockReturnValue(first.promise)
    const store = useSessionStore()
    const old = store.login({ ...credentials, email: buyer.email })
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1))
    const queued = store.login(credentials)
    store.clear()
    first.resolve(sessionOf(buyer))
    await Promise.all([old, queued])
    expect(request).toHaveBeenCalledTimes(1)
    expect(store.user).toBeNull()
  })

  it('freezes queued credentials instead of reading a changed form later', async () => {
    const first = deferred<ReturnType<typeof sessionOf>>()
    const request = vi.spyOn(api, 'login').mockReturnValueOnce(first.promise).mockResolvedValue(sessionOf(seller))
    const store = useSessionStore()
    const old = store.login({ ...credentials, email: buyer.email })
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1))
    const input = { ...credentials }
    const current = store.login(input)
    input.email = buyer.email
    input.password = 'Changed after submission'
    first.resolve(sessionOf(buyer))
    await Promise.all([old, current])
    expect(request.mock.calls[1]?.[0]).toEqual(credentials)
    expect(store.user?.id).toBe(seller.id)
  })

  it('rejects a profile response for a different identity and preserves the current user', async () => {
    vi.spyOn(api, 'updateProfile').mockResolvedValue({ user: seller })
    const store = useSessionStore()
    store.user = buyer
    store.initialized = true
    const result = await store.updateProfile({ displayName: 'Changed buyer', locale: 'en-US', timezone: 'UTC' })
    expect(result).toBeNull()
    expect(store.user?.id).toBe(buyer.id)
    expect(store.error).not.toBe('')
  })

  it('does not send the old account profile form while a new account is signing in', async () => {
    const login = deferred<ReturnType<typeof sessionOf>>()
    const request = vi.spyOn(api, 'login').mockReturnValue(login.promise)
    const profile = vi.spyOn(api, 'updateProfile').mockResolvedValue({ user: seller })
    const store = useSessionStore()
    store.user = buyer
    store.initialized = true
    const pending = store.login(credentials)
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1))
    const update = store.updateProfile({ displayName: 'Private buyer form', locale: 'en-US', timezone: 'UTC' })
    login.resolve(sessionOf(seller))
    const [result] = await Promise.all([update, pending])
    expect(result).toBeNull()
    expect(profile).not.toHaveBeenCalled()
    expect(store.user?.id).toBe(seller.id)
  })
})
