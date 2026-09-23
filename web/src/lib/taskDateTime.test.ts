import { describe, expect, it } from 'vitest'
import { taskDeadlineISO, taskLocalMinute } from './taskDateTime'

describe('task deadlines', () => {
  it('uses the selected timezone including fractional offsets', () => {
    expect(taskDeadlineISO('2026-09-20T18:30', 'Asia/Shanghai')).toBe('2026-09-20T10:30:00.000Z')
    expect(taskDeadlineISO('2026-09-20T18:30', 'Asia/Kathmandu')).toBe('2026-09-20T12:45:00.000Z')
    expect(taskLocalMinute(new Date('2026-09-20T10:30:00Z'), 'Asia/Shanghai')).toBe('2026-09-20T18:30')
  })
  it('rejects invalid dates, DST gaps and ambiguous fall-back times', () => {
    for (const time of ['2026-02-30T12:00', '2026-03-08T02:30', '2026-11-01T01:30']) {
      expect(() => taskDeadlineISO(time, 'America/New_York')).toThrow(RangeError)
    }
    expect(taskDeadlineISO('2026-11-01T03:00', 'America/New_York')).toBe('2026-11-01T08:00:00.000Z')
  })
})
