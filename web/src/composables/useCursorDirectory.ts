import { ref, type Ref } from 'vue'

type Page<T> = { items: T[]; nextCursor?: string }

export function useCursorDirectory<T extends { id: string }>(fetchPage: (cursor?: string) => Promise<Page<T>>) {
  const items = ref<T[]>([]) as Ref<T[]>
  const nextCursor = ref<string | null>(null)
  const loadingMore = ref(false)

  async function load(cursor = '') {
    const page = await fetchPage(cursor || undefined)
    if (!cursor) {
      items.value = page.items
    } else {
      const known = new Set(items.value.map(item => item.id))
      items.value = [...items.value, ...page.items.filter(item => !known.has(item.id))]
    }
    nextCursor.value = page.nextCursor || null
  }

  async function loadMore() {
    if (!nextCursor.value || loadingMore.value) return
    loadingMore.value = true
    try {
      await load(nextCursor.value)
    } finally {
      loadingMore.value = false
    }
  }

  return { items, nextCursor, loadingMore, load, loadMore }
}
