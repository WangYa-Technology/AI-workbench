import { afterEach, expect, it, vi } from 'vitest'
import { readCommentDraft, saveCommentDraft, setCommunityDraftActor } from './communityDrafts'

afterEach(() => vi.unstubAllGlobals())

it('keeps a visitor draft through sign-in and removes private drafts on account changes', () => {
  const data = new Map<string, string>()
  vi.stubGlobal('sessionStorage', {
    get length() { return data.size },
    key: (index: number) => Array.from(data.keys())[index],
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => data.set(key, value),
    removeItem: (key: string) => data.delete(key),
  })
  setCommunityDraftActor(null)
  saveCommentDraft('post', 'Visitor reply')
  setCommunityDraftActor('alice')
  expect(readCommentDraft('post')).toBe('Visitor reply')
  saveCommentDraft('post', 'Private Alice reply')
  setCommunityDraftActor('bob')
  expect(readCommentDraft('post')).toBe('')
  setCommunityDraftActor('alice')
  expect(readCommentDraft('post')).toBe('')
  saveCommentDraft('post', 'Another private reply')
  setCommunityDraftActor(null)
  expect(Array.from(data.values())).not.toContain('Another private reply')
})

it('keeps editing usable when storage is unavailable', () => {
  vi.stubGlobal('sessionStorage', { get length() { throw new Error('unavailable') } })
  expect(() => setCommunityDraftActor('alice')).not.toThrow()
  expect(() => saveCommentDraft('post', 'reply')).not.toThrow()
  expect(readCommentDraft('post')).toBe('')
})
