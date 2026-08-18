import { describe, expect, it } from 'vitest'
import type { CreationCapability } from '../api/client'
import { isCreationCapabilityComplete } from './creationCapabilities'

const capability = (overrides: Partial<CreationCapability> = {}): CreationCapability => ({
  mode: 'image',
  available: true,
  aspectRatios: ['auto', '1:1'],
  qualities: ['auto', 'standard'],
  durationSeconds: [],
  outputFormats: ['jpeg', 'png'],
  resultFormats: ['jpeg', 'png'],
  referenceKinds: ['image'],
  supportsMask: true,
  ...overrides,
})

describe('creation capability projection', () => {
  it('accepts a complete mode contract', () => {
    expect(isCreationCapabilityComplete(capability())).toBe(true)
  })

  it('rejects a successful response with missing arrays', () => {
    const incomplete = capability()
    delete (incomplete as Partial<CreationCapability>).referenceKinds
    expect(isCreationCapabilityComplete(incomplete)).toBe(false)
  })

  it('requires mode-specific controls for video and music', () => {
    expect(isCreationCapabilityComplete(capability({ mode: 'video', durationSeconds: [] }))).toBe(false)
    expect(isCreationCapabilityComplete(capability({ mode: 'music', aspectRatios: [], referenceKinds: [], durationSeconds: [30], outputFormats: ['wav'], resultFormats: ['wav'] }))).toBe(true)
  })
})
