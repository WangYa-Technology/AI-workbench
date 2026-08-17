import { expect, test } from '@playwright/test'

test('completes private copyright intake through requester and Admin operations', async ({ page }) => {
  test.setTimeout(90_000)
  const runID = Date.now().toString(36)
  const subject = `Copyright intake ${runID}`
  const worksResponse = await page.request.get('/api/v1/works')
  expect(worksResponse.ok()).toBeTruthy()
  const works = await worksResponse.json() as { items: Array<{ id: string }> }
  expect(works.items.length).toBeGreaterThan(0)

  const requesterSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(requesterSession.ok()).toBeTruthy()
  await page.goto('/support')
  await page.getByRole('button', { name: 'New case', exact: true }).click()
  const intakeForm = page.locator('.support-form')
  await expect(intakeForm).toBeVisible()
  await intakeForm.getByRole('combobox').nth(0).selectOption('copyright')
  await intakeForm.getByRole('textbox').nth(0).fill(subject)
  await intakeForm.getByRole('textbox').nth(1).fill(`The referenced public work may reproduce controlled material. Review request ${runID}.`)
  await intakeForm.getByRole('combobox').nth(1).selectOption('work')
  await intakeForm.getByRole('textbox').nth(2).fill(works.items[0].id)
  await intakeForm.getByRole('combobox').nth(2).selectOption('rights_holder')
  await intakeForm.getByRole('textbox').nth(3).fill(`I control the relevant source material and request a bounded platform review for ${runID}.`)
  await page.getByRole('button', { name: 'Submit case', exact: true }).click()
  await expect(page.getByText('Support case created with an auditable intake record.', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
  const caseURL = page.url()
  expect(caseURL).toMatch(/\/support\/[0-9a-f-]{36}$/)

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto('/admin?tab=support')
  const supportFilters = page.locator('.admin-operations-filters')
  await supportFilters.getByLabel('Search cases', { exact: true }).fill(subject)
  await supportFilters.getByRole('combobox').nth(0).selectOption('open')
  await supportFilters.getByRole('combobox').nth(1).selectOption('copyright')
  await supportFilters.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(page).toHaveURL(/tab=support.*supportQ=Copyright(?:%20|\+)intake[^&]*&supportStatus=open.*supportCategory=copyright/)
  const caseButton = page.locator('.admin-support-queue > button').filter({ hasText: subject }).first()
  await expect(caseButton).toBeVisible()
  await caseButton.click()
  const controls = page.locator('.admin-support-controls form')
  const replyForm = controls.nth(0)
  await replyForm.getByRole('textbox').nth(0).fill('We received the copyright intake and started reviewing the stable resource reference.')
  await replyForm.getByRole('textbox').nth(1).fill(`E2E ${runID}: operator acknowledged the bounded copyright evidence.`)
  await replyForm.getByRole('checkbox').check()
  await replyForm.getByRole('button', { name: 'Send and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()

  const decisionForm = page.locator('.admin-support-controls form').nth(1)
  await decisionForm.getByRole('combobox').nth(0).selectOption('waiting_for_requester')
  await decisionForm.getByRole('textbox').fill(`E2E ${runID}: source publication date is needed to continue review.`)
  await decisionForm.getByRole('checkbox').check()
  await decisionForm.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.locator('.admin-support-detail').getByText('Needs your reply', { exact: true })).toBeVisible()

  const returnToRequester = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(returnToRequester.ok()).toBeTruthy()
  await page.goto(caseURL)
  await expect(page.getByText('We received the copyright intake and started reviewing the stable resource reference.', { exact: true })).toBeVisible()
  await page.locator('.support-reply textarea').fill(`The source was first published in a controlled catalog in January 2025. Reference ${runID}.`)
  await page.getByRole('button', { name: 'Send reply', exact: true }).click()
  await expect(page.getByText('Your reply was added to the case.', { exact: true })).toBeVisible()

  const returnToAdmin = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(returnToAdmin.ok()).toBeTruthy()
  await page.goto(`/admin?tab=support&supportQ=${encodeURIComponent(subject)}&supportStatus=in_review&supportCategory=copyright`)
  await page.locator('.admin-support-queue > button').filter({ hasText: subject }).first().click()
  const resolutionForm = page.locator('.admin-support-controls form').nth(1)
  await resolutionForm.getByRole('combobox').nth(0).selectOption('resolved')
  await resolutionForm.getByRole('combobox').nth(1).selectOption('content_restricted')
  await resolutionForm.getByRole('textbox').fill(`E2E ${runID}: reviewed evidence supports restricting the referenced content.`)
  await resolutionForm.getByRole('checkbox').check()
  await resolutionForm.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.locator('.admin-support-detail').getByText('Resolved', { exact: true })).toBeVisible()

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto('/notifications')
  await expect(page.getByText('Support case updated', { exact: true }).first()).toBeVisible()
})

test('keeps user and Admin support workspaces within a mobile viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  await page.goto('/support')
  await expect(page.getByRole('heading', { name: 'Support and copyright intake', exact: true })).toBeVisible()
  let widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  await page.goto('/admin?tab=support')
  await expect(page.getByRole('button', { name: 'Support', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Support queue', exact: true })).toBeVisible()
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})

test('loads a real second page of private owner support history', async ({ page }) => {
  const runID = Date.now().toString(36)
  const registered = await page.request.post('/api/v1/auth/register', { data: {
    email: `support-history-${runID}@example.test`,
    password: `support-history-${runID}`,
    handle: `support_${runID}`,
    displayName: 'Support History Test',
    locale: 'en-US',
    timezone: 'UTC',
  } })
  expect(registered.status()).toBe(201)
  for (let index = 0; index < 21; index += 1) {
    const created = await page.request.post('/api/v1/support/cases', { data: {
      category: 'general_support',
      subject: `Private support history ${runID} ${index + 1}`,
      details: `Private support pagination evidence ${runID} record ${index + 1} remains owner scoped.`,
      locale: 'en-US',
    } })
    expect(created.status()).toBe(201)
  }

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/support')
  const cases = page.locator('.support-case-index > button')
  await expect(cases).toHaveCount(20)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(cases).toHaveCount(21)
  await expect(page.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)
})
