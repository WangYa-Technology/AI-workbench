import { describe, expect, it } from 'vitest'
import { inlineTextPreview, readTextPreview, TEXT_PREVIEW_BYTES } from './textPreview'

function textResponse(bytes: Uint8Array, chunkSize: number) {
  let offset = 0
  let cancelled = false
  const body = new ReadableStream<Uint8Array>({
    pull(controller) {
      if (offset === bytes.length) { controller.close(); return }
      controller.enqueue(bytes.subarray(offset, offset + chunkSize))
      offset = Math.min(bytes.length, offset + chunkSize)
    },
    cancel() { cancelled = true },
  }, { highWaterMark: 0 })
  return { response: new Response(body, { headers: { 'Content-Type': 'text/plain; charset=utf-8' } }),
    consumed: () => offset, cancelled: () => cancelled }
}

describe('bounded text previews', () => {
  it('decodes UTF-8 split across network chunks without corrupting characters', async () => {
    const text = '中文 🧑🏽‍💻 café\n'
    const source = textResponse(new TextEncoder().encode(text), 1)
    expect(await readTextPreview(source.response)).toEqual({ text, truncated: false })
    expect(source.cancelled()).toBe(false)
  })

  for (const size of [0, TEXT_PREVIEW_BYTES - 1, TEXT_PREVIEW_BYTES]) {
    it(`keeps an entire ${size}-byte file without a false truncation notice`, async () => {
      const source = textResponse(new TextEncoder().encode('a'.repeat(size)), 1024)
      expect(await readTextPreview(source.response)).toEqual({ text: 'a'.repeat(size), truncated: false })
    })
  }

  it('stops and cancels a large response even when the server ignores Range', async () => {
    const source = textResponse(new TextEncoder().encode('a'.repeat(TEXT_PREVIEW_BYTES * 5)), 1024)
    expect(await readTextPreview(source.response)).toEqual({ text: 'a'.repeat(TEXT_PREVIEW_BYTES), truncated: true })
    expect(source.cancelled()).toBe(true)
    expect(source.consumed()).toBeLessThanOrEqual(TEXT_PREVIEW_BYTES + 1024)
  })

  it('does not display a partial final UTF-8 character at the byte boundary', async () => {
    const prefix = 'a'.repeat(TEXT_PREVIEW_BYTES - 1)
    const source = textResponse(new TextEncoder().encode(prefix + '中文'), 1024)
    expect(await readTextPreview(source.response)).toEqual({ text: prefix, truncated: true })
    expect(source.cancelled()).toBe(true)
  })

  for (const contentType of ['application/zip', 'application/octet-stream', '']) {
    it(`rejects ${contentType || 'missing MIME'} without reading binary data`, async () => {
      let cancelled = false
      let read = false
      const body = new ReadableStream({ pull() { read = true }, cancel() { cancelled = true } }, { highWaterMark: 0 })
      await expect(readTextPreview(new Response(body, { headers: { 'Content-Type': contentType } }))).rejects.toThrow('Text preview unavailable')
      expect(read).toBe(false)
      expect(cancelled).toBe(true)
    })
  }

  it('rejects revoked access and partial network failures instead of presenting them as complete', async () => {
    await expect(readTextPreview(new Response('Denied', { status: 403, headers: { 'Content-Type': 'text/plain' } }))).rejects.toThrow()
    let reads = 0
    const body = new ReadableStream<Uint8Array>({ pull(controller) {
      if (reads++ === 0) controller.enqueue(new TextEncoder().encode('Incomplete'))
      else controller.error(new Error('Connection lost'))
    } })
    await expect(readTextPreview(new Response(body, { headers: { 'Content-Type': 'text/plain' } }))).rejects.toThrow('Connection lost')
    expect(body.locked).toBe(false)
  })

  it('also bounds inline text by UTF-8 bytes and preserves complete code points', () => {
    expect(inlineTextPreview('Short text')).toEqual({ text: 'Short text', truncated: false })
    const preview = inlineTextPreview('中'.repeat(TEXT_PREVIEW_BYTES))
    expect(preview.truncated).toBe(true)
    expect(preview.text).toBe('中'.repeat(Math.floor(TEXT_PREVIEW_BYTES / 3)))
    expect(new TextEncoder().encode(preview.text).length).toBeLessThanOrEqual(TEXT_PREVIEW_BYTES)
  })
})
