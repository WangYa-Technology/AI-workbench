export const TEXT_PREVIEW_BYTES = 64 * 1024

export interface TextPreview { text: string; truncated: boolean }

export function inlineTextPreview(text: string): TextPreview {
  // Bound encoding allocations too; a code unit requires at least one UTF-8 byte.
  const prefix = text.slice(0, TEXT_PREVIEW_BYTES + 1)
  const bytes = new TextEncoder().encode(prefix)
  const truncated = prefix.length < text.length || bytes.length > TEXT_PREVIEW_BYTES
  return { text: new TextDecoder().decode(bytes.subarray(0, TEXT_PREVIEW_BYTES), { stream: truncated }), truncated }
}

export async function readTextPreview(response: Response): Promise<TextPreview> {
  if (!response.ok || !response.headers.get('Content-Type')?.toLowerCase().startsWith('text/') || !response.body) {
    await response.body?.cancel()
    throw new Error('Text preview unavailable')
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let bytes = 0
  let text = ''
  let ended = false
  try {
    while (true) {
      const chunk = await reader.read()
      if (chunk.done) {
        ended = true
        return { text: text + decoder.decode(), truncated: false }
      }
      const remaining = TEXT_PREVIEW_BYTES - bytes
      text += decoder.decode(chunk.value.subarray(0, remaining), { stream: true })
      bytes += Math.min(chunk.value.byteLength, remaining)
      if (chunk.value.byteLength > remaining) {
        // Leave a partial final UTF-8 sequence buffered instead of showing a
        // replacement character for a code point cut at the preview boundary.
        return { text, truncated: true }
      }
    }
  } finally {
    if (!ended) await reader.cancel().catch(() => undefined)
    reader.releaseLock()
  }
}
