import { CommandSessionChangedError } from './taskCommands'

// Keep both the dispatch and the continuation inside the view that owns them.
// Disposal never cancels a command already accepted by the server; it prevents
// that command's result from driving more requests or UI in another context.
export function createScopedApi<T extends object>(client: T, isCurrent: () => boolean, onError?: (error: unknown) => void): T {
  const assertCurrent = () => { if (!isCurrent()) throw new CommandSessionChangedError() }
  return new Proxy(client, {
    get(target, key, receiver) {
      const method: unknown = Reflect.get(target, key, receiver)
      if (typeof method !== 'function') return method
      return async (...args: unknown[]) => {
        assertCurrent()
        try {
          const result: unknown = await Reflect.apply(method, target, args)
          assertCurrent()
          return result
        } catch (error) {
          assertCurrent()
          onError?.(error)
          assertCurrent()
          throw error
        }
      }
    },
  })
}
