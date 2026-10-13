import { afterEach, describe, expect, it, vi } from 'vitest'
import { checkLicensedDownload, DownloadCheckError } from './licensedDownload'

afterEach(() => vi.unstubAllGlobals())
describe('licensed download preflight', () => {
  it('requests one byte and cancels an unbounded response without buffering', async () => {
    let reads = 0, cancelled = false
    const response = new Response(new ReadableStream({ pull() { reads++ }, cancel() { cancelled = true } }, { highWaterMark: 0 }), { status: 200 })
    const fetch = vi.fn().mockResolvedValue(response)
    vi.stubGlobal('fetch', fetch)
    const controller = new AbortController()
    await checkLicensedDownload('/api/v1/assets/test/content', controller.signal)
    expect(fetch).toHaveBeenCalledWith('/api/v1/assets/test/content', expect.objectContaining({ signal: controller.signal, cache: 'no-store', credentials: 'include', headers: { Range: 'bytes=0-0' } }))
    expect(reads).toBe(0)
    expect(cancelled).toBe(true)
  })
  it.each([[401, 'accessChanged'], [403, 'accessChanged'], [404, 'fileMissing'], [500, 'downloadUnavailable'], [416, 'downloadUnavailable']])('maps %s and closes the response', async (status, reason) => {
    let closed = false
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new ReadableStream({ cancel() { closed = true } }, { highWaterMark: 0 }), { status: Number(status) })))
    await expect(checkLicensedDownload('/api/v1/assets/test/content', new AbortController().signal)).rejects.toEqual(new DownloadCheckError(String(reason) as 'accessChanged'))
    expect(closed).toBe(true)
  })
})
