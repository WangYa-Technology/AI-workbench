import { ref, type Ref } from 'vue'

type Page<T> = { items: T[]; nextCursor?: string }

export function mergeUniqueBy<T>(current: T[], incoming: T[], identity: (item: T) => string): T[] {
  const known = new Set(current.map(identity))
  return [...current, ...incoming.filter(item => !known.has(identity(item)))]
}

export function useCursorDirectory<T>(fetchPage: (cursor?: string) => Promise<Page<T>>, identity: (item: T) => string = (item) => (item as { id: string }).id) {
  const items = ref<T[]>([]) as Ref<T[]>
  const nextCursor = ref<string | null>(null)
  const loadingMore = ref(false)

  async function load(cursor = '') {
    const page = await fetchPage(cursor || undefined)
    if (!cursor) {
      items.value = page.items
    } else {
      items.value = mergeUniqueBy(items.value, page.items, identity)
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
