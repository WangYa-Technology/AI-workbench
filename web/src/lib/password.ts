/** New passwords use Unicode character counts and bcrypt's UTF-8 byte limit. */
export function validNewPassword(value: string): boolean {
  return Array.from(value).length >= 10 && new TextEncoder().encode(value).length <= 72
}
