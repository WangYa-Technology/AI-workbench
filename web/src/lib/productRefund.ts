import { textWithin } from './unicodeText'

export function validRefundReason(value: string) {
  return textWithin(value, 10, 500) && !value.includes('\0') && !/[\uD800-\uDFFF]/u.test(value)
}
