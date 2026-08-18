import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useCreationDraftStore, type CreationDraftSnapshot } from './creationDraft'

const snapshot = (): CreationDraftSnapshot => ({
  prompt: 'A precise product study',
  view: 'guide',
  settings: {
    ratio: '4:5',
    quality: 'high',
    format: 'png',
    count: 2,
    duration: 10,
    responseLength: 'balanced',
  },
})

describe('multimodal creation draft handoff', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('preserves a private in-memory draft across authentication navigation', () => {
    const store = useCreationDraftStore()
    const input = snapshot()
    store.save('image', input)

    input.prompt = 'Changed after saving'
    input.settings.count = 4
    const restored = store.restore('image')
    expect(restored?.prompt).toBe('A precise product study')
    expect(restored?.settings.count).toBe(2)

    if (restored) restored.settings.quality = 'standard'
    expect(store.restore('image')?.settings.quality).toBe('high')
  })

  it('keeps each creation mode independent', () => {
    const store = useCreationDraftStore()
    store.save('image', snapshot())
    store.save('video', { ...snapshot(), prompt: 'A slow product reveal', settings: { ...snapshot().settings, format: 'mp4' } })

    expect(store.restore('image')?.prompt).toBe('A precise product study')
    expect(store.restore('video')?.prompt).toBe('A slow product reveal')
  })
})
