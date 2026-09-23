// Retain only opaque operation keys and payload hashes, never form contents.
const prefix = 'hcai:pending-task:'
let actor = ''
let actorEpoch = 0
const sessionTransitions = new Set<symbol>()
const inflight = new Map<string, Promise<unknown>>()
const memory = new Map<string, string>()
// Monetary commands retain their opaque keys across reauthentication in this
// tab. A denied retry cannot establish whether an earlier request committed.
const financeMemory = new Map<string, string>()
const financePrefix = 'hcai:pending-finance:'

export class CommandSessionChangedError extends Error {
  constructor() { super('Session changed while preparing the request.') }
}

export function beginCommandSessionTransition(): () => void {
  actorEpoch++
  const token = Symbol('session-transition')
  sessionTransitions.add(token)
  // Independent tokens keep an older completion from opening a newer change.
  return () => { sessionTransitions.delete(token) }
}

export function assertCommandSessionReady() {
  if (sessionTransitions.size) throw new CommandSessionChangedError()
}

export function setTaskCommandActor(id: string | null) {
  if (actor !== (id || '') || !id) actorEpoch++
  actor = id || ''
  if (!id) {
    memory.clear()
    try {
      for (let i = sessionStorage.length - 1; i >= 0; i--) {
        const key = sessionStorage.key(i)
        if (key?.startsWith(prefix)) sessionStorage.removeItem(key)
      }
    } catch { /* Storage may be unavailable in a restricted browser. */ }
  }
}

export async function runTaskCommand<T>(
  path: string, method: string, body: unknown,
  send: (key: string) => Promise<T>, definitiveFailure: (error: unknown) => boolean,
): Promise<T> {
  return runScopedCommand(actor, actorEpoch, path, method, body, send, definitiveFailure)
}

// Freeze the wire representation before the asynchronous digest. Both the key
// and the HTTP body must describe the same submission, including nested arrays.
// Parsing the snapshot preserves existing JSON payload fingerprints; storing the
// serialized string as the identity instead would discard pending legacy keys.
export async function runJSONCommand<T>(
  path: string, method: string, body: unknown,
  send: (key: string, serialized: string | undefined) => Promise<T>,
  definitiveFailure: (error: unknown) => boolean,
): Promise<T> {
  const scope = actor
  const epoch = actorEpoch
  const serialized = JSON.stringify(body)
  const snapshot = serialized === undefined ? undefined : JSON.parse(serialized)
  return runScopedCommand(scope, epoch, path, method, snapshot, key => send(key, serialized), definitiveFailure)
}

async function runScopedCommand<T>(
  scope: string, epoch: number, path: string, method: string, body: unknown,
  send: (key: string) => Promise<T>, definitiveFailure: (error: unknown) => boolean,
  storagePrefix = prefix, pendingKeys = memory,
): Promise<T> {
  const assertSession = () => {
    assertCommandSessionReady()
    if (scope !== actor || epoch !== actorEpoch) throw new CommandSessionChangedError()
  }
  assertSession()
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify([scope, path, method, body])))
  assertSession()
  const storageKey = storagePrefix + Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
  const inflightKey = `${epoch}:${storageKey}`
  const existing = inflight.get(inflightKey)
  if (existing) return existing as Promise<T>
  let key = pendingKeys.get(storageKey)
  try { key ||= sessionStorage.getItem(storageKey) || undefined } catch { /* Use the in-memory key. */ }
  key ||= crypto.randomUUID()
  pendingKeys.set(storageKey, key)
  try { sessionStorage.setItem(storageKey, key) } catch { /* Use the in-memory key. */ }
  const forget = () => {
    if (pendingKeys.get(storageKey) === key) pendingKeys.delete(storageKey)
    try {
      if (sessionStorage.getItem(storageKey) === key) sessionStorage.removeItem(storageKey)
    } catch { /* No persistent storage. */ }
  }
  const operation = Promise.resolve().then(() => { assertSession(); return send(key) }).then(result => {
    // A response accepted under an old session is not an acknowledgement by
    // the current caller. Retain its key so a retry can confirm the original
    // operation, and never expose the old result to the new session.
    assertSession()
    forget()
    return result
  }, error => {
    assertSession()
    if (definitiveFailure(error)) forget()
    throw error
  }).finally(() => inflight.delete(inflightKey))
  inflight.set(inflightKey, operation)
  return operation
}

// Community and task writes use the same user-scoped retry protocol.
export const runIdempotentCommand = runTaskCommand

export function runFinanceCommand<T>(path: string, body: unknown, send: (key: string) => Promise<T>): Promise<T> {
  return runScopedCommand(actor, actorEpoch, path, 'POST', body, send, () => false, financePrefix, financeMemory)
}

// File bytes participate in identity; name and size alone cannot distinguish
// replacements. Snapshot the form before hashing so caller mutations cannot
// change the request after its key was selected. Persist hashes/opaque keys only.
export async function runUploadCommand<T>(
  path: string, form: FormData, send: (key: string, snapshot: FormData) => Promise<T>,
  definitiveFailure: (error: unknown) => boolean,
): Promise<T> {
  const scope = actor
  const epoch = actorEpoch
  const snapshot = new FormData()
  for (const [name, value] of form.entries()) snapshot.append(name, value)
  const identity: unknown[] = []
  for (const [name, value] of Array.from(snapshot.entries()).sort(([a], [b]) => a.localeCompare(b))) {
    if (typeof value === 'string') identity.push([name, value.trim()])
    else {
      const digest = await crypto.subtle.digest('SHA-256', await value.arrayBuffer())
      identity.push([name, value.name, value.size, Array.from(new Uint8Array(digest), b => b.toString(16).padStart(2, '0')).join('')])
    }
  }
  return runScopedCommand(scope, epoch, path, 'POST', identity, key => send(key, snapshot), definitiveFailure)
}
