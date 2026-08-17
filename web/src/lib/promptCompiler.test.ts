import { describe, expect, it } from 'vitest'
import { compilePrompt, MAX_COMPILED_PROMPT_LENGTH } from './promptCompiler'

describe('prompt compiler', () => {
  it('keeps the primary prompt ahead of optional configuration', () => {
    expect(compilePrompt('  Primary direction  ', ['Tone: precise', '', 'Safety review'])).toBe(
      'Primary direction\n\nTone: precise\n\nSafety review',
    )
  })

  it('preserves a full-length primary prompt and excludes overflowing metadata', () => {
    const primary = 'x'.repeat(MAX_COMPILED_PROMPT_LENGTH)
    const result = compilePrompt(primary, ['Tone: precise'])

    expect(result).toBe(primary)
    expect(result).toHaveLength(MAX_COMPILED_PROMPT_LENGTH)
  })

  it('skips oversized metadata while retaining later sections that fit', () => {
    expect(compilePrompt('Primary', ['Oversized metadata', 'Tone: precise'], 24)).toBe(
      'Primary\n\nTone: precise',
    )
  })
})
