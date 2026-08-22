import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { baseParse, NodeTypes, type TemplateChildNode } from '@vue/compiler-dom'
import { parse as parseSFC } from '@vue/compiler-sfc'
import { describe, expect, it } from 'vitest'

const sourceRoot = dirname(dirname(dirname(fileURLToPath(import.meta.url))))
const scanRoots = [join(sourceRoot, 'pages'), join(sourceRoot, 'components')]
const nativeControlTags = new Set(['button', 'input', 'select', 'textarea', 'table', 'progress', 'dialog', 'details', 'meter'])

function vueFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return entry.name === 'ui' ? [] : vueFiles(path)
    return entry.name.endsWith('.vue') ? [path] : []
  })
}

function walk(nodes: TemplateChildNode[], visit: (node: TemplateChildNode) => void) {
  for (const node of nodes) {
    visit(node)
    if (node.type === NodeTypes.ELEMENT) walk(node.children, visit)
    if (node.type === NodeTypes.IF) for (const branch of node.branches) walk(branch.children, visit)
    if (node.type === NodeTypes.FOR) walk(node.children, visit)
  }
}

describe('Appica compatibility migration', () => {
  it('routes shared native controls through the Vue compatibility layer', () => {
    const violations: string[] = []
    for (const file of scanRoots.flatMap(vueFiles)) {
      const source = readFileSync(file, 'utf8')
      const template = parseSFC(source, { filename: file }).descriptor.template?.content
      if (!template) continue
      walk(baseParse(template).children, (node) => {
        if (node.type === NodeTypes.ELEMENT && nativeControlTags.has(node.tag)) {
          violations.push(`${relative(sourceRoot, file)}:${node.loc.start.line} <${node.tag}>`)
        }
      })
    }
    expect(violations).toEqual([])
  })

  it('uses component-backed buttons for action-styled links', () => {
    const violations = scanRoots.flatMap(vueFiles).flatMap((file) => {
      const source = readFileSync(file, 'utf8')
      return [...source.matchAll(/<RouterLink\b[^>]*class="[^"]*(?:command-button|primary-action|secondary-action|closing-primary|home-register|icon-button)[^"]*"/g)]
        .map((match) => `${relative(sourceRoot, file)}:${source.slice(0, match.index).split('\n').length}`)
    })
    expect(violations).toEqual([])
  })
})
