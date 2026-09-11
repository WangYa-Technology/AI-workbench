import { defineStore } from 'pinia'
import { api, APIError, messageFrom, type LoginRequest, type ProfileUpdate, type RegisterRequest, type UnifiedAuthConfirmRequest, type UnifiedAuthRegisterRequest, type User } from '../api/client'

let pendingSession: Promise<User | null> | null = null

export const useSessionStore = defineStore('session', {
  state: () => ({
    user: null as User | null,
    initialized: false,
    loading: false,
    error: '',
  }),
  actions: {
    async ensure(force = false) {
      if (this.initialized && !force) return this.user
      if (pendingSession) return pendingSession
      this.loading = true
      this.error = ''
      pendingSession = (async () => {
        try {
          const current = await api.session()
          this.user = current.user
          return this.user
        } catch (error) {
          if (error instanceof APIError && error.status === 401) {
            this.user = null
            return null
          }
          this.error = messageFrom(error)
          return null
        } finally {
          this.loading = false
          this.initialized = true
          pendingSession = null
        }
      })()
      return pendingSession
    },
    async login(input: LoginRequest) {
      return this.establish(() => api.login(input))
    },
    async loginWithCode(input: UnifiedAuthConfirmRequest) {
      return this.establish(() => api.unifiedAuthLoginCode(input))
    },
    async register(input: RegisterRequest) {
      return this.establish(() => api.register(input))
    },
    async registerWithCode(input: UnifiedAuthRegisterRequest) {
      return this.establish(() => api.unifiedAuthRegister(input))
    },
    async establish(request: () => ReturnType<typeof api.login>) {
      this.loading = true
      this.error = ''
      try {
        const current = await request()
        this.user = current.user
        this.initialized = true
        return this.user
      } catch (error) {
        this.error = messageFrom(error)
        return null
      } finally {
        this.loading = false
      }
    },
    async startDemoSession(actor: 'creator' | 'publisher' | 'admin' = 'creator') {
      return this.establish(() => api.startDemoSession(actor))
    },
    async switchDemoActor(actor: 'creator' | 'publisher' | 'admin') {
      return this.startDemoSession(actor)
    },
    async updateProfile(input: ProfileUpdate) {
      this.loading = true
      this.error = ''
      try {
        const result = await api.updateProfile(input)
        this.user = result.user
        return result.user
      } catch (error) {
        this.error = messageFrom(error)
        return null
      } finally {
        this.loading = false
      }
    },
    async logout() {
      this.loading = true
      this.error = ''
      try {
        await api.logout()
        this.user = null
        this.initialized = true
      } catch (error) {
        this.error = messageFrom(error)
      } finally {
        this.loading = false
      }
    },
    clear() {
      this.user = null
      this.initialized = true
      this.error = ''
    },
  },
})
