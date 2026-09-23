import { AsyncLocalStorage } from 'node:async_hooks'

const requestSignals = new AsyncLocalStorage()

// The SDK does not expose a signal on each action. Scope cancellation to the
// incoming request so a disconnected caller cannot start its next remote step,
// without aborting other requests that share the same SDK client.
export function withWaffoRequestCancellation(handler) {
  return async (req, res) => {
    const controller = new AbortController()
    const abort = () => controller.abort()
    const close = () => { if (!res.writableEnded) abort() }
    req.once('aborted', abort)
    res.once('close', close)
    if (req.aborted || res.destroyed) abort()
    try {
      return await requestSignals.run(controller.signal, () => handler(req, res))
    } finally {
      // Authenticated checkout uses Promise.all inside the pinned SDK. One
      // failed branch can finish the handler while the other still consumes
      // its response. End every child call with this request's scope, even
      // when res.end() made the normal close listener a no-op. This cannot
      // undo a financial request already accepted by the provider.
      abort()
      req.off('aborted', abort)
      res.off('close', close)
    }
  }
}

// The Go connector response is bounded too, but the SDK parses the remote body
// in this process first. Bound decoded bytes before handing them to its parser.
export const maxWaffoResponseBytes = 1024 * 1024

export function waffoErrorStatus(error) {
  const message = String(error?.message || '').toLowerCase()
  if (message.includes('signature') || message.includes('timestamp')) return 401
  const status = Number(error?.status)
  if ([401, 403, 429].includes(status)) return status
  return status >= 400 && status < 500 ? 422 : 502
}

// A redirect may replay a ticket-creation POST. Preserve caller cancellation
// while enforcing a deadline that also covers consumption of the remote body.
export async function waffoFetch(url, init) {
  const callerSignal = init?.signal ?? (url instanceof Request ? url.signal : undefined)
  const deadline = AbortSignal.timeout(20_000)
  const signal = AbortSignal.any([deadline, callerSignal, requestSignals.getStore()].filter(Boolean))
  const response = await globalThis.fetch(url, { ...init, redirect: 'error', signal })
  // The pinned SDK's unwrapAction checks errors[], not HTTP status. Refuse
  // failed HTTP responses before their data can masquerade as a session,
  // refund ticket or authenticated query result. Do not parse private errors.
  if (!response.ok) {
    await response.body?.cancel()
    const error = new Error('waffo_http_error')
    error.status = response.status
    throw error
  }
  if (!response.body) return response
  const declared = response.headers.get('content-length')
  if (declared !== null && (!/^\d+$/.test(declared) || Number(declared) > maxWaffoResponseBytes)) {
    await response.body.cancel()
    throw new Error('waffo_response_too_large')
  }
  let received = 0
  const body = response.body.pipeThrough(new TransformStream({
    transform(chunk, controller) {
      signal.throwIfAborted()
      received += chunk.byteLength
      if (received > maxWaffoResponseBytes) throw new Error('waffo_response_too_large')
      controller.enqueue(chunk)
    },
  }))
  const headers = new Headers(response.headers)
  // fetch already decoded gzip/br. These transport headers no longer describe
  // the bounded decoded stream; preserve status and application headers.
  headers.delete('content-encoding')
  headers.delete('content-length')
  return new Response(body, { status: response.status, statusText: response.statusText, headers })
}
