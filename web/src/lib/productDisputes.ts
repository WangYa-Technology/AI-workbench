// Receipts are references, never navigable URLs. Do not expose URL credentials,
// paths, queries or fragments, including legacy records accepted before this UI.
export function maskDisputeReference(value?: string): string {
  if (!value) return ''
  if (/^[a-z][a-z\d+.-]*:/i.test(value) || /[?@#]/.test(value)) return '••••'
  return value.length > 4 ? `••••${value.slice(-4)}` : '••••'
}
