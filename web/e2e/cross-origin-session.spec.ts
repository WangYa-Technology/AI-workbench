import { createServer } from 'node:http'
import { expect, test } from '@playwright/test'

test('same-site cross-origin pages cannot revoke or replace a signed-in session', async ({ page, request, baseURL }) => {
  // A second loopback port is same-site but cross-origin. SameSite=Lax cookies
  // can accompany its simple POST; lack of CORS read access is insufficient.
  const attacker = createServer((_request, response) => {
    response.writeHead(200, { 'Content-Type': 'text/html' })
    response.end('<!doctype html><title>Isolated cross-origin fixture</title>')
  })
  await new Promise<void>(resolve => attacker.listen(0, '127.0.0.1', resolve))
  try {
    const address = attacker.address()
    if (!address || typeof address === 'string') throw new Error('Missing fixture address')
    const handle = `csrf_${crypto.randomUUID().slice(0, 8)}`
    const registered = await page.request.post('/api/v1/auth/register', { data: {
      email: `${handle}@example.test`, password: `CSRF-${crypto.randomUUID()}`,
      handle, displayName: 'CSRF Test', locale: 'en-US', timezone: 'UTC',
    } })
    expect(registered.status()).toBe(201)
    const original = (await registered.json()).user.id
    const attackerCredentials = { email: `other_${handle}@example.test`, password: `CSRF-${crypto.randomUUID()}` }
    const other = await request.post('/api/v1/auth/register', { data: {
      ...attackerCredentials, handle: `other_${handle}`, displayName: 'Other CSRF Test', locale: 'en-US', timezone: 'UTC',
    } })
    expect(other.status()).toBe(201)
    await page.goto(`http://127.0.0.1:${address.port}`)
    for (const action of ['logout', 'login']) {
      const target = `${baseURL}/api/v1/auth/${action}`
      const received = page.waitForResponse(response => response.url() === target && response.request().method() === 'POST')
      await page.evaluate(async ({ url, credentials }) => {
        // text/plain avoids a CORS preflight while carrying JSON the API can
        // decode. Chromium may hide network header details on blocked replies;
        // assert the actual response and retained session instead.
        await fetch(url, { method: 'POST', mode: 'no-cors', credentials: 'include',
          headers: { 'Content-Type': 'text/plain' }, body: JSON.stringify(credentials) })
      }, { url: target, credentials: attackerCredentials })
      expect((await received).status()).toBe(403)
      const session = await page.request.get('/api/v1/auth/session')
      expect(session.status()).toBe(200)
      expect((await session.json()).user.id).toBe(original)
    }
    // The real site's own request must still work with browser metadata.
    await page.goto('/market')
    const status = await page.evaluate(async () => (await fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'include' })).status)
    expect(status).toBe(204)
    expect((await page.request.get('/api/v1/auth/session')).status()).toBe(401)
  } finally {
    await new Promise<void>((resolve, reject) => attacker.close(error => error ? reject(error) : resolve()))
  }
})
