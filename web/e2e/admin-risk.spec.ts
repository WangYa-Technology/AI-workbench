import { expect, test } from '@playwright/test'

test('turns a disputed task into a permission-controlled, audited risk review', async ({ page }) => {
  test.setTimeout(90_000)
  const runID = Date.now().toString(36)
  const title = `Risk review image brief ${runID}`

  const publisherSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  const publisher = (await publisherSession.json()) as { user: { id: string } }
  const statementResponse = await page.request.get('/api/v1/billing/statement')
  const statement = (await statementResponse.json()) as { account: { availableCents: number } }
  if (statement.account.availableCents < 50_000) {
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
    const adjustment = await page.request.post(`/api/v1/admin/finance/accounts/${publisher.user.id}/adjust`, {
      data: {
        deltaCents: 100_000 - statement.account.availableCents,
        currency: 'USD',
        reason: `Repeatable risk workflow funding ${runID}`,
        confirmed: true,
      },
    })
    expect(adjustment.ok()).toBeTruthy()
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  }

  const createResponse = await page.request.post('/api/v1/tasks', {
    headers: { 'Idempotency-Key': `risk-task-${runID}` },
    data: {
      title,
      summary: 'A bounded Local Test brief used to verify the operations risk-review boundary.',
      brief: 'Create an editorial image with stable geometry, one clear subject, and complete source evidence.',
      deliverableType: 'image',
      deliverables: ['One 2400px master image'],
      acceptanceRules: ['No third-party marks', 'Source information is complete'],
      rightsTerms: 'Worldwide editorial use for six months.',
      aiDisclosureRequirement: 'List the model and every source asset.',
      budgetCents: 42_000,
      currency: 'USD',
      deadline: new Date(Date.now() + 14 * 24 * 60 * 60 * 1000).toISOString(),
      clientTimezone: 'Europe/London',
      allowDirectAccept: true,
    },
  })
  expect(createResponse.ok()).toBeTruthy()
  const created = (await createResponse.json()) as { id: string }

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto(`/market/demands/${created.id}`)
  await page.getByRole('button', { name: 'Accept task', exact: true }).click()
  await page.getByLabel('Delivery note', { exact: true }).fill('Delivery submitted with Local Test source evidence and model disclosure.')
  await page.getByRole('button', { name: 'Submit delivery', exact: true }).click()
  await page.getByRole('button', { name: 'Open dispute', exact: true }).click()
  await page.getByLabel('Dispute reason', { exact: true }).fill('The requested interpretation conflicts with the acceptance wording in the published brief.')
  await page.getByRole('button', { name: 'Open dispute', exact: true }).click()
  await expect(page.getByText('Dispute opened. Settlement remains paused.', { exact: true })).toBeVisible()

  const deniedQueue = await page.request.get('/api/v1/admin/risk/signals')
  expect(deniedQueue.status()).toBe(403)

  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  await page.goto('/admin?tab=risk')
  await expect(page.getByRole('heading', { name: 'Cross-domain risk review', exact: true })).toBeVisible()
  const riskFilters = page.locator('.admin-operations-filters')
  await riskFilters.getByLabel('Search signals', { exact: true }).fill(created.id)
  await riskFilters.getByRole('combobox').nth(0).selectOption('open')
  await riskFilters.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`tab=risk.*riskQ=${created.id}.*riskStatus=open`))
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  const signal = page.locator('.risk-admin-list article').filter({ hasText: title })
  await expect(signal).toContainText('Task dispute')
  await expect(signal).toContainText('Open')
  await expect(signal).toContainText('v1')
  await signal.getByRole('button', { name: 'Review signal', exact: true }).click()
  const commandPanel = page.locator('.admin-command-panel')
  await expect(commandPanel).toBeInViewport()
  await commandPanel.getByRole('combobox', { name: 'Review decision', exact: true }).selectOption('no_action')
  const reviewReason = `Reviewed the bounded Local Test dispute evidence and closed without action ${runID}.`
  await commandPanel.getByRole('textbox', { name: 'Required reason', exact: true }).fill(reviewReason)
  await commandPanel.getByRole('checkbox', { name: 'I reviewed the target and confirm this operation.', exact: true }).check()
  await commandPanel.getByRole('button', { name: 'Apply and record', exact: true }).click()

  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()
	await expect(signal).toHaveCount(0)
	await riskFilters.getByRole('combobox').nth(0).selectOption('dismissed')
	await riskFilters.getByRole('button', { name: 'Apply filters', exact: true }).click()
	await expect(page).toHaveURL(new RegExp(`tab=risk.*riskQ=${created.id}.*riskStatus=dismissed`))
  await expect(signal).toContainText('No action')
  await expect(signal).toContainText('v2')
  await expect(signal.getByRole('link', { name: 'Open resource', exact: true })).toHaveAttribute('href', `/market/demands?task=${created.id}`)

  await page.getByRole('button', { name: 'Audit', exact: true }).click()
  await expect(page.getByText('admin.risk_reviewed', { exact: true }).first()).toBeVisible()
  await expect(page.locator('.audit-list article').filter({ hasText: reviewReason }).first()).toBeVisible()
})

test('activates an immutable risk rule revision for subsequent dispute signals', async ({ page }) => {
  test.setTimeout(90_000)
  const runID = Date.now().toString(36)
  const title = `Versioned risk brief ${runID}`
  const changeReason = `Increase subsequent dispute classification for bounded workflow ${runID}.`

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  const deniedRules = await page.request.get('/api/v1/admin/risk/rules')
  expect(deniedRules.status()).toBe(403)

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  await page.goto('/admin?tab=riskRules')
  await expect(page.getByRole('heading', { name: 'Cross-domain risk rules', exact: true })).toBeVisible()
  const form = page.locator('.risk-rules-admin form')
  const previousVersionText = await page.locator('.risk-rules-admin > section').first().locator('header > span').textContent()
  const previousVersion = Number(previousVersionText?.replace('v', ''))
  await form.getByRole('textbox', { name: 'Revision name', exact: true }).fill(`Dispute sensitivity ${runID}`)
  await form.getByRole('spinbutton', { name: 'Task dispute score', exact: true }).fill('93')
  await form.getByRole('spinbutton', { name: 'Transaction refund score', exact: true }).fill('47')
  await form.getByRole('spinbutton', { name: 'Community report score', exact: true }).fill('39')
  await form.getByRole('spinbutton', { name: 'Media rejection score', exact: true }).fill('78')
  await form.getByRole('spinbutton', { name: 'Registration account-link score', exact: true }).fill('65')
  await form.getByRole('spinbutton', { name: 'Minimum distinct accounts', exact: true }).fill('3')
  await form.getByRole('spinbutton', { name: 'Observation window in hours', exact: true }).fill('24')
  await expect(form.getByText('Only aggregate counts and the immutable rule revision are retained in risk evidence. Raw IP addresses, device fingerprints, network hashes, and linked-account lists are not stored.', { exact: true })).toBeVisible()
  await form.getByRole('spinbutton', { name: 'Medium threshold', exact: true }).fill('35')
  await form.getByRole('spinbutton', { name: 'High threshold', exact: true }).fill('65')
  await form.getByRole('spinbutton', { name: 'Critical threshold', exact: true }).fill('90')
  await form.getByRole('textbox', { name: 'Required reason', exact: true }).fill(changeReason)
  await form.getByRole('checkbox', { name: 'I reviewed the scores and ordered thresholds and confirm this immutable revision.', exact: true }).check()
  await form.getByRole('button', { name: 'Activate revision', exact: true }).click()
  await expect(page.getByText('Risk rule revision activated with audit evidence.', { exact: true })).toBeVisible()
  await expect(page.locator('.risk-rules-admin > section').first().locator('header > span')).toHaveText(`v${previousVersion + 1}`)

  const publisherSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  const publisher = (await publisherSession.json()) as { user: { id: string } }
  const statementResponse = await page.request.get('/api/v1/billing/statement')
  const statement = (await statementResponse.json()) as { account: { availableCents: number } }
  if (statement.account.availableCents < 20_000) {
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
    const adjustment = await page.request.post(`/api/v1/admin/finance/accounts/${publisher.user.id}/adjust`, {
      data: { deltaCents: 50_000, currency: 'USD', reason: `Repeatable versioned risk workflow funding ${runID}`, confirmed: true },
    })
    expect(adjustment.ok()).toBeTruthy()
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  }
  const createResponse = await page.request.post('/api/v1/tasks', {
    headers: { 'Idempotency-Key': `risk-rules-task-${runID}` },
    data: {
      title,
      summary: 'A bounded brief proving that only subsequent signals use the active risk revision.',
      brief: 'Create one editorial image with a stable subject and complete deterministic Local Test source evidence.',
      deliverableType: 'image', deliverables: ['One image'], acceptanceRules: ['Complete source evidence'],
      rightsTerms: 'Worldwide editorial use for six months.', aiDisclosureRequirement: 'List the model and source assets.',
      budgetCents: 12_000, currency: 'USD', deadline: new Date(Date.now() + 14 * 24 * 60 * 60 * 1000).toISOString(),
      clientTimezone: 'Europe/London', allowDirectAccept: true,
    },
  })
  expect(createResponse.ok()).toBeTruthy()
  const created = (await createResponse.json()) as { id: string }

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto(`/market/demands/${created.id}`)
  await page.getByRole('button', { name: 'Accept task', exact: true }).click()
  await page.getByLabel('Delivery note', { exact: true }).fill('Submitted with deterministic Local Test source and model evidence.')
  await page.getByRole('button', { name: 'Submit delivery', exact: true }).click()
  await page.getByRole('button', { name: 'Open dispute', exact: true }).click()
  await page.getByLabel('Dispute reason', { exact: true }).fill('Acceptance wording conflicts with the requested interpretation and requires a versioned risk signal.')
  await page.getByRole('button', { name: 'Open dispute', exact: true }).click()
  await expect(page.getByText('Dispute opened. Settlement remains paused.', { exact: true })).toBeVisible()

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  const signalsResponse = await page.request.get(`/api/v1/admin/risk/signals?resourceType=task&resourceId=${created.id}`)
  expect(signalsResponse.ok()).toBeTruthy()
  const signals = (await signalsResponse.json()) as { items: Array<{ resourceId: string; score: number; severity: string; evidence: Record<string, unknown> }> }
  const signal = signals.items.find((item) => item.resourceId === created.id)
  expect(signal).toBeDefined()
  expect(signal).toMatchObject({ score: 93, severity: 'critical' })
  expect(signal?.evidence.riskRuleVersion).toBe(previousVersion + 1)
  expect(signal?.evidence.riskRuleRevisionId).toMatch(/^[0-9a-f-]{36}$/)

  await page.goto('/admin?tab=audit')
  await expect(page.getByText('admin.risk_rules_updated', { exact: true }).first()).toBeVisible()
  await expect(page.locator('.audit-list article').filter({ hasText: changeReason }).first()).toBeVisible()
})

test('routes privacy-minimized registration account-link evidence to the exact Admin account', async ({ page }) => {
  test.setTimeout(90_000)
  const runID = Date.now().toString(36)
  let subject: { id: string; handle: string } | undefined

  for (let index = 1; index <= 3; index += 1) {
    const handle = `link_${runID}_${index}`
    const registration = await page.request.post('/api/v1/auth/register', { data: {
      email: `${handle}@test.local`,
      password: `account-link-${runID}`,
      handle,
      displayName: `Account Link ${index}`,
      locale: 'en-US',
      timezone: 'UTC',
    } })
    expect(registration.ok()).toBeTruthy()
    const body = (await registration.json()) as { user: { id: string; handle: string } }
    if (index === 3) subject = body.user
  }
  expect(subject).toBeDefined()

  const denied = await page.request.get(`/api/v1/admin/risk/signals?resourceType=user&resourceId=${subject?.id}`)
  expect(denied.status()).toBe(403)

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  const focused = await page.request.get(`/api/v1/admin/risk/signals?resourceType=user&resourceId=${subject?.id}`)
  expect(focused.ok()).toBeTruthy()
  const focusedBody = (await focused.json()) as { items: Array<{
    resourceType: string
    resourceId: string
    resourceTitle: string
    targetPath: string
    signalType: string
    evidence: Record<string, unknown>
  }> }
  expect(focusedBody.items).toHaveLength(1)
  expect(focusedBody.items[0]).toMatchObject({
    resourceType: 'user',
    resourceId: subject?.id,
    resourceTitle: `Account @${subject?.handle}`,
    targetPath: `/admin?tab=users&q=${subject?.handle}`,
    signalType: 'account_link',
  })
  expect(focusedBody.items[0].evidence).toMatchObject({ networkDataStored: false })
  for (const forbidden of ['networkHash', 'ipAddress', 'deviceFingerprint', 'linkedAccounts']) {
    expect(focusedBody.items[0].evidence).not.toHaveProperty(forbidden)
  }

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/admin?tab=risk&riskQ=${subject?.handle}&riskStatus=open`)
  const signal = page.locator('.risk-admin-list article').filter({ hasText: `Account @${subject?.handle}` })
  await expect(signal).toContainText('Registration account link')
  await expect(signal).toContainText('Open')
  await expect(signal.locator('a.text-link')).toHaveAttribute('href', `/admin?tab=users&q=${subject?.handle}`)
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})
