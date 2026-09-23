import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { generateKeyPairSync } from 'node:crypto'
import { gzipSync } from 'node:zlib'
import { WaffoPancake } from '@waffo/pancake-ts'
import { waffoFetch, maxWaffoResponseBytes, withWaffoRequestCancellation, waffoErrorStatus } from './transport.mjs'

test('financial POSTs never follow redirects, including same-origin 307 and 308', async () => {
  let first = 0, redirected = 0
  const server = http.createServer((req, res) => {
    if (req.url === '/other') { redirected++; res.end('{}'); return }
    first++
    res.writeHead(Number(req.url.slice(1)), { location: '/other' }); res.end()
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  try {
    for (const code of [301, 302, 303, 307, 308]) {
      await assert.rejects(waffoFetch(`http://127.0.0.1:${server.address().port}/${code}`, {
        method: 'POST', body: JSON.stringify({ payment: 'test' }), headers: { authorization: 'Bearer fixture' },
      }))
    }
    assert.equal(first, 5)
    assert.equal(redirected, 0)
  } finally {
    server.closeAllConnections()
    await new Promise(resolve => server.close(resolve))
  }
})

async function withServer(handler, run) {
  const server = http.createServer(handler)
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  try {
    await run(`http://127.0.0.1:${server.address().port}`)
  } finally {
    server.closeAllConnections()
    await new Promise(resolve => server.close(resolve))
  }
}

test('caller cancellation prevents a financial POST before dispatch', async () => {
  let calls = 0
  await withServer((_req, res) => { calls++; res.end('{}') }, async origin => {
    for (const requestObject of [false, true]) {
      const controller = new AbortController()
      controller.abort()
      const options = { method: 'POST', body: '{}', signal: controller.signal }
      const request = requestObject ? new Request(origin, options) : origin
      await assert.rejects(waffoFetch(request, requestObject ? undefined : options), { name: 'AbortError' })
    }
    assert.equal(calls, 0)
  })
})

test('caller cancellation stops reading after headers, without retrying the request', { timeout: 3000 }, async () => {
  let calls = 0
  await withServer((_req, res) => {
    calls++
    res.writeHead(200, { 'content-type': 'application/json' })
    res.write('{"data":')
  }, async origin => {
    const controller = new AbortController()
    const response = await waffoFetch(origin, { method: 'POST', body: '{}', signal: controller.signal })
    const reading = response.json()
    controller.abort()
    await assert.rejects(reading, { name: 'AbortError' })
    assert.equal(calls, 1)
  })
})

test('remote success preserves headers and HTTP failures cannot reach the SDK parser', async () => {
  await withServer((req, res) => {
    res.writeHead(Number(req.url.slice(1)), { 'content-type': 'application/json', 'x-request-id': 'fixture' })
    res.end(JSON.stringify({ data: { id: 'fixture' } }))
  }, async origin => {
    const response = await waffoFetch(`${origin}/200`)
    assert.equal(response.status, 200)
    assert.equal(response.headers.get('x-request-id'), 'fixture')
    assert.deepEqual(await response.json(), { data: { id: 'fixture' } })
    for (const [status, outward] of [[400, 422], [401, 401], [403, 403], [409, 422], [422, 422], [429, 429], [500, 502], [503, 502]]) {
      await assert.rejects(waffoFetch(`${origin}/${status}`), error => {
        assert.equal(error.message, 'waffo_http_error')
        assert.equal(error.status, status)
        assert.equal(waffoErrorStatus(error), outward)
        return true
      })
    }
  })
})

test('declared, chunked and compressed oversized bodies cannot reach the SDK parser', async () => {
  const oversized = JSON.stringify({ data: 'x'.repeat(maxWaffoResponseBytes) })
  await withServer((req, res) => {
    if (req.url === '/declared') {
      res.writeHead(200, { 'content-length': Buffer.byteLength(oversized) })
      res.end(oversized)
    } else if (req.url === '/gzip') {
      const compressed = gzipSync(oversized)
      res.writeHead(200, { 'content-encoding': 'gzip', 'content-length': compressed.length })
      res.end(compressed)
    } else {
      res.writeHead(200, { 'transfer-encoding': 'chunked' })
      res.write(oversized.slice(0, 100))
      res.end(oversized.slice(100))
    }
  }, async origin => {
    for (const path of ['/declared', '/chunked', '/gzip']) {
      await assert.rejects(async () => {
        const response = await waffoFetch(origin + path)
        await response.json()
      }, /waffo_response_too_large/)
    }
  })
})

test('exactly bounded decoded JSON remains readable', async () => {
  const payload = JSON.stringify({ data: 'x'.repeat(maxWaffoResponseBytes - 11) })
  assert.equal(Buffer.byteLength(payload), maxWaffoResponseBytes)
  await withServer((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/json', 'content-length': Buffer.byteLength(payload) })
    res.end(payload)
  }, async origin => {
    const response = await waffoFetch(origin)
    assert.equal((await response.json()).data.length, maxWaffoResponseBytes - 11)
  })
})

test('disconnect between SDK steps prevents the financial step and preserves another request', { timeout: 3000 }, async () => {
  const remoteCalls = []
  let readyCount = 0, finishedCount = 0
  let ready, release, disconnected, finished
  const bothReady = new Promise(resolve => { ready = resolve })
  const gate = new Promise(resolve => { release = resolve })
  const closed = new Promise(resolve => { disconnected = resolve })
  const bothFinished = new Promise(resolve => { finished = resolve })
  let cancelledError
  await withServer((req, res) => {
    remoteCalls.push(req.url)
    res.end('{"data":{}}')
  }, async remote => {
    await withServer(withWaffoRequestCancellation(async (req, res) => {
      if (req.url === '/cancel') res.once('close', disconnected)
      try {
        const response = await waffoFetch(`${remote}/session${req.url}`, { method: 'POST', body: '{}' })
        await response.json()
        if (++readyCount === 2) ready()
        await gate
        const result = await waffoFetch(`${remote}/refund${req.url}`, { method: 'POST', body: '{}' })
        res.end(await result.text())
      } catch (error) {
        if (req.url === '/cancel') cancelledError = error
        res.destroy()
      } finally {
        if (++finishedCount === 2) finished()
      }
    }), async connector => {
      const controller = new AbortController()
      const cancelled = fetch(`${connector}/cancel`, { signal: controller.signal }).catch(error => error)
      const healthy = fetch(`${connector}/healthy`)
      await bothReady
      controller.abort()
      await closed
      release()
      assert.equal((await cancelled).name, 'AbortError')
      assert.deepEqual(await (await healthy).json(), { data: {} })
      await bothFinished
      assert.equal(cancelledError?.name, 'AbortError')
      assert.deepEqual(remoteCalls.sort(), ['/refund/healthy', '/session/cancel', '/session/healthy'])
    })
  })
})

test('request deadline includes an unfinished response body', { timeout: 23000 }, async () => {
  await withServer((_req, res) => {
    res.writeHead(200, { 'content-type': 'application/json' })
    res.write('{"data":')
  }, async origin => {
    const response = await waffoFetch(origin)
    await assert.rejects(response.json(), error => ['TimeoutError', 'AbortError'].includes(error.name))
  })
})

test('checkout failure cancels the unfinished parallel SDK branch', { timeout: 5000 }, async t => {
  const { privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
  for (const [failedPath, status] of [['/issue-session-token', 200], ['/create-session', 200], ['/issue-session-token', 502], ['/create-session', 502]]) {
    await t.test(`${failedPath} HTTP ${status}`, async () => {
      const requests = new Map()
      let cancelled
      const branchClosed = new Promise(resolve => { cancelled = resolve })
      await withServer((req, res) => {
        const path = req.url.slice(req.url.lastIndexOf('/'))
        assert.ok(['/issue-session-token', '/create-session'].includes(path))
        assert.ok(req.headers['x-signature'])
        assert.ok(!requests.has(path), 'parallel branch was retried')
        requests.set(path, res)
        if (path !== failedPath) res.once('close', cancelled)
        if (requests.size === 2) {
          const failed = requests.get(failedPath)
          const pending = [...requests].find(([key]) => key !== failedPath)[1]
          pending.writeHead(200, { 'content-type': 'application/json' })
          pending.write('{"data":')
          failed.writeHead(status, { 'content-type': 'application/json' })
          // The pinned SDK only rejects HTTP.post immediately on a transport
          // or JSON failure. A valid error envelope waits for Promise.all.
          failed.end('interrupted provider JSON')
        }
      }, async remote => {
        const client = new WaffoPancake({
          merchantId: 'MER_' + 'a'.repeat(22), privateKey: privateKey.export({ type: 'pkcs8', format: 'pem' }),
          baseUrl: remote, environment: 'test', fetch: waffoFetch,
        })
        await withServer(withWaffoRequestCancellation(async (_req, res) => {
          try {
            await client.checkout.authenticated.create({
              productId: 'PROD_' + 'b'.repeat(22), buyerIdentity: 'isolated-buyer', currency: 'USD',
              orderMerchantExternalId: 'isolated-order', priceSnapshot: { amount: '19.00', taxCategory: 'digital_goods' },
            })
            res.end('unexpected success')
          } catch {
            res.writeHead(502)
            res.end('checkout outcome unknown')
          }
        }), async connector => {
          const response = await fetch(connector)
          assert.equal(response.status, 502)
          assert.equal(await response.text(), 'checkout outcome unknown')
          let timer
          try {
            const closed = await Promise.race([
              branchClosed.then(() => true),
              new Promise(resolve => { timer = setTimeout(() => resolve(false), 500) }),
            ])
            assert.equal(closed, true, 'unfinished SDK request survived the connector response')
            assert.equal(requests.size, 2, 'cancellation must not retry checkout')
          } finally { clearTimeout(timer) }
        })
      })
    })
  }
})

test('remote HTTP failures close unfinished bodies without parsing them', { timeout: 3000 }, async () => {
  let closed
  const bodyClosed = new Promise(resolve => { closed = resolve })
  await withServer((_req, res) => {
    res.once('close', closed)
    res.writeHead(429, { 'content-type': 'application/json' })
    res.flushHeaders()
  }, async origin => {
    await assert.rejects(waffoFetch(origin), { message: 'waffo_http_error', status: 429 })
    await bodyClosed
  })
})

test('pinned merchant and customer SDK clients both use the bounded transport', async () => {
  const { privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
  let oversized = false
  const calls = []
  await withServer((req, res) => {
    calls.push({ path: req.url, merchant: Boolean(req.headers['x-signature']), customer: req.headers.authorization === 'Bearer fixture' })
    res.writeHead(200, { 'content-type': 'application/json', 'transfer-encoding': 'chunked' })
    res.end(JSON.stringify({ data: { marker: oversized ? 'x'.repeat(maxWaffoResponseBytes) : 'verified' } }))
  }, async origin => {
    const client = new WaffoPancake({
      merchantId: 'MER_' + 'a'.repeat(22), privateKey: privateKey.export({ type: 'pkcs8', format: 'pem' }),
      baseUrl: origin, environment: 'test', fetch: waffoFetch,
    })
    for (const graphql of [client.graphql, client.customer('fixture').graphql]) {
      oversized = false
      assert.equal((await graphql.query({ query: '{ __typename }' })).data.marker, 'verified')
      oversized = true
      await assert.rejects(graphql.query({ query: '{ __typename }' }), /Non-JSON response/)
    }
    assert.equal(calls.length, 4)
    assert.deepEqual(calls.map(call => [call.merchant, call.customer]), [[true, false], [true, false], [false, true], [false, true]])
  })
})

test('pinned checkout SDK cannot treat HTTP failure data as a successful session', async () => {
  const { privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
  await withServer((req, res) => {
    res.writeHead(503, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ data: req.url.endsWith('/create-session')
      ? { sessionId: 'CHK_false', checkoutUrl: 'https://checkout.example.test/false', expiresAt: '2030-01-01T00:00:00Z' }
      : { token: 'false-token', expiresAt: '2030-01-01T00:00:00Z' } }))
  }, async origin => {
    const client = new WaffoPancake({
      merchantId: 'MER_' + 'a'.repeat(22), privateKey: privateKey.export({ type: 'pkcs8', format: 'pem' }),
      baseUrl: origin, environment: 'test', fetch: waffoFetch,
    })
    await assert.rejects(client.checkout.authenticated.create({
      productId: 'PROD_' + 'b'.repeat(22), buyerIdentity: 'isolated-buyer', currency: 'USD',
      orderMerchantExternalId: 'isolated-order', priceSnapshot: { amount: '19.00', taxCategory: 'digital_goods' },
    }), { message: 'waffo_http_error', status: 503 })
    await assert.rejects(client.customer('isolated-token').graphql.query({ query: '{ __typename }' }),
      { message: 'waffo_http_error', status: 503 })
  })
})
