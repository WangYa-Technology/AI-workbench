/** Match Go's RuneCountInString and PostgreSQL char_length. */
export const textLength = (value: string) => Array.from(value).length
export const textWithin = (value: string, min: number, max: number) => {
  const length = textLength(value.trim())
  return length >= min && length <= max
}
