import { getCurrentScope, onScopeDispose, ref, type Ref } from 'vue'

type Page<T> = { items: T[]; nextCursor?: string }

export function mergeUniqueBy<T>(current: T[], incoming: T[], identity: (item: T) => string): T[] {
  const known = new Set(current.map(identity))
  return [...current, ...incoming.filter(item => !known.has(identity(item)))]
}

export function useCursorDirectory<T>(fetchPage: (cursor?: string) => Promise<Page<T>>, identity: (item: T) => string = (item) => (item as { id: string }).id) {
  const items = ref<T[]>([]) as Ref<T[]>
  const nextCursor = ref<string | null>(null)
  const loading = ref(false)
  const loadingMore = ref(false)
  let generation = 0
  let disposed = false

  function reset() {
    ++generation
    items.value = []
    nextCursor.value = null
    loading.value = loadingMore.value = false
  }

  async function load(cursor = '') {
    if (disposed) return
    if (cursor && loading.value) return
    const current = ++generation
    if (!cursor) {
      items.value = []
      nextCursor.value = null
    }
    loading.value = true
    loadingMore.value = Boolean(cursor)
    try {
      const page = await fetchPage(cursor || undefined)
      if (current !== generation) return
      items.value = cursor ? mergeUniqueBy(items.value, page.items, identity) : page.items
      nextCursor.value = page.nextCursor || null
    } catch (error) {
      if (current === generation) throw error
    } finally {
      if (current === generation) loading.value = loadingMore.value = false
    }
  }

  async function loadMore() {
    if (!nextCursor.value || loading.value) return
    await load(nextCursor.value)
  }

  if (getCurrentScope()) onScopeDispose(() => { disposed = true; reset() })
  return { items, nextCursor, loading, loadingMore, load, loadMore, reset }
}
