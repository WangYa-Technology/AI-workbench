import { fixtureCredentials } from './helpers/identity'
import { expect, test, type Locator, type Page } from '@playwright/test'

async function chooseOption(page: Page, trigger: Locator, optionName: string) {
  await trigger.click()
  await page.getByRole('option', { name: optionName, exact: true }).click()
}

test('completes private copyright intake through requester and Admin operations', async ({ page }) => {
  test.setTimeout(90_000)
  const runID = Date.now().toString(36)
  const subject = `Copyright intake ${runID}`
  const worksResponse = await page.request.get('/api/v1/works')
  expect(worksResponse.ok()).toBeTruthy()
  const works = await worksResponse.json() as { items: Array<{ id: string }> }
  expect(works.items.length).toBeGreaterThan(0)

  const requesterSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(requesterSession.ok()).toBeTruthy()
  await page.goto('/support')
  await page.getByRole('button', { name: 'New case', exact: true }).click()
  const intakeForm = page.locator('.support-form')
  await expect(intakeForm).toBeVisible()
  await chooseOption(page, intakeForm.getByRole('combobox', { name: 'Case category', exact: true }), 'Copyright intake')
  await intakeForm.getByRole('textbox').nth(0).fill(subject)
  await intakeForm.getByRole('textbox').nth(1).fill(`The referenced public work may reproduce controlled material. Review request ${runID}.`)
  await chooseOption(page, intakeForm.getByRole('combobox', { name: 'Related resource type', exact: true }), 'Work')
  await intakeForm.getByRole('textbox').nth(2).fill(works.items[0].id)
  await chooseOption(page, intakeForm.getByRole('combobox', { name: 'Relationship to the rights', exact: true }), 'Rights holder')
  await intakeForm.getByRole('textbox').nth(3).fill(`I control the relevant source material and request a bounded platform review for ${runID}.`)
  await page.getByRole('button', { name: 'Submit case', exact: true }).click()
  await expect(page.getByText('Support case created with an auditable intake record.', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
  const caseURL = page.url()
  expect(caseURL).toMatch(/\/support\/[0-9a-f-]{36}$/)

  const adminSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto('/admin?tab=support')
  const supportFilters = page.locator('.admin-operations-filters')
  await supportFilters.getByLabel('Search cases', { exact: true }).fill(subject)
  await chooseOption(page, supportFilters.getByRole('combobox', { name: 'Status', exact: true }), 'Open')
  await chooseOption(page, supportFilters.getByRole('combobox', { name: 'Category', exact: true }), 'Copyright intake')
  await supportFilters.getByRole('button', { name: 'Apply filters', exact: true }).click()
  await expect(page).toHaveURL(/tab=support.*supportQ=Copyright(?:%20|\+)intake[^&]*&supportStatus=open.*supportCategory=copyright/)
  const caseButton = page.locator('.admin-support-queue > button').filter({ hasText: subject }).first()
  await expect(caseButton).toBeVisible()
  await caseButton.click()
  const controls = page.locator('.admin-support-controls form')
  const replyForm = controls.nth(0)
  await replyForm.getByRole('textbox').nth(0).fill('We received the copyright intake and started reviewing the stable resource reference.')
  await replyForm.getByRole('button', { name: 'Send and record', exact: true }).click()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()

  const decisionForm = page.locator('.admin-support-controls form').nth(1)
  await chooseOption(page, decisionForm.getByRole('combobox', { name: 'Status', exact: true }), 'Needs your reply')
  await decisionForm.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.locator('.admin-support-detail > header > em')).toHaveText('Needs your reply')

  const returnToRequester = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(returnToRequester.ok()).toBeTruthy()
  await page.goto(caseURL)
  await expect(page.getByText('We received the copyright intake and started reviewing the stable resource reference.', { exact: true })).toBeVisible()
  await page.locator('.support-reply textarea').fill(`The source was first published in a controlled catalog in January 2025. Reference ${runID}.`)
  await page.getByRole('button', { name: 'Send reply', exact: true }).click()
  await expect(page.getByText('Your reply was added to the case.', { exact: true })).toBeVisible()

  const returnToAdmin = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect(returnToAdmin.ok()).toBeTruthy()
  await page.goto(`/admin?tab=support&supportQ=${encodeURIComponent(subject)}&supportStatus=in_review&supportCategory=copyright`)
  await page.locator('.admin-support-queue > button').filter({ hasText: subject }).first().click()
  const resolutionForm = page.locator('.admin-support-controls form').nth(1)
  await chooseOption(page, resolutionForm.getByRole('combobox', { name: 'Status', exact: true }), 'Resolved')
  await chooseOption(page, resolutionForm.getByRole('combobox', { name: 'Resolution code', exact: true }), 'Content restricted')
  await resolutionForm.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.locator('.admin-support-detail > header > em')).toHaveText('Resolved')

  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/notifications')
  await expect(page.getByText('Support replied', { exact: true }).first()).toBeVisible()
})

test('keeps user and Admin support workspaces within a mobile viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/support')
  await expect(page.getByRole('heading', { name: 'Support and copyright intake', exact: true })).toBeVisible()
  let widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  await page.goto('/admin?tab=support')
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
