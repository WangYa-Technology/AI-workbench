export type HslColor = { h: number; s: number; l: number }

export function normalizeHex(value: string) {
  const match = value.trim().match(/^#?([\da-f]{3}|[\da-f]{6})$/i)
  if (!match) return undefined
  const hex = match[1].length === 3 ? match[1].split('').map(char => char + char).join('') : match[1]
  return `#${hex.toUpperCase()}`
}

export function hexToHsl(value: string): HslColor {
  const hex = normalizeHex(value) ?? '#000000'
  const r = parseInt(hex.slice(1, 3), 16) / 255
  const g = parseInt(hex.slice(3, 5), 16) / 255
  const b = parseInt(hex.slice(5, 7), 16) / 255
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const delta = max - min
  let h = 0
  if (delta) h = max === r ? ((g - b) / delta) % 6 : max === g ? (b - r) / delta + 2 : (r - g) / delta + 4
  h = Math.round(h * 60)
  if (h < 0) h += 360
  const l = (max + min) / 2
  const s = delta ? delta / (1 - Math.abs(2 * l - 1)) : 0
  return { h, s: Math.round(s * 100), l: Math.round(l * 100) }
}

export function hslToHex({ h, s, l }: HslColor) {
  const saturation = Math.max(0, Math.min(100, s)) / 100
  const lightness = Math.max(0, Math.min(100, l)) / 100
  const chroma = (1 - Math.abs(2 * lightness - 1)) * saturation
  const x = chroma * (1 - Math.abs((h / 60) % 2 - 1))
  const m = lightness - chroma / 2
  const [r, g, b] = h < 60 ? [chroma, x, 0] : h < 120 ? [x, chroma, 0] : h < 180 ? [0, chroma, x] : h < 240 ? [0, x, chroma] : h < 300 ? [x, 0, chroma] : [chroma, 0, x]
  return `#${[r, g, b].map(channel => Math.round((channel + m) * 255).toString(16).padStart(2, '0')).join('').toUpperCase()}`
}
