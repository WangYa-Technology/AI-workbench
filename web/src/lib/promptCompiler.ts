export const MAX_COMPILED_PROMPT_LENGTH = 2000

export function compilePrompt(
  primaryPrompt: string,
  optionalSections: string[],
  maxLength = MAX_COMPILED_PROMPT_LENGTH,
) {
  const primary = primaryPrompt.trim().slice(0, maxLength)
  const sections = primary ? [primary] : []
  let compiledLength = primary.length

  for (const rawSection of optionalSections) {
    const section = rawSection.trim()
    if (!section) continue

    const separatorLength = sections.length ? 2 : 0
    if (compiledLength + separatorLength + section.length > maxLength) continue

    sections.push(section)
    compiledLength += separatorLength + section.length
  }

  return sections.join('\n\n')
}
