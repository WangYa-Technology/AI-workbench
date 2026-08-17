import { defineStore } from 'pinia'
import { api, messageFrom, type Notification } from '../api/client'
import { useSessionStore } from './session'

export const useNotificationsStore = defineStore('notifications', {
  state: () => ({
    items: [] as Notification[],
    unreadCount: 0,
    nextCursor: '',
    loading: false,
    initialized: false,
    error: '',
  }),
  actions: {
    async refreshCount() {
      const session = useSessionStore()
      if (!session.user) {
        this.clear()
        return
      }
      try {
        const page = await api.listNotifications({ readState: 'unread', limit: 1 })
        this.unreadCount = page.unreadCount
      } catch (error) {
        this.error = messageFrom(error)
      }
    },
    async load(query: { readState?: 'all' | 'unread' | 'read'; kind?: string } = {}) {
      this.loading = true
      this.error = ''
      try {
        const page = await api.listNotifications({ ...query, limit: 30 })
        this.items = page.items
        this.unreadCount = page.unreadCount
        this.nextCursor = page.nextCursor || ''
      } catch (error) {
        this.error = messageFrom(error)
      } finally {
        this.loading = false
        this.initialized = true
      }
    },
    async markRead(item: Notification) {
      if (item.readAt) return item
      try {
        const updated = await api.markNotificationRead(item.id)
        this.items = this.items.map(candidate => candidate.id === updated.id ? updated : candidate)
        this.unreadCount = Math.max(0, this.unreadCount - 1)
        return updated
      } catch (error) {
        this.error = messageFrom(error)
        return null
      }
    },
    async markAllRead() {
      try {
        await api.markAllNotificationsRead()
        const now = new Date().toISOString()
        this.items = this.items.map(item => item.readAt ? item : { ...item, readAt: now })
        this.unreadCount = 0
      } catch (error) {
        this.error = messageFrom(error)
      }
    },
    clear() {
      this.items = []
      this.unreadCount = 0
      this.nextCursor = ''
      this.initialized = false
      this.error = ''
    },
  },
})
