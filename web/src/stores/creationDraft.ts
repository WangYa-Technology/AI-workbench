import { defineStore } from 'pinia'

export type CreationDraftMode = 'chat' | 'image' | 'video' | 'music'
export type CreationDraftView = 'gallery' | 'guide'

export interface CreationOutputSettings {
  ratio: 'auto' | '1:1' | '4:5' | '16:9'
  quality: 'auto' | 'standard' | 'high'
  format: 'png' | 'jpeg' | 'mp4' | 'wav' | 'txt'
  count: number
  duration: number
  responseLength: 'short' | 'balanced' | 'detailed'
}

export interface CreationDraftSnapshot {
  prompt: string
  view: CreationDraftView
  settings: CreationOutputSettings
}

function cloneSnapshot(snapshot: CreationDraftSnapshot): CreationDraftSnapshot {
  return { ...snapshot, settings: { ...snapshot.settings } }
}

export const useCreationDraftStore = defineStore('creation-draft', {
  state: () => ({
    drafts: {} as Partial<Record<CreationDraftMode, CreationDraftSnapshot>>,
  }),
  actions: {
    save(mode: CreationDraftMode, snapshot: CreationDraftSnapshot) {
      this.drafts[mode] = cloneSnapshot(snapshot)
    },
    restore(mode: CreationDraftMode) {
      const snapshot = this.drafts[mode]
      return snapshot ? cloneSnapshot(snapshot) : null
    },
    clear(mode: CreationDraftMode) {
      delete this.drafts[mode]
    },
  },
})
