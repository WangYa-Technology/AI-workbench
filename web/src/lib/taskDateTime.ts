/** Format a wall-clock minute in the selected IANA timezone. */
export function taskLocalMinute(instant: Date, timeZone: string): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).formatToParts(instant)
  const value = (type: string) => parts.find(part => part.type === type)?.value
  return `${value('year')}-${value('month')}-${value('day')}T${value('hour')}:${value('minute')}`
}

/** Reject nonexistent and ambiguous DST minutes rather than silently moving a deadline. */
export function taskDeadlineISO(wallClock: string, timeZone: string): string {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(wallClock)) throw new RangeError('Invalid deadline')
  const naive = Date.parse(`${wallClock}:00Z`)
  if (!Number.isFinite(naive) || new Date(naive).toISOString().slice(0, 16) !== wallClock) throw new RangeError('Invalid deadline')
  const candidates = new Set<number>()
  for (let hour = -48; hour <= 48; hour += 6) {
    const probe = naive + hour * 3_600_000
    const local = Date.parse(`${taskLocalMinute(new Date(probe), timeZone)}:00Z`)
    const candidate = naive - (local - probe)
    if (taskLocalMinute(new Date(candidate), timeZone) === wallClock) candidates.add(candidate)
  }
  if (candidates.size !== 1) throw new RangeError('Ambiguous or nonexistent deadline')
  return new Date([...candidates][0]!).toISOString()
}
