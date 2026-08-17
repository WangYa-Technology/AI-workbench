import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { baseParse, NodeTypes, type TemplateChildNode } from '@vue/compiler-dom'
import { parse as parseSFC } from '@vue/compiler-sfc'
import { describe, expect, it } from 'vitest'
import enUS from './messages/en-US'
import zhCN from './messages/zh-CN'

type MessageTree = Record<string, unknown>

const sourceRoot = dirname(fileURLToPath(import.meta.url))
const appRoot = dirname(sourceRoot)

function flatten(value: MessageTree, prefix = '', output: Record<string, string> = {}) {
  for (const [key, child] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (child && typeof child === 'object') flatten(child as MessageTree, path, output)
    else output[path] = String(child)
  }
  return output
}

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    return entry.isDirectory() ? sourceFiles(path) : [path]
  })
}

const placeholders = (value: string) => [...value.matchAll(/\{([\w.-]+)\}/g)].map((match) => match[1]).sort()

describe('internationalization contract', () => {
  const en = flatten(enUS)
  const zh = flatten(zhCN)

  it('keeps locale keys, values, and interpolation parameters aligned', () => {
    expect(Object.keys(zh).sort()).toEqual(Object.keys(en).sort())
    for (const key of Object.keys(en)) {
      expect(en[key].trim(), `${key} must have an English value`).not.toBe('')
      expect(zh[key].trim(), `${key} must have a Chinese value`).not.toBe('')
      expect(placeholders(zh[key]), `${key} must use the same interpolation parameters`).toEqual(placeholders(en[key]))
    }
  })

  it('resolves every literal translation key used by the application', () => {
    const missing = new Set<string>()
    for (const file of sourceFiles(appRoot).filter((path) => /\.(ts|vue)$/.test(path))) {
      const source = readFileSync(file, 'utf8')
      for (const match of source.matchAll(/\bt\(\s*['"]([^'"]+)['"]/g)) {
        if (!(match[1] in en)) missing.add(match[1])
      }
    }
    expect([...missing].sort()).toEqual([])
  })

  it('keeps static user-facing template copy inside the locale catalog', () => {
    const violations: string[] = []
    for (const file of sourceFiles(appRoot).filter((path) => path.endsWith('.vue'))) {
      const source = readFileSync(file, 'utf8')
      const template = parseSFC(source, { filename: file }).descriptor.template?.content || ''
      const inspect = (node: TemplateChildNode) => {
        if (node.type === NodeTypes.TEXT) {
          const copy = node.content.trim()
          const technicalCopy = copy.replace(/[·/\s]/g, '')
          if (copy && !['English (US)', '简体中文'].includes(copy) && !['v', 'SHA-256'].includes(technicalCopy) && /[A-Za-z\u3400-\u9fff]/.test(copy)) violations.push(`${file}: ${copy}`)
        }
        if (node.type === NodeTypes.ELEMENT) {
          for (const property of node.props) {
            if (property.type === NodeTypes.ATTRIBUTE && ['alt', 'aria-label', 'placeholder', 'title'].includes(property.name) && property.value?.content) {
              violations.push(`${file}: ${property.value.content}`)
            }
          }
          node.children.forEach(inspect)
        } else if ('children' in node && Array.isArray(node.children)) {
          node.children.forEach((child) => inspect(child as TemplateChildNode))
        }
      }
      baseParse(template).children.forEach(inspect)
    }
    expect(violations).toEqual([])
  })

  it('localizes every stable error code emitted by the Go API', () => {
    const transportRoot = join(appRoot, '../../internal/transport/httpapi')
    const codes = new Set<string>()
    for (const file of sourceFiles(transportRoot).filter((path) => path.endsWith('.go'))) {
      const source = readFileSync(file, 'utf8')
      for (const match of source.matchAll(/WriteError\([^\n]*?"([a-z][a-z0-9_]+)",/g)) codes.add(match[1])
    }
    codes.add('unexpected_response')
    expect([...codes].filter((code) => !(`errors.codes.${code}` in en)).sort()).toEqual([])
  })
})
