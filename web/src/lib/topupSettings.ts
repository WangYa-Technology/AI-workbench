// Parse decimal money without silently rounding extra precision or accepting
// exponent notation. API amounts and comparisons always use integer cents.
export function usdAmountCents(value: string | number): number | null {
  const match = /^(\d{1,6})(?:\.(\d{1,2}))?$/.exec(String(value).trim())
  if (!match) return null
  const cents = Number(match[1]) * 100 + Number((match[2] ?? '').padEnd(2, '0'))
  return cents <= 99999999 ? cents : null
}

export function topupPresetCents(value: string, minimum: number): number[] | null {
  if (!value.trim()) return []
  const parts = value.split(/[,，]/).map(usdAmountCents)
  if (parts.length > 12 || parts.some(amount => amount === null || amount < minimum) || new Set(parts).size !== parts.length) return null
  return (parts as number[]).sort((a, b) => a - b)
}
