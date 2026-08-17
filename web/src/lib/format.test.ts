import { describe, expect, it } from 'vitest'
import { formatCurrency, formatDateTime } from './format'

describe('international formatting', () => {
  it('formats USD from integer cents', () => {
    expect(formatCurrency(1299, 'USD', 'en-US')).toBe('$12.99')
  })

  it('formats the same instant in the requested time zone', () => {
    const value = '2026-08-10T12:00:00.000Z'
    expect(formatDateTime(value, 'en-US', 'UTC')).toContain('12:00 PM')
    expect(formatDateTime(value, 'en-US', 'America/Los_Angeles')).toContain('5:00 AM')
  })
})
