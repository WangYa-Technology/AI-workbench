import { ref, watch } from 'vue'
import { defineStore } from 'pinia'
import { api, APIError, messageFrom, type Notification } from '../api/client'
import { useSessionStore } from './session'

type Query = { readState?: 'all' | 'unread' | 'read'; kind?: string }

export const useNotificationsStore = defineStore('notifications', () => {
  const session = useSessionStore()
  const items = ref<Notification[]>([])
  const unreadCount = ref(0)
  const nextCursor = ref('')
  const loading = ref(false)
  const initialized = ref(false)
  const error = ref('')
  const accessDenied = ref(false)
  let context = 0
  let listing = 0
  let counting = 0
  let view = 0
  let query: Query = {}
  const marking = new Set<string>()

  function clear() {
    context++; listing++; counting++; view++; marking.clear()
    items.value = []; unreadCount.value = 0; nextCursor.value = ''; loading.value = false
    initialized.value = false; error.value = ''; accessDenied.value = false; query = {}
  }
  function clearView() {
    listing++; view++; items.value = []; nextCursor.value = ''; loading.value = false; initialized.value = false; query = {}
  }
  watch([() => session.user, () => session.initialized], clear, { flush: 'sync', deep: true })
  const available = () => !!session.user && session.user.status === 'active'
  const current = (version: number) => version === context && available() && !accessDenied.value
  function denyAccess(cause: unknown) {
    clear(); error.value = messageFrom(cause); accessDenied.value = true
  }
  function fail(cause: unknown) {
    if (cause instanceof APIError && [401, 403].includes(cause.status)) denyAccess(cause)
    else error.value = messageFrom(cause)
  }
  async function refreshCount() {
    if (!available()) { clear(); return }
    if (accessDenied.value) return
    const version = context; const count = ++counting
    try {
      const page = await api.listNotifications({ readState: 'unread', limit: 1 })
      if (current(version) && count === counting) unreadCount.value = page.unreadCount
    } catch (cause) { if (current(version) && count === counting) fail(cause) }
  }
  async function load(input: Query = {}) {
    if (!available()) { clear(); return }
    accessDenied.value = false
    query = { ...input }; view++; const version = context; const list = ++listing; const count = ++counting
    items.value = []; nextCursor.value = ''; loading.value = true; error.value = ''
    try {
      const page = await api.listNotifications({ ...query, limit: 30 })
      if (!current(version) || list !== listing) return
      items.value = page.items; nextCursor.value = page.nextCursor || ''
      if (count === counting) unreadCount.value = page.unreadCount
    } catch (cause) { if (current(version) && list === listing) fail(cause) }
    finally { if (current(version) && list === listing) { loading.value = false; initialized.value = true } }
  }
  async function loadMore(input: Query = {}) {
    if (!available() || accessDenied.value || !nextCursor.value || loading.value) return
    if ((input.readState || 'all') !== (query.readState || 'all') || (input.kind || '') !== (query.kind || '')) return
    const version = context; const list = listing; const count = ++counting
    loading.value = true; error.value = ''
    try {
      const page = await api.listNotifications({ ...query, cursor: nextCursor.value, limit: 30 })
      if (!current(version) || list !== listing) return
      const seen = new Set(items.value.map(item => item.id))
      items.value.push(...page.items.filter(item => !seen.has(item.id))); nextCursor.value = page.nextCursor || ''
      if (count === counting) unreadCount.value = page.unreadCount
    } catch (cause) { if (current(version) && list === listing) fail(cause) }
    finally { if (current(version) && list === listing) loading.value = false }
  }
  async function markRead(item: Notification) {
    if (!available() || accessDenied.value || marking.has(item.id) || !items.value.some(row => row === item)) return null
    if (item.readAt) return item
    const version = context; const currentView = view; marking.add(item.id); counting++
    try {
      const updated = await api.markNotificationRead(item.id)
      if (!current(version)) return null
      if (currentView === view) {
        listing++; loading.value = false
        items.value = items.value.flatMap(row => row.id !== updated.id ? [row] : query.readState === 'unread' ? [] : [updated])
        unreadCount.value = Math.max(0, unreadCount.value - 1)
      }
      await refreshCount()
      return current(version) ? updated : null
    } catch (cause) { if (current(version)) fail(cause); return null }
    finally { if (version === context) marking.delete(item.id) }
  }
  async function markAllRead() {
    if (!available() || accessDenied.value || marking.has('*')) return
    const version = context; const currentView = view; marking.add('*'); counting++
    const ids = new Set(items.value.filter(item => !item.readAt).map(item => item.id))
    try {
      await api.markAllNotificationsRead()
      if (!current(version)) return
      if (currentView === view) {
        listing++; loading.value = false
        const now = new Date().toISOString()
        items.value = items.value.flatMap(item => !ids.has(item.id) ? [item] : query.readState === 'unread' ? [] : [{ ...item, readAt: now }])
      }
      await refreshCount()
    } catch (cause) { if (current(version)) fail(cause) }
    finally { if (version === context) marking.delete('*') }
  }
  return { items, unreadCount, nextCursor, loading, initialized, error, accessDenied, refreshCount, load, loadMore, markRead, markAllRead, clear, clearView, denyAccess }
})
