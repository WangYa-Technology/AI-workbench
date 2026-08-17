import { createHmac } from 'node:crypto'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, test } from '@playwright/test'

type CapturedRequest = {
  body: Buffer
  eventId: string
  timestamp: string
  signature: string
}

test('creates, signs, rotates, dead-letters, and replays a local Webhook', async ({ page }) => {
  test.setTimeout(60_000)
  const captured: CapturedRequest[] = []
  let receiverStatus = 204
  const receiver = createServer((request, response) => {
    const chunks: Buffer[] = []
    request.on('data', chunk => chunks.push(Buffer.from(chunk)))
    request.on('end', () => {
      captured.push({
        body: Buffer.concat(chunks),
        eventId: String(request.headers['hcai-webhook-id'] || ''),
        timestamp: String(request.headers['hcai-webhook-timestamp'] || ''),
        signature: String(request.headers['hcai-webhook-signature'] || ''),
      })
      response.statusCode = receiverStatus
      response.end('bounded receiver evidence')
    })
  })
  await new Promise<void>((resolve, reject) => {
    receiver.once('error', reject)
    receiver.listen(0, '127.0.0.1', resolve)
  })

  const address = receiver.address() as AddressInfo
  const suffix = Date.now().toString(36)
  const endpointName = `Browser receiver ${suffix}`
  const endpointURL = `http://127.0.0.1:${address.port}/hcai-events`
  const email = `webhook_${suffix}@example.test`
  const handle = `wh_${suffix}`
  const password = 'webhook-contract-2026'
  let initialControl: { enabled: boolean, maxServiceAccounts: number, maxActiveKeys: number, defaultTtlDays: number, version: number } | undefined

  const verifySignature = (request: CapturedRequest, secret: string) => {
    expect(request.eventId).toMatch(/^[0-9a-f-]{36}$/)
    expect(request.timestamp).toMatch(/^\d+$/)
    const digest = createHmac('sha256', secret).update(`${request.timestamp}.`).update(request.body).digest('hex')
    expect(request.signature).toBe(`v1=${digest}`)
  }

  try {
    await page.setViewportSize({ width: 390, height: 844 })
    expect((await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })).ok()).toBeTruthy()
    const initialResponse = await page.request.get('/api/v1/admin/developer/access')
    expect(initialResponse.ok()).toBeTruthy()
    initialControl = (await initialResponse.json()).control
    const enabled = await page.request.put('/api/v1/admin/developer/control', { data: {
      enabled: true,
      maxServiceAccounts: initialControl!.maxServiceAccounts,
      maxActiveKeys: initialControl!.maxActiveKeys,
      defaultTtlDays: initialControl!.defaultTtlDays,
      expectedVersion: initialControl!.version,
      reason: `Enable local signed Webhook browser verification ${suffix}.`,
      confirmed: true,
    } })
    expect(enabled.ok()).toBeTruthy()

    await page.request.post('/api/v1/auth/logout')
    const registered = await page.request.post('/api/v1/auth/register', { data: {
      email,
      password,
      handle,
      displayName: 'Webhook Contract',
      locale: 'en-US',
      timezone: 'America/New_York',
    } })
    expect(registered.status()).toBe(201)

    await page.goto('/settings?section=developer')
    await expect(page.getByRole('heading', { name: 'Signed Webhooks' })).toBeVisible()
    await page.getByLabel('Endpoint name').fill(endpointName)
    await page.getByLabel('Endpoint URL').fill(endpointURL)
    await page.getByRole('button', { name: 'Create Webhook' }).click()
    await expect(page.getByText('Webhook endpoint created. Store the one-time signing secret now.')).toBeVisible()
    const secretPanel = page.locator('.settings-panel.developer-secret').last()
    const initialSecret = await secretPanel.locator('code').textContent()
		expect(initialSecret).toMatch(/^whsec_[A-Za-z0-9_-]+$/)
    const endpoint = page.locator('.webhook-endpoint-row').filter({ hasText: endpointName })
    await expect(endpoint).toBeVisible()

    await endpoint.getByRole('button', { name: 'Send test' }).click()
    await expect.poll(() => captured.length).toBeGreaterThanOrEqual(1)
    verifySignature(captured[0], initialSecret!)
    await expect(endpoint.getByText('Delivered', { exact: true }).first()).toBeVisible()

    const webhookControls = page.locator('.webhook-danger-controls')
    await webhookControls.getByLabel('Operation reason').fill(`Rotate after verifying the first signed request ${suffix}.`)
    await webhookControls.getByLabel(/I confirm this secret rotation/).check()
    await endpoint.getByRole('button', { name: 'Rotate secret' }).click()
    await expect(page.getByText('Webhook signing secret rotated. Existing queued deliveries retain their original secret revision.')).toBeVisible()
    const rotatedSecret = await secretPanel.locator('code').textContent()
		expect(rotatedSecret).toMatch(/^whsec_[A-Za-z0-9_-]+$/)
    expect(rotatedSecret).not.toBe(initialSecret)

    await endpoint.getByRole('button', { name: 'Send test' }).click()
    await expect.poll(() => captured.length).toBeGreaterThanOrEqual(2)
    verifySignature(captured[1], rotatedSecret!)

    receiverStatus = 400
    await endpoint.getByRole('button', { name: 'Send test' }).click()
    await expect.poll(() => captured.length).toBeGreaterThanOrEqual(3)
    verifySignature(captured[2], rotatedSecret!)
    await expect(endpoint.getByText('Dead letter', { exact: true }).first()).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)

		expect((await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })).ok()).toBeTruthy()
		await page.setViewportSize({ width: 390, height: 844 })
		await page.goto(`/admin?tab=developer&webhookQ=${suffix}&webhookEventType=developer.webhook.test&emailQ=no-match-${suffix}&emailKind=password_reset`)
		await expect(page.getByRole('searchbox', { name: 'Search Webhook recovery' })).toHaveValue(suffix)
		await expect(page.getByRole('combobox', { name: 'Event type' })).toHaveValue('developer.webhook.test')
		await expect(page.getByRole('searchbox', { name: 'Search email recovery' })).toHaveValue(`no-match-${suffix}`)
		await expect(page.getByRole('combobox', { name: 'Email action' })).toHaveValue('password_reset')
		await page.reload()
		await expect(page.getByRole('searchbox', { name: 'Search Webhook recovery' })).toHaveValue(suffix)
		expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)
		await page.setViewportSize({ width: 1280, height: 800 })
		const deadLetterSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Webhook dead letters' }) })
    const deadLetter = deadLetterSection.locator('.webhook-dead-letter-list article').filter({ hasText: endpointName })
    await expect(deadLetter).toBeVisible()
		await page.getByPlaceholder(/receiver recovery evidence/).fill(`Receiver recovered and its current signing secret was verified ${suffix}.`)
		await page.getByLabel(/I confirm this dead-letter delivery/).check()
    receiverStatus = 204
    await deadLetter.getByRole('button', { name: `Replay Webhook delivery for ${endpointName}` }).click()
    await expect(page.getByText('Webhook replay queued with audit and original-delivery lineage.')).toBeVisible()
    await expect.poll(() => captured.length).toBeGreaterThanOrEqual(4)
    verifySignature(captured[3], rotatedSecret!)

    await page.request.post('/api/v1/auth/logout')
    expect((await page.request.post('/api/v1/auth/login', { data: { email, password } })).ok()).toBeTruthy()
    await page.goto('/settings?section=developer')
    const restoredEndpoint = page.locator('.webhook-endpoint-row').filter({ hasText: endpointName })
    await expect(restoredEndpoint.getByText('Delivered', { exact: true }).first()).toBeVisible()
    await expect(restoredEndpoint.getByText('Dead letter', { exact: true }).first()).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1280)
  } finally {
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } }).catch(() => undefined)
    if (initialControl) {
      const currentResponse = await page.request.get('/api/v1/admin/developer/access').catch(() => undefined)
      if (currentResponse?.ok()) {
        const current = await currentResponse.json()
        await page.request.put('/api/v1/admin/developer/control', { data: {
          enabled: initialControl.enabled,
          maxServiceAccounts: initialControl.maxServiceAccounts,
          maxActiveKeys: initialControl.maxActiveKeys,
          defaultTtlDays: initialControl.defaultTtlDays,
          expectedVersion: current.control.version,
          reason: `Restore Developer Access after signed Webhook verification ${suffix}.`,
          confirmed: true,
        } }).catch(() => undefined)
      }
    }
    await new Promise<void>(resolve => receiver.close(() => resolve()))
  }
})
