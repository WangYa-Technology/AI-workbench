import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type ListingMutation, type ProductDraft } from './client'
import { setTaskCommandActor } from '../lib/taskCommands'

const fetchMock = vi.fn<typeof fetch>()
const productID = '00000000-0000-4000-8000-000000009901'
const draft = (): ProductDraft => ({
  title: 'Accepted title', description: 'Accepted description', productType: 'asset', category: 'market_asset',
  assetId: '00000000-0000-4000-8000-000000009902', previewAssetId: null,
  priceCents: 1900, currency: 'USD', licenseCode: 'commercial', aiDisclosure: 'Authored by seller',
  includedFiles: ['original.txt'], files: [{ assetId: '00000000-0000-4000-8000-000000009902', name: 'original.txt' }], compatibility: 'UTF-8',
})
const keyAt = (index: number) => new Headers(fetchMock.mock.calls[index]?.[1]?.headers).get('Idempotency-Key')
const bodyAt = (index: number) => JSON.parse(String(fetchMock.mock.calls[index]?.[1]?.body))
beforeEach(() => {
  setTaskCommandActor(null)
  setTaskCommandActor(crypto.randomUUID())
  fetchMock.mockReset()
  fetchMock.mockImplementation(async () => new Response(JSON.stringify({ id: productID })))
  vi.stubGlobal('fetch', fetchMock)
})
afterEach(() => vi.unstubAllGlobals())

describe('JSON commands preserve submitted content across asynchronous key preparation', () => {
  it('recovers the original created listing after response loss with the same key and body', async () => {
    // Model the server contract: the first request commits, its response is lost,
    // and a retry with the same key must contain the same submitted document.
    let acceptedBody: unknown
    let acceptedKey: string | null = null
    fetchMock.mockImplementation(async (_url, init) => {
      const body = JSON.parse(String(init?.body))
      const key = new Headers(init?.headers).get('Idempotency-Key')
      if (!acceptedKey) {
        acceptedKey = key
        acceptedBody = body
        throw new TypeError('Committed response lost')
      }
      if (key !== acceptedKey || JSON.stringify(body) !== JSON.stringify(acceptedBody)) {
        return new Response(JSON.stringify({ error: { code: 'product_listing_conflict', message: 'Different command payload', retryable: false } }), { status: 409 })
      }
      return new Response(JSON.stringify({ id: productID }))
    })
    const input: ListingMutation = { draft: draft() }
    const original = structuredClone(input)
    const pending = api.mutateSellerProduct(null, 'create', input)
    input.draft!.title = 'Later unsaved title'
    input.draft!.files![0]!.name = 'later.txt'
    input.draft!.includedFiles[0] = 'later.txt'
    await expect(pending).rejects.toThrow('Committed response lost')
    await expect(api.mutateSellerProduct(null, 'create', original)).resolves.toMatchObject({ id: productID })
    expect(acceptedBody).toEqual(original)
    expect(keyAt(0)).toBe(keyAt(1))
  })

  it('keeps the edited price, source and version captured at submit time', async () => {
    const input: ListingMutation = { expectedVersion: 'observed-version', draft: draft() }
    const original = structuredClone(input)
    const pending = api.mutateSellerProduct(productID, 'edit', input)
    input.expectedVersion = 'later-version'
    input.draft!.priceCents = 9500
    input.draft!.files![0]!.assetId = productID
    await pending
    expect(bodyAt(0)).toEqual(original)
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe('PUT')
  })

  it('keeps a reviewer decision bound to the confirmed reason and version', async () => {
    const input = { expectedVersion: 'reviewed-version', confirmed: true, reason: 'Reviewed source and license.' }
    const original = { ...input }
    const pending = api.mutateSellerProduct(productID, 'approve', input, true)
    input.reason = 'Not the confirmed decision'
    input.expectedVersion = 'unreviewed-version'
    await pending
    expect(bodyAt(0)).toEqual(original)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`/api/v1/admin/products/${productID}/approve`)
  })

  it('does not change a task delivery file list after submission', async () => {
    const input = { assetIds: [productID], note: 'Ready for review', rightsEvidence: 'Original', aiDisclosure: 'Authored', rightsConfirmed: true }
    const original = structuredClone(input)
    const pending = api.deliverTask('task', input)
    input.assetIds.push('another-file')
    input.note = 'Edited after submit'
    await pending
    expect(bodyAt(0)).toEqual(original)
  })

  it('does not publish an edit made after the community submit action', async () => {
    const input = { draft: false, title: 'Submitted discussion', body: 'Submitted content.' }
    const original = { ...input }
    const pending = api.createCommunityPost(input)
    input.title = 'Unsaved title'
    input.body = 'Unsaved content'
    await pending
    expect(bodyAt(0)).toEqual(original)
  })

  it('gives two distinct submitted snapshots their own content and keys', async () => {
    const input: ListingMutation = { draft: draft() }
    const firstBody = structuredClone(input)
    const first = api.mutateSellerProduct(null, 'create', input)
    input.draft!.title = 'Second explicit submission'
    const secondBody = structuredClone(input)
    const second = api.mutateSellerProduct(null, 'create', input)
    await Promise.all([first, second])
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls.map((_, i) => bodyAt(i))).toEqual(expect.arrayContaining([firstBody, secondBody]))
    expect(keyAt(0)).not.toBe(keyAt(1))
  })

  it('uses an existing pending key from the prior JSON fingerprint without persisting form content', async () => {
    const actor = 'upgrading-seller'
    setTaskCommandActor(actor)
    const input: ListingMutation = { draft: draft() }
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify([actor, '/seller/products', 'POST', input])))
    const storageKey = 'hcai:pending-task:' + Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
    const originalKey = crypto.randomUUID()
    const storage = new Map<string, string>([[storageKey, originalKey]])
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
    })
    fetchMock.mockRejectedValue(new TypeError('Response still unknown'))
    await expect(api.mutateSellerProduct(null, 'create', input)).rejects.toThrow('Response still unknown')
    expect(keyAt(0)).toBe(originalKey)
    expect([...storage.entries()]).toEqual([[storageKey, originalKey]])
    expect(JSON.stringify([...storage])).not.toContain(input.draft!.description)
  })

  it('keeps bodyless commands bodyless and does not add a JSON content type', async () => {
    await api.claimTask('task')
    const init = fetchMock.mock.calls[0]?.[1]
    expect(init?.body).toBeUndefined()
    expect(new Headers(init?.headers).has('Content-Type')).toBe(false)
    expect(keyAt(0)).toBeTruthy()
  })

  it('does not send a prepared listing command under a different actor', async () => {
    const pending = api.mutateSellerProduct(null, 'create', { draft: draft() })
    setTaskCommandActor('another-seller')
    await expect(pending).rejects.toThrow('Session changed')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('coalesces matching submitted snapshots even if the first form changes while hashing', async () => {
    let release!: (response: Response) => void
    fetchMock.mockImplementation(() => new Promise(resolve => { release = resolve }))
    const input: ListingMutation = { draft: draft() }
    const original = structuredClone(input)
    const first = api.mutateSellerProduct(null, 'create', input)
    input.draft!.priceCents = 3000
    const second = api.mutateSellerProduct(null, 'create', original)
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    release(new Response(JSON.stringify({ id: productID })))
    expect(await first).toEqual(await second)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(bodyAt(0)).toEqual(original)
  })
})
