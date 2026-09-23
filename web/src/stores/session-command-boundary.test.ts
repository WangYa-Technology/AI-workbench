import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type User } from '../api/client'
import { useSessionStore } from './session'

const buyer: User = { id: 'buyer', email: 'buyer@fixture.test', handle: 'buyer', displayName: 'Buyer', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', emailVerified: true, permissions: [] }
const seller: User = { ...buyer, id: 'seller', email: 'seller@fixture.test', handle: 'seller' }
const sessionOf = (user: User) => ({ user, authentication: 'session' })
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(yes => { resolve = yes })
  return { promise, resolve }
}
const fetchMock = vi.fn<typeof fetch>()
beforeEach(() => {
  setActivePinia(createPinia())
  fetchMock.mockReset().mockImplementation(async () => new Response(JSON.stringify({ id: 'unexpected-command' })))
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
async function currentBuyer() {
  vi.spyOn(api, 'session').mockResolvedValue(sessionOf(buyer))
  const store = useSessionStore()
  await store.ensure()
  return store
}

const commands = [
  ['listing submission', () => api.mutateSellerProduct('product', 'submit', { expectedVersion: 'observed-version', rightsConfirmed: true })],
  ['purchase', () => api.checkoutProduct('product', true, 'observed-offer')],
  ['refund', () => api.refundOrder('order', 'Original purchase did not match the terms.')],
  ['finance adjustment', () => api.adminAdjustFinance('account', { deltaCents: 500, currency: 'USD' })],
  ['non-idempotent preview update', () => api.setProductPreview('product', 'asset', 'observed-offer')],
  ['file upload', () => {
    const form = new FormData()
    form.append('title', 'Original buyer file')
    form.append('file', new File(['private'], 'source.txt', { type: 'text/plain' }))
    return api.uploadAsset(form)
  }],
] as const

describe('unsent commands cannot cross an in-flight identity change', () => {
  it.each(commands)('blocks %s after the login cookie arrives but before its body identifies the user', async (_name, command) => {
    const store = await currentBuyer()
    const loginBody = deferred<ReturnType<typeof sessionOf>>()
    let loginHeadersReceived = false
    const writes: string[] = []
    fetchMock.mockImplementation(async (url) => {
      if (url === '/api/v1/auth/login') {
        loginHeadersReceived = true // Browser may already have applied Set-Cookie.
        const response = new Response('{}')
        response.json = () => loginBody.promise
        return response
      }
      writes.push(String(url))
      return new Response(JSON.stringify({ id: 'written-with-new-cookie' }))
    })
    const login = store.login({ email: seller.email, password: 'fixture-password' })
    await vi.waitFor(() => expect(loginHeadersReceived).toBe(true))
    const result = await command().then(() => 'sent', error => error)
    loginBody.resolve(sessionOf(seller))
    await login
    expect(result).toBeInstanceOf(Error)
    expect((result as Error).message).toContain('Session changed')
    expect(writes).toEqual([])
    expect(store.user?.id).toBe(seller.id)
  })

  it('invalidates a command already hashing when a same-account login begins', async () => {
    const store = await currentBuyer()
    const loginResponse = deferred<ReturnType<typeof sessionOf>>()
    vi.spyOn(api, 'login').mockReturnValue(loginResponse.promise)
    const command = api.checkoutProduct('product', true, 'observed-offer').then(() => 'sent', error => error)
    const login = store.login({ email: buyer.email, password: 'fixture-password' })
    const result = await command
    loginResponse.resolve(sessionOf(buyer))
    await login
    expect(result).toBeInstanceOf(Error)
    expect(fetchMock).not.toHaveBeenCalled()
    await api.checkoutProduct('product', true, 'observed-offer')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps writes blocked after local clear while a cookie-changing request is still in flight', async () => {
    const store = await currentBuyer()
    const loginResponse = deferred<ReturnType<typeof sessionOf>>()
    const loginRequest = vi.spyOn(api, 'login').mockReturnValue(loginResponse.promise)
    const login = store.login({ email: seller.email, password: 'fixture-password' })
    await vi.waitFor(() => expect(loginRequest).toHaveBeenCalledTimes(1))
    store.clear()
    const result = await api.setProductPreview('product', 'asset', 'observed-offer').then(() => 'sent', error => error)
    loginResponse.resolve(sessionOf(seller))
    await login
    expect(result).toBeInstanceOf(Error)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(store.user).toBeNull()
  })

  it('rechecks the actual session after a login response is lost instead of trusting the old actor', async () => {
    const store = await currentBuyer()
    vi.mocked(api.session).mockResolvedValue(sessionOf(seller))
    vi.spyOn(api, 'login').mockRejectedValue(new TypeError('Login body lost after cookie'))
    const result = await store.login({ email: seller.email, password: 'fixture-password' })
    expect(result).toBeNull()
    expect(api.session).toHaveBeenCalledTimes(2)
    expect(store.user?.id).toBe(seller.id)
    expect(store.error).not.toBe('')
  })

  it('does not report a failed logout as success, even when the old cookie is still active', async () => {
    const store = await currentBuyer()
    vi.spyOn(api, 'logout').mockRejectedValue(new TypeError('Logout failed'))
    await expect(store.logout()).resolves.toBe(false)
    expect(api.session).toHaveBeenCalledTimes(2)
    expect(store.user?.id).toBe(buyer.id)
    expect(store.error).not.toBe('')
  })

  it('reports confirmed logout separately from request failure', async () => {
    const store = await currentBuyer()
    vi.spyOn(api, 'logout').mockResolvedValue(undefined)
    await expect(store.logout()).resolves.toBe(true)
    expect(store.user).toBeNull()
    expect(api.session).toHaveBeenCalledTimes(1)
  })

  it('keeps the transition blocked until an uncertain authentication has been checked', async () => {
    const store = await currentBuyer()
    const recovery = deferred<ReturnType<typeof sessionOf>>()
    vi.mocked(api.session).mockReturnValue(recovery.promise)
    vi.spyOn(api, 'login').mockRejectedValue(new TypeError('Uncertain login'))
    const login = store.login({ email: seller.email, password: 'fixture-password' })
    await vi.waitFor(() => expect(api.session).toHaveBeenCalledTimes(2))
    const blocked = await api.setProductPreview('product', 'asset', 'offer').then(() => 'sent', error => error)
    recovery.resolve(sessionOf(seller))
    await login
    expect(blocked).toBeInstanceOf(Error)
    expect(fetchMock).not.toHaveBeenCalled()
    await api.setProductPreview('product', 'asset', 'offer')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('does not release a newer login gate when an earlier request finishes', async () => {
    const store = await currentBuyer()
    const oldResponse = deferred<ReturnType<typeof sessionOf>>()
    const newResponse = deferred<ReturnType<typeof sessionOf>>()
    const loginRequest = vi.spyOn(api, 'login').mockReturnValueOnce(oldResponse.promise).mockReturnValueOnce(newResponse.promise)
    const old = store.login({ email: buyer.email, password: 'fixture-password' })
    await vi.waitFor(() => expect(loginRequest).toHaveBeenCalledTimes(1))
    const current = store.login({ email: seller.email, password: 'fixture-password' })
    oldResponse.resolve(sessionOf(buyer))
    await old
    await vi.waitFor(() => expect(loginRequest).toHaveBeenCalledTimes(2))
    const blocked = await api.setProductPreview('product', 'asset', 'offer').then(() => 'sent', error => error)
    newResponse.resolve(sessionOf(seller))
    await current
    expect(blocked).toBeInstanceOf(Error)
    expect(fetchMock).not.toHaveBeenCalled()
    await api.setProductPreview('product', 'asset', 'offer')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('does not leave a cached actor when both authentication and reconciliation fail', async () => {
    const store = await currentBuyer()
    vi.spyOn(api, 'login').mockRejectedValue(new TypeError('Uncertain login'))
    vi.mocked(api.session).mockRejectedValue(new TypeError('Session unavailable'))
    await expect(store.login({ email: seller.email, password: 'fixture-password' })).resolves.toBeNull()
    expect(store.user).toBeNull()
    expect(store.initialized).toBe(true)
    expect(store.loading).toBe(false)
    expect(store.error).not.toBe('')
    vi.mocked(api.session).mockResolvedValue(sessionOf(seller))
    await store.ensure(true)
    expect(store.user?.id).toBe(seller.id)
    await api.setProductPreview('product', 'asset', 'offer')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('continues to send an ordinary profile update through the shared API client', async () => {
    const store = await currentBuyer()
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ user: { ...buyer, displayName: 'Updated buyer' } })))
    await expect(store.updateProfile({ displayName: 'Updated buyer', locale: 'en-US', timezone: 'UTC' })).resolves.toMatchObject({ id: buyer.id, displayName: 'Updated buyer' })
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/account/profile')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
