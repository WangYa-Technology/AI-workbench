import { describe, expect, it } from 'vitest'
import { validRefundReason } from './productRefund'

describe('refund reason Unicode contract', () => {
  it.each(['a', '界', '😀'])('counts %s by code point after trimming', (character) => {
    expect(validRefundReason(character.repeat(9))).toBe(false)
    expect(validRefundReason(` \t${character.repeat(10)}\n`)).toBe(true)
    expect(validRefundReason(character.repeat(500))).toBe(true)
    expect(validRefundReason(character.repeat(501))).toBe(false)
  })
  it('rejects NUL and isolated surrogate characters', () => {
    expect(validRefundReason('a'.repeat(10) + '\0')).toBe(false)
    expect(validRefundReason('a'.repeat(10) + '\uD800')).toBe(false)
    expect(validRefundReason('a'.repeat(10) + '\uDC00')).toBe(false)
  })
})
