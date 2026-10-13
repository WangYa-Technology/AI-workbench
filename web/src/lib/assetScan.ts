// Polling is read-only and bounded even when a request hangs. The caller owns
// cancellation and identity/view guards; no late result may update that view.
export async function pollAssetScan<T extends { scanStatus: string }>(options: {
  signal: globalThis.AbortSignal
  read: (signal: globalThis.AbortSignal) => Promise<T>
  update: (item: T) => void
  paused: () => void
  budgetMs?: number
}) {
  const { signal } = options
  const deadline = Date.now() + (options.budgetMs ?? 60_000)
  while (!signal.aborted && Date.now() < deadline) {
    await new Promise<void>(resolve => {
      const finish = () => { globalThis.clearTimeout(timer); signal.removeEventListener('abort', finish); resolve() }
      const timer = globalThis.setTimeout(finish, Math.min(750, deadline - Date.now()))
      signal.addEventListener('abort', finish, { once: true })
    })
    if (signal.aborted || Date.now() >= deadline) break
    const request = new globalThis.AbortController()
    const abort = () => request.abort()
    signal.addEventListener('abort', abort, { once: true })
    const timer = globalThis.setTimeout(abort, Math.min(10_000, deadline - Date.now()))
    try {
      const item = await options.read(request.signal)
      if (signal.aborted) return
      options.update(item)
      if (item.scanStatus !== 'pending') return
    } catch (reason) {
      if (signal.aborted) return
      const status = (reason as { status?: number })?.status
      if (status === 401 || status === 403 || status === 404) break
      // Temporary failures retry within the same time budget.
    } finally {
      globalThis.clearTimeout(timer)
      signal.removeEventListener('abort', abort)
    }
  }
  if (!signal.aborted) options.paused()
}
