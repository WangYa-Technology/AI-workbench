const prefix = 'hcai:community-comment:'
let actor = 'guest'

export function setCommunityDraftActor(id: string | null) {
  const next = id || 'guest'
  try {
    const keys = Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index) || '')
    for (const key of keys) {
      if (key.startsWith('community-draft:')) sessionStorage.removeItem(key)
      if (!key.startsWith(prefix) || key.startsWith(`${prefix}${next}:`)) continue
      // Preserve a visitor's draft only when that visitor signs in.
      if (actor === 'guest' && id && key.startsWith(`${prefix}guest:`)) {
        sessionStorage.setItem(`${prefix}${next}:${key.slice(`${prefix}guest:`.length)}`, sessionStorage.getItem(key) || '')
      }
      sessionStorage.removeItem(key)
    }
  } catch { /* The editor works without browser storage. */ }
  actor = next
}

export function readCommentDraft(postId: string): string {
  try { return sessionStorage.getItem(`${prefix}${actor}:${postId}`) || '' } catch { return '' }
}

export function saveCommentDraft(postId: string, value: string) {
  try {
    const key = `${prefix}${actor}:${postId}`
    if (value) sessionStorage.setItem(key, value)
    else sessionStorage.removeItem(key)
  } catch { /* The editor works without browser storage. */ }
}
