import { expect, it } from 'vitest'
import { textLength, textWithin } from './unicodeText'

it('counts Unicode code points consistently across Chinese and emoji', () => {
  expect(textLength('中🙂a')).toBe(3)
  expect(textWithin('🙂'.repeat(120), 3, 120)).toBe(true)
  expect(textWithin('中'.repeat(121), 3, 120)).toBe(false)
  expect(textWithin('  ', 2, 1000)).toBe(false)
})
