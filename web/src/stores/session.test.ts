import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, APIError } from '../api/client'
import { useSessionStore } from './session'

describe('session initialization', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => vi.restoreAllMocks())

  it('does not repeat an anonymous session probe unless explicitly forced', async () => {
    const sessionRequest = vi.spyOn(api, 'session').mockRejectedValue(new APIError(401, {
      code: 'authentication_required',
      message: 'Authentication is required.',
      retryable: false,
    }))
    const session = useSessionStore()

    await expect(session.ensure()).resolves.toBeNull()
    await expect(session.ensure()).resolves.toBeNull()
    expect(sessionRequest).toHaveBeenCalledTimes(1)

    await expect(session.ensure(true)).resolves.toBeNull()
    expect(sessionRequest).toHaveBeenCalledTimes(2)
  })
})
