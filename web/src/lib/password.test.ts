import { describe, expect, it } from 'vitest'
import { validNewPassword } from './password'

describe('new password boundaries', () => {
  it.each([
    ['a'.repeat(9), false], ['a'.repeat(10), true],
    ['a'.repeat(72), true], ['a'.repeat(73), false],
    ['密'.repeat(9), false], ['密'.repeat(24), true], ['密'.repeat(25), false],
    ['🔑'.repeat(18), true], ['🔑'.repeat(19), false],
  ])('validates %s', (value, valid) => expect(validNewPassword(value as string)).toBe(valid))
})
