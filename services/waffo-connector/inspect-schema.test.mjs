import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, rm, readdir, readFile, stat, symlink } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { captureSchema, captureSchemaFile, normalizeSchema, schemaQuery } from './inspect-schema.mjs'

const scalar = { kind: 'SCALAR', name: 'String' }
const field = { name: 'echo', args: [], type: scalar }
function fixture() {
  return { data: { __schema: { queryType: { name: 'Query' }, types: [
    { kind: 'OBJECT', name: 'Query', fields: [structuredClone(field)], inputFields: null, enumValues: null },
    { ...scalar, fields: null, inputFields: null, enumValues: null },
  ] } } }
}

test('capture performs only introspection and strips unrequested data', async () => {
  let calls = 0
  const response = fixture()
  response.data.customerEmail = 'private@example.test'
  response.data.__schema.types[0].description = 'private description'
  const client = { graphql: { query: async input => {
    calls++
    assert.deepEqual(input, { query: schemaQuery })
    assert.match(input.query, /^query HCAIReadOnlySchema/)
    assert.doesNotMatch(input.query, /\b(mutation|onetimeOrders|payments|refundTickets|buyerEmail|description|defaultValue)\b/)
    return response
  } } }
  const result = await captureSchema(client, 'test')
  assert.equal(calls, 1)
  assert.equal(result.schema.queryType, 'Query')
  assert.equal(result.environment, 'test')
  assert.equal(result.sdkVersion, '0.19.1')
  assert.doesNotMatch(JSON.stringify(result), /private/)
  await assert.rejects(captureSchema(client, ''), /environment_required/)
  assert.equal(calls, 1)
})

test('partial, malformed, duplicated and unresolved schema responses fail closed', () => {
  for (const mutate of [
    response => { response.errors = [{ message: 'partial failure' }] },
    response => { response.data = null },
    response => { response.data.__schema.types = null },
    response => { response.data.__schema.queryType.name = 'Absent' },
    response => { response.data.__schema.types.pop() },
    response => { response.data.__schema.types.push(response.data.__schema.types[0]) },
    response => { response.data.__schema.types[0].fields.push(structuredClone(field)) },
    response => { response.data.__schema.types[0].fields[0].name = 'bad\nname' },
    response => { response.data.__schema.types[0].fields[0].args = null },
    response => { response.data.__schema.types[0].fields[0].type = { kind: 'NON_NULL', name: null, ofType: null } },
    response => { response.data.__schema.types[0].fields[0].type = { kind: 'OBJECT', name: 'String' } },
  ]) {
    const response = fixture()
    mutate(response)
    assert.throws(() => normalizeSchema(response), /schema_response_invalid/)
  }
})

test('missing merchant configuration creates no artifact and does not print environment secrets', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'hcai-schema-'))
  try {
    const result = spawnSync(process.execPath, ['inspect-schema.mjs', '--output', join(directory, 'schema.json')], {
      cwd: fileURLToPath(new URL('.', import.meta.url)),
      env: { PATH: process.env.PATH, WAFFO_ENVIRONMENT: 'test', WAFFO_CONNECTOR_TOKEN: 'fixture-private-secret' }, encoding: 'utf8',
    })
    assert.equal(result.status, 1)
    assert.match(result.stderr, /Schema capture failed/)
    assert.doesNotMatch(result.stdout + result.stderr, /fixture-private-secret/)
    assert.deepEqual(await readdir(directory), [])
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
})

test('validated capture creates a private file and cannot overwrite files or symlinks', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'hcai-schema-file-'))
  const client = { graphql: { query: async () => fixture() } }
  try {
    const output = join(directory, 'schema.json')
    await captureSchemaFile(client, 'test', output)
    const original = await readFile(output, 'utf8')
    assert.equal(JSON.parse(original).schema.queryType, 'Query')
    assert.equal((await stat(output)).mode & 0o777, 0o600)
    await assert.rejects(captureSchemaFile(client, 'prod', output), { code: 'EEXIST' })
    const alias = join(directory, 'alias.json')
    await symlink(output, alias)
    await assert.rejects(captureSchemaFile(client, 'prod', alias), { code: 'EEXIST' })
    assert.equal(await readFile(output, 'utf8'), original)
    await assert.rejects(captureSchemaFile({ graphql: { query: async () => ({ errors: [{}] }) } }, 'test', join(directory, 'failed.json')), /schema_response_invalid/)
    assert.deepEqual((await readdir(directory)).sort(), ['alias.json', 'schema.json'])
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
})
