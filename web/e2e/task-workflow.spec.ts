import { expect, test } from '@playwright/test'

test('completes proposal, task creation, revision, delivery, and local test settlement', async ({ page }) => {
	test.setTimeout(90_000)
  const runID = Date.now().toString(36)
  const title = `Editorial launch image system ${runID}`
  const deadline = new Date(Date.now() + 21 * 24 * 60 * 60 * 1000).toISOString().slice(0, 16)

  const publisherSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  const publisher = (await publisherSession.json()) as { user: { id: string } }
  const statementResponse = await page.request.get('/api/v1/billing/statement')
  const statement = (await statementResponse.json()) as { account: { availableCents: number } }
  if (statement.account.availableCents < 100_000) {
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
    const adjustment = await page.request.post(`/api/v1/admin/finance/accounts/${publisher.user.id}/adjust`, {
      data: {
        deltaCents: 150_000 - statement.account.availableCents,
        currency: 'USD',
        reason: `Repeatable task workflow funding ${runID}`,
        confirmed: true,
      },
    })
    expect(adjustment.ok()).toBeTruthy()
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  }
  await page.goto('/market/demands')
  await page.getByRole('button', { name: 'Publish brief', exact: true }).click()
  await page.getByLabel('Task title', { exact: true }).fill(title)
  await page.getByLabel('Short summary', { exact: true }).fill('A disciplined image system for an international research launch.')
  await page.getByLabel('Production brief', { exact: true }).fill('Create a coherent hero image and social crop with clear subject separation, restrained material detail, and space for editorial typography.')
  await page.getByLabel('Reward (USD)', { exact: true }).fill('800')
  await page.getByLabel('Deadline', { exact: true }).fill(deadline)
  await page.getByLabel('Commissioner timezone', { exact: true }).selectOption('Europe/London')
  await page.getByLabel('Deliverables, one per line', { exact: true }).fill('3000px master image\nVertical social crop\nPrompt and model disclosure')
  await page.getByLabel('Acceptance rules, one per line', { exact: true }).fill('No third-party marks\nSubject remains legible at thumbnail size\nAll source media is disclosed')
  await page.getByLabel('Rights terms', { exact: true }).fill('Worldwide campaign use for twelve months. Creator retains portfolio display rights after launch.')
  await page.getByLabel('AI disclosure requirements', { exact: true }).fill('List every model, source asset, and manual post-production step used in the final delivery.')
  await page.getByRole('button', { name: 'Publish task', exact: true }).click()

  await expect(page).toHaveURL(/\/market\/demands\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Use creator account', exact: true }).click()
  await page.getByRole('button', { name: 'Submit proposal', exact: true }).click()
  await page.getByLabel('Approach', { exact: true }).fill('I will lock composition and subject separation first, then complete two visual quality passes before delivery.')
  await page.getByLabel('Planned delivery', { exact: true }).fill('Master image, vertical crop, and reproducible prompt notes.')
  await page.getByLabel('Proposed amount (USD)', { exact: true }).fill('750')
  await page.getByLabel('Timeline (days)', { exact: true }).fill('6')
  await page.getByRole('button', { name: 'Submit proposal', exact: true }).click()
  await expect(page.getByText('Proposal submitted.', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Your proposal', exact: true })).toBeVisible()
  await expect(page.getByText('Master image, vertical crop, and reproducible prompt notes.', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Use publisher account', exact: true }).click()
  await page.getByRole('button', { name: 'Accept proposal', exact: true }).click()
  await expect(page.getByText('Creator assigned.', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Use creator account', exact: true }).click()
  await page.getByRole('link', { name: 'Create for this task', exact: true }).click()
  await expect(page.locator('.source-reference').getByText(title, { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Generate image', exact: true }).click()
  await expect(page.locator('.studio-task').first().locator('.studio-task-status')).toContainText('Saved to Assets')
  await page.locator('.studio-task').first().locator('.studio-task-copy').click()
  await page.getByRole('dialog', { name: 'Generation details' }).getByRole('link', { name: 'Submit delivery', exact: true }).click()
  await page.getByLabel('Delivery note', { exact: true }).fill('First delivery with source context and model disclosure attached.')
  await page.getByRole('button', { name: 'Submit delivery', exact: true }).click()
  await expect(page.getByText('Delivery submitted for review.', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Use publisher account', exact: true }).click()
  await page.getByLabel('Review note', { exact: true }).fill('Increase subject separation and remove the repeated texture at the right edge.')
  await page.getByRole('button', { name: 'Request revision', exact: true }).click()
  await expect(page.getByText('Revision request sent.', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Use creator account', exact: true }).click()
  await page.getByLabel('Delivery note', { exact: true }).fill('Revised delivery with stronger separation and the edge texture removed.')
  await page.getByRole('button', { name: 'Submit delivery', exact: true }).click()
  await page.getByRole('button', { name: 'Use publisher account', exact: true }).click()
  await page.getByLabel('Review note', { exact: true }).fill('The revision satisfies every published acceptance rule.')
  await page.getByRole('button', { name: 'Accept delivery', exact: true }).click()

  await expect(page.getByText('Delivery accepted and Local Test settlement recorded.', { exact: true })).toBeVisible()
  await expect(page.getByText('$750.00 / Local Test USD', { exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Tasks', exact: true }).click()
  await expect(page).toHaveURL(/\/workspace\/tasks$/)
  await expect(page.getByRole('link', { name: new RegExp(title) })).toBeVisible()
})

test('directly accepts a brief and pauses settlement through a dispute', async ({ page }) => {
  const runID = Date.now().toString(36)
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  const createResponse = await page.request.post('/api/v1/tasks', {
    headers: { 'Idempotency-Key': `dispute-task-${runID}` },
    data: {
      title: `Direct image brief ${runID}`,
      summary: 'A direct-accept brief used to verify the local dispute boundary.',
      brief: 'Create a clean editorial image with stable geometry, a clear focal subject, and no third-party marks or identifiable people.',
      deliverableType: 'image',
      deliverables: ['One 3000px master image'],
      acceptanceRules: ['No third-party marks', 'Source information is complete'],
      rightsTerms: 'Worldwide campaign use for six months.',
      aiDisclosureRequirement: 'List the model and all source media.',
      budgetCents: 42000,
      currency: 'USD',
      deadline: new Date(Date.now() + 14 * 24 * 60 * 60 * 1000).toISOString(),
      clientTimezone: 'Europe/London',
      allowDirectAccept: true,
    },
  })
  expect(createResponse.ok()).toBeTruthy()
  const created = await createResponse.json() as { id: string }

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto(`/market/demands/${created.id}`)
  await page.getByRole('button', { name: 'Accept task', exact: true }).click()
  await page.getByLabel('Delivery note', { exact: true }).fill('Direct delivery with local source evidence attached.')
  await page.getByRole('button', { name: 'Submit delivery', exact: true }).click()
  await page.getByRole('button', { name: 'Open dispute', exact: true }).click()
  await page.getByLabel('Dispute reason', { exact: true }).fill('The requested interpretation conflicts with the acceptance wording published in the original brief.')
  await page.getByRole('button', { name: 'Open dispute', exact: true }).click()

  await expect(page.getByText('Dispute opened. Settlement remains paused.', { exact: true })).toBeVisible()
  await expect(page.getByText('Disputed', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('No real funds move until a production payment provider is approved.', { exact: true })).toBeVisible()
})

test('keeps the mobile task marketplace keyboard-ready with reduced motion', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' })
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  await page.goto('/market/demands')

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)

  const publishButton = page.getByRole('button', { name: 'Publish brief', exact: true })
  await publishButton.click()
  const dialog = page.getByRole('dialog', { name: 'Publish a production brief' })
  await expect(dialog).toBeVisible()
  await expect(page.getByLabel('Task title', { exact: true })).toBeFocused()
  const transitionDuration = await page.getByLabel('Task title', { exact: true }).evaluate((element) => Number.parseFloat(getComputedStyle(element).transitionDuration))
  expect(transitionDuration).toBeGreaterThan(0)
  expect(transitionDuration).toBeLessThanOrEqual(0.00001)

  await page.keyboard.press('Shift+Tab')
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true }).first()).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  await expect(dialog.getByRole('button', { name: 'Publish task', exact: true })).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true }).first()).toBeFocused()

  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(publishButton).toBeFocused()
})

test('lets a commissioner cancel an open task with durable evidence', async ({ page }) => {
  const runID = Date.now().toString(36)
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'publisher' } })
  const createResponse = await page.request.post('/api/v1/tasks', {
    headers: { 'Idempotency-Key': `cancel-task-${runID}` },
    data: {
      title: `Cancelable editorial brief ${runID}`,
      summary: 'An open brief used to verify commissioner cancellation evidence.',
      brief: 'Create an editorial image direction with stable geometry, clear typography space, and a documented source trail.',
      deliverableType: 'image',
      deliverables: ['One 2400px master image'],
      acceptanceRules: ['No third-party marks', 'Source information is complete'],
      rightsTerms: 'Worldwide editorial use for six months.',
      aiDisclosureRequirement: 'List the model and all source media.',
      budgetCents: 54000,
      currency: 'USD',
      deadline: new Date(Date.now() + 14 * 24 * 60 * 60 * 1000).toISOString(),
      clientTimezone: 'America/New_York',
      allowDirectAccept: false,
    },
  })
  expect(createResponse.ok()).toBeTruthy()
  const created = await createResponse.json() as { id: string }

  await page.goto(`/market/demands/${created.id}`)
  await page.getByRole('button', { name: 'Cancel task', exact: true }).click()
  await expect(page.getByText('Cancellation closes every pending proposal and cannot be undone.', { exact: true })).toBeVisible()
  await page.getByLabel('Cancellation reason', { exact: true }).fill('The launch schedule changed before a creator was selected.')
  await page.getByRole('button', { name: 'Cancel task', exact: true }).last().click()

  await expect(page.getByText('Task cancelled and pending proposals closed.', { exact: true })).toBeVisible()
  await expect(page.getByText('Cancelled', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('The launch schedule changed before a creator was selected.', { exact: true })).toBeVisible()
  await expect(page.getByText('No real funds move until a production payment provider is approved.', { exact: true })).toBeVisible()
})
