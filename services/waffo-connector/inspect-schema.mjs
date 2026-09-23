import { writeFile } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
import { WaffoPancake } from '@waffo/pancake-ts'
import { waffoFetch } from './transport.mjs'

// Introspection only: never fetch orders, customers, payment data or mutations.
const typeRef = depth => `kind name${depth ? ` ofType { ${typeRef(depth - 1)} }` : ''}`
const fields = `name type { ${typeRef(7)} }`
export const schemaQuery = `query HCAIReadOnlySchema {
  __schema {
    queryType { name }
    types {
      kind name
      fields(includeDeprecated: true) { ${fields} args { ${fields} } }
      inputFields { ${fields} }
      enumValues(includeDeprecated: true) { name }
    }
  }
}`

const identifier = /^[_A-Za-z][_0-9A-Za-z]*$/
const namedKinds = new Set(['SCALAR', 'OBJECT', 'INTERFACE', 'UNION', 'ENUM', 'INPUT_OBJECT'])
const invalid = () => { throw new Error('schema_response_invalid') }
function name(value) {
  if (typeof value !== 'string' || value.length > 200 || !identifier.test(value)) invalid()
  return value
}
function reference(value, depth = 0) {
  if (!value || depth > 7) invalid()
  if (['LIST', 'NON_NULL'].includes(value.kind)) {
    if (value.name != null || !value.ofType || value.kind === 'NON_NULL' && value.ofType.kind === 'NON_NULL') invalid()
    return { kind: value.kind, ofType: reference(value.ofType, depth + 1) }
  }
  if (!namedKinds.has(value.kind) || value.ofType != null) invalid()
  return { kind: value.kind, name: name(value.name) }
}
function entries(value, convert) {
  if (value === null) return null
  if (!Array.isArray(value) || value.length > 10000) invalid()
  const seen = new Set()
  const result = value.map(item => {
    const normalized = convert(item)
    if (seen.has(normalized.name)) invalid()
    seen.add(normalized.name)
    return normalized
  })
  return result.sort((a, b) => a.name.localeCompare(b.name, 'en'))
}
const field = value => ({ name: name(value?.name), type: reference(value?.type) })

export function normalizeSchema(response) {
  if (response?.errors != null && (!Array.isArray(response.errors) || response.errors.length)) invalid()
  const schema = response?.data?.__schema
  const queryType = name(schema?.queryType?.name)
  const types = entries(schema?.types, value => {
    if (!namedKinds.has(value?.kind)) invalid()
    return {
      kind: value.kind, name: name(value.name),
      fields: entries(value.fields, value => ({ ...field(value), args: entries(value.args, field) })),
      inputFields: entries(value.inputFields, field),
      enumValues: entries(value.enumValues, value => ({ name: name(value?.name) })),
    }
  })
  if (!types?.some(type => type.name === queryType && type.kind === 'OBJECT' && type.fields?.length)) invalid()
  const kinds = new Map(types.map(type => [type.name, type.kind]))
  const checkRef = type => {
    if (type.ofType) return checkRef(type.ofType)
    if (kinds.get(type.name) !== type.kind) invalid()
  }
  for (const type of types) {
    for (const item of [...(type.fields ?? []), ...(type.inputFields ?? [])]) {
      checkRef(item.type)
      if ('args' in item) {
        if (item.args === null) invalid()
        for (const arg of item.args) checkRef(arg.type)
      }
    }
  }
  return { queryType, types }
}

export async function captureSchema(client, environment) {
  if (!['test', 'prod'].includes(environment)) throw new Error('schema_environment_required')
  const schema = normalizeSchema(await client.graphql.query({ query: schemaQuery }))
  return { version: 1, environment, sdkVersion: '0.19.1', observedAt: new Date().toISOString(), schema }
}

export async function captureSchemaFile(client, environment, output) {
  const report = await captureSchema(client, environment)
  await writeFile(output, JSON.stringify(report, null, 2) + '\n', { flag: 'wx', mode: 0o600 })
}

async function main() {
  if (process.argv.length !== 4 || process.argv[2] !== '--output') throw new Error('usage: npm run schema:inspect -- --output PATH')
  const environment = process.env.WAFFO_ENVIRONMENT?.trim()
  const merchantId = process.env.WAFFO_MERCHANT_ID?.trim()
  const privateKey = process.env.WAFFO_PRIVATE_KEY || (process.env.WAFFO_PRIVATE_KEY_BASE64
    ? Buffer.from(process.env.WAFFO_PRIVATE_KEY_BASE64, 'base64').toString('utf8') : '')
  if (!['test', 'prod'].includes(environment) || !merchantId || !privateKey) throw new Error('schema_credentials_and_environment_required')
  const client = new WaffoPancake({ merchantId, privateKey, environment, fetch: waffoFetch })
  await captureSchemaFile(client, environment, process.argv[3])
  process.stdout.write('Schema captured; no transaction queries or financial actions executed.\n')
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(() => {
    // Do not print SDK/server errors, credentials, signatures or arbitrary data.
    process.stderr.write('Schema capture failed. Check arguments, credentials, introspection permission and an unused output path.\n')
    process.exitCode = 1
  })
}
