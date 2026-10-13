import { describe, expect, it } from 'vitest'
import type { Asset } from '../api/client'
import { assetDisclosure } from './assetDisclosure'
import { licenseKind, licenseLabel, licenseExplanation } from './contentPresentation'
import { textWithin } from './unicodeText'

describe('asset provenance presentation', () => {
  for (const provider of ['local_test', 'openai']) it(`discloses ${provider} without billing claims`, () => {
    const asset = { sourceType: 'generation', provenance: { generation: { provider, modelName: 'fixture-model' } } } as Asset
    expect(assetDisclosure(asset)).toContain(provider)
    expect(assetDisclosure(asset)).toContain('fixture-model')
    expect(assetDisclosure(asset)).not.toMatch(/paid|charge|付费|收费/)
  })
  it('requires a statement for uploaded and unknown sources', () => {
    expect(assetDisclosure(null)).toBe('')
    expect(assetDisclosure({ sourceType: 'upload' } as Asset)).toBe('')
    expect(assetDisclosure({ sourceType: 'generation' } as Asset)).toBe('')
  })
  for (const [code, kind] of Object.entries({ 'creator-owned': 'owned', 'creator-owned-local-test': 'owned', 'hcai-personal-v1': 'personal', 'hcai-commercial-v1': 'commercial', 'hcai-commercial-standard-v1': 'commercial', 'task-contract': 'task', future: 'review' })) {
    it(`explains ${code} conservatively`, () => {
      expect(licenseKind(code)).toBe(kind)
      expect(licenseLabel(code)).not.toContain('content.')
      expect(licenseExplanation(code)).not.toContain('content.')
    })
  }
  for (const value of ['a', '图', '😀', 'e\u0301']) it(`counts codepoints for ${value}`, () => {
    const boundary = value.repeat(value.length === 2 && value !== '😀' ? 60 : 120)
    expect(textWithin(boundary, 3, 120)).toBe(true)
    expect(textWithin(boundary + 'x', 3, 120)).toBe(false)
  })
})
