import { setCommunityDraftActor } from '../lib/communityDrafts'
import { defineStore } from 'pinia'
import { beginCommandSessionTransition, CommandSessionChangedError, setTaskCommandActor } from '../lib/taskCommands'
import { api, APIError, messageFrom, type LoginRequest, type ProfileUpdate, type UnifiedAuthConfirmRequest, type UnifiedAuthRegisterRequest, type User } from '../api/client'

interface SessionState {
  user: User | null
  initialized: boolean
  loading: boolean
  error: string
}
interface SessionRequests {
  revision: number
  probe: Promise<User | null> | null
  mutation: Promise<User | null> | null
}
// Promises belong to a store instance, not to every Pinia instance in the module.
const requests = new WeakMap<SessionState, SessionRequests>()
function requestsFor(store: SessionState) {
  let current = requests.get(store)
  if (!current) {
    current = { revision: 0, probe: null, mutation: null }
    requests.set(store, current)
  }
  return current
}
function acceptUser(store: SessionState, user: User | null) {
  store.user = user
  setTaskCommandActor(user?.id || null)
  setCommunityDraftActor(user?.id || null)
}

function mutateSession(store: SessionState, request: () => Promise<User | null>, identityChange = true): Promise<User | null> {
  const state = requestsFor(store)
  const revision = ++state.revision
  const previous = state.mutation
  const releaseCommands = identityChange ? beginCommandSessionTransition() : () => {}
  state.probe = null
  store.loading = true
  store.error = ''
  // Cookie-changing requests must finish in order. Ignoring an old JSON result
  // alone would not stop its late Set-Cookie from replacing the current session.
  const operation = Promise.resolve().then(async () => {
    if (previous) await previous
    if (revision !== state.revision) return null
    try {
      const user = await request()
      if (revision !== state.revision) return null
      acceptUser(store, user)
      store.initialized = true
      return user
    } catch (error) {
      if (revision === state.revision) {
        store.error = messageFrom(error)
        if (identityChange) {
          // A failed body/connection does not establish which cookie the browser
          // accepted. Discard the cached actor and probe before allowing writes.
          acceptUser(store, null)
          store.initialized = false
          try {
            const current = await api.session()
            if (revision === state.revision) acceptUser(store, current.user)
          } catch {
            // Without a verified response stay anonymous; preserve the original
            // authentication error instead of claiming a confirmed sign-out.
          } finally {
            if (revision === state.revision) store.initialized = true
          }
        }
      }
      return null
    }
  }).finally(() => {
    releaseCommands()
    if (state.mutation === operation) state.mutation = null
    if (revision === state.revision) store.loading = false
  })
  state.mutation = operation
  return operation
}

export const useSessionStore = defineStore('session', {
  state: () => ({
    user: null as User | null,
    initialized: false,
    loading: false,
    error: '',
  }),
  actions: {
    async ensure(force = false) {
      const state = requestsFor(this)
      if (state.mutation) {
        // Wait for the latest queued identity change before returning a user.
        while (state.mutation) await state.mutation
        return this.user
      }
      if (this.initialized && !force) return this.user
      if (state.probe) return state.probe
      const revision = state.revision
      this.loading = true
      this.error = ''
      const operation = (async () => {
        try {
          const current = await api.session()
          if (revision !== state.revision) return null
          acceptUser(this, current.user)
          return this.user
        } catch (error) {
          if (revision !== state.revision) return null
          if (error instanceof APIError && error.status === 401) {
            acceptUser(this, null)
            return null
          }
          this.error = messageFrom(error)
          return null
        } finally {
          if (revision === state.revision) {
            this.loading = false
            this.initialized = true
          }
        }
      })().finally(() => {
        if (state.probe === operation) state.probe = null
      })
      state.probe = operation
      return operation
    },
    async login(input: LoginRequest) {
      const snapshot = { ...input }
      return this.establish(() => api.login(snapshot))
    },
    async loginWithCode(input: UnifiedAuthConfirmRequest) {
      const snapshot = { ...input }
      return this.establish(() => api.unifiedAuthLoginCode(snapshot))
    },
    async registerWithCode(input: UnifiedAuthRegisterRequest) {
      const snapshot = { ...input }
      return this.establish(() => api.unifiedAuthRegister(snapshot))
    },
    async establish(request: () => ReturnType<typeof api.login>) {
      return mutateSession(this, async () => (await request()).user)
    },
    async updateProfile(input: ProfileUpdate) {
      // A queued login can change the browser cookie before a profile request
      // starts. Never send an old account's form behind that identity change.
      if (requestsFor(this).mutation) return null
      const actor = this.user?.id
      const snapshot = { ...input }
      return mutateSession(this, async () => {
        if (!actor || this.user?.id !== actor) throw new CommandSessionChangedError()
        const result = await api.updateProfile(snapshot)
        if (result.user.id !== actor) throw new CommandSessionChangedError()
        return result.user
      }, false)
    },
    async logout() {
      const state = requestsFor(this)
      const revision = state.revision + 1
      await mutateSession(this, async () => {
        await api.logout()
        return null
      })
      return state.revision === revision && !this.user && !this.error
    },
    clear() {
      const state = requestsFor(this)
      state.revision++
      state.probe = null
      acceptUser(this, null)
      this.initialized = true
      this.loading = false
      this.error = ''
    },
  },
})
