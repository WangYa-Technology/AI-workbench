import { computed, inject, onBeforeUnmount, watch, type InjectionKey, type Ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, APIError } from '../api/client'
import { createScopedApi } from '../lib/scopedApi'
import { useSessionStore } from '../stores/session'

export const adminAccessBoundary: InjectionKey<{
  revision: Readonly<Ref<number>>
  deny: (status: 401 | 403) => void
}> = Symbol('adminAccessBoundary')

export function useAdminContext() {
  const session = useSessionStore()
  const route = useRoute()
  return computed(() => JSON.stringify([
    route.fullPath.split('#')[0], session.initialized, session.user?.id,
    session.user?.status, session.user?.role,
    [...(session.user?.permissions ?? [])].sort(),
  ]))
}

export function useAdminApi(reportAccessDenied = false) {
  const context = useAdminContext()
  const boundary = inject(adminAccessBoundary, null)
  const revision = boundary?.revision.value
  let active = true
  // Synchronous invalidation also covers A -> B -> A before Vue renders, and
  // the interval between a confirmed scope change and component destruction.
  watch(context, () => { active = false }, { flush: 'sync' })
  onBeforeUnmount(() => { active = false })
  return createScopedApi(api, () => active && revision === boundary?.revision.value, error => {
    // Child evidence widgets retain their local denial/retry behavior. A parent
    // denial, however, invalidates every request owned by that workspace at once.
    if (reportAccessDenied && error instanceof APIError && (error.status === 401 || error.status === 403)) {
      boundary?.deny(error.status)
    }
  })
}
