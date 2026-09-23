import { describe, expect, it } from 'vitest'
import { topupPresetCents, usdAmountCents } from './topupSettings'

describe('USD top-up amounts', () => {
  it('preserves exact cents and the supported upper bound', () => {
    expect(usdAmountCents('0.29')).toBe(29)
    expect(usdAmountCents(' 10.1 ')).toBe(1010)
    expect(usdAmountCents(20)).toBe(2000)
    expect(usdAmountCents('999999.99')).toBe(99999999)
  })
  it('rejects rounding, exponent notation and invalid or excessive amounts', () => {
    for (const value of ['', '1.001', '1e2', '-1', 'NaN', 'Infinity', '1,000', '1000000', '.5']) {
      expect(usdAmountCents(value), value).toBeNull()
    }
  })
  it('allows no suggestions and parses distinct suggestions in cents', () => {
    expect(topupPresetCents(' ', 50)).toEqual([])
    expect(topupPresetCents('20, 10.29，10', 1000)).toEqual([1000, 1029, 2000])
  })
  it('rejects duplicate, malformed, below-minimum and too many suggestions', () => {
    for (const value of ['10,10.00', '10,', '10,,20', '5,10', '10.001', '1000000', Array.from({ length: 13 }, (_, i) => String(i + 10)).join(',')]) {
      expect(topupPresetCents(value, 1000), value).toBeNull()
    }
  })
})
