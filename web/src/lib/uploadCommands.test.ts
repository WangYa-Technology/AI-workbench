import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { runUploadCommand, setTaskCommandActor } from './taskCommands'
import { api } from '../api/client'

const pending = new Map<string, string>()
const storage = {
  get length() { return pending.size },
  key: (index: number) => Array.from(pending.keys())[index] ?? null,
  getItem: (key: string) => pending.get(key) ?? null,
  setItem: (key: string, value: string) => { pending.set(key, value) },
  removeItem: (key: string) => { pending.delete(key) },
  clear: () => pending.clear(),
}
const form = (body = 'Private file bytes', title = 'Private upload title') => {
  const value = new FormData()
  value.append('title', title)
  value.append('file', new File([body], 'same-name.txt', { type: 'text/plain' }))
  return value
}
const uncertain = () => false
beforeEach(() => {
  pending.clear()
  vi.stubGlobal('sessionStorage', storage)
  setTaskCommandActor(null)
  setTaskCommandActor('upload-owner')
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('shared upload command recovery', () => {
  it('reuses a pending key after form reconstruction and page-module reload, without persisting private contents', async () => {
    let original = ''
    await expect(runUploadCommand('/assets/uploads', form(), async key => { original = key; throw new TypeError('Lost response') }, uncertain)).rejects.toThrow()
    expect(pending.size).toBe(1)
    expect(JSON.stringify(Array.from(pending))).not.toContain('Private')
    expect(JSON.stringify(Array.from(pending))).not.toContain('same-name.txt')
    vi.resetModules()
    const reloaded = await import('./taskCommands')
    reloaded.setTaskCommandActor('upload-owner')
    const keys: string[] = []
    await reloaded.runUploadCommand('/assets/uploads', form(), async key => { keys.push(key); return 'restored' }, uncertain)
    expect(keys).toEqual([original])
    expect(pending.size).toBe(0)
    await reloaded.runUploadCommand('/assets/uploads', form(), async key => { keys.push(key); return 'new' }, uncertain)
    expect(keys[1]).not.toBe(original)
  })
  it('binds bytes, title, note, route and account rather than file name or size', async () => {
    const keys: string[] = []
    const fail = async (key: string) => { keys.push(key); throw new Error('uncertain') }
    await runUploadCommand('/assets/uploads', form('one'), fail, uncertain).catch(() => {})
    await runUploadCommand('/assets/uploads', form('two'), fail, uncertain).catch(() => {})
    await runUploadCommand('/assets/uploads', form('one', 'Other title'), fail, uncertain).catch(() => {})
    const version = form('one'); version.append('note', 'First revision')
    await runUploadCommand('/assets/a/versions', version, fail, uncertain).catch(() => {})
    version.set('note', 'Second revision')
    await runUploadCommand('/assets/a/versions', version, fail, uncertain).catch(() => {})
    await runUploadCommand('/assets/b/versions', version, fail, uncertain).catch(() => {})
    setTaskCommandActor('another-owner')
    await runUploadCommand('/assets/uploads', form('one'), fail, uncertain).catch(() => {})
    expect(new Set(keys).size).toBe(7)
  })
  it('coalesces concurrent identical uploads and snapshots caller mutations', async () => {
    const original = form()
    let calls = 0
    const send = async (_key: string, snapshot: FormData) => {
      calls++
      expect(snapshot.get('title')).toBe('Private upload title')
      expect(await (snapshot.get('file') as File).text()).toBe('Private file bytes')
      await new Promise(resolve => setTimeout(resolve, 20))
      return 'asset-id'
    }
    const first = runUploadCommand('/assets/uploads', original, send, uncertain)
    original.set('title', 'Changed during hashing')
    const second = runUploadCommand('/assets/uploads', form(), send, uncertain)
    expect(await Promise.all([first, second])).toEqual(['asset-id', 'asset-id'])
    expect(calls).toBe(1)
  })
  it('does not dispatch if the account changes while bytes are hashed', async () => {
    const digest = crypto.subtle.digest.bind(crypto.subtle)
    vi.spyOn(crypto.subtle, 'digest').mockImplementationOnce(async (algorithm, data) => {
      setTaskCommandActor('changed-owner')
      return digest(algorithm, data)
    })
    const send = vi.fn()
    await expect(runUploadCommand('/assets/uploads', form(), send, uncertain)).rejects.toThrow('Session changed')
    expect(send).not.toHaveBeenCalled()
  })
  it('does not merge a new login with an old in-flight request or erase its retry key', async () => {
    let release!: (value: string) => void
    let firstKey = ''
    const first = runUploadCommand('/assets/uploads', form(), async key => {
      firstKey = key
      return new Promise<string>(resolve => { release = resolve })
    }, uncertain)
    await vi.waitFor(() => expect(firstKey).not.toBe(''))
    setTaskCommandActor(null)
    setTaskCommandActor('upload-owner')
    let nextKey = ''
    await expect(runUploadCommand('/assets/uploads', form(), async key => {
      nextKey = key
      throw new TypeError('New session response lost')
    }, uncertain)).rejects.toThrow()
    expect(nextKey).not.toBe(firstKey)
    release('old-session-asset')
    await expect(first).rejects.toThrow('Session changed')
    expect(Array.from(pending.values())).toContain(nextKey)
  })
  it('sends actual multipart bodies and the same header after a lost HTTP response', async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError('Connection interrupted'))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'same-asset' }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(api.uploadAsset(form())).rejects.toThrow()
    expect(await api.uploadAsset(form())).toEqual({ id: 'same-asset' })
    const first = fetchMock.mock.calls[0]![1]!
    const second = fetchMock.mock.calls[1]![1]!
    expect(new Headers(first.headers).get('Idempotency-Key')).toBe(new Headers(second.headers).get('Idempotency-Key'))
    expect(new Headers(second.headers).has('Content-Type')).toBe(false)
    expect(second.body).toBeInstanceOf(FormData)
    expect(pending.size).toBe(0)
  })
})
