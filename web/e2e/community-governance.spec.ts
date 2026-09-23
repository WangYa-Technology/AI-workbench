import { randomUUID } from 'node:crypto'
import { fixtureCredentials } from './helpers/identity'
import { chooseOption, expectSelection } from './helpers/select'
import { expect, test } from '@playwright/test'

test('reports Community content, resolves it, appeals, and restores it', async ({ page }) => {
  const runID = Date.now().toString(36)
  const reportDetails = `E2E governance review ${runID}: verify the AI disclosure and source evidence.`
  const reporterEmail = `governance-${runID}@example.test`
  const reporterPassword = `governance-test-${runID}`

  const reporterSession = await page.request.post('/api/v1/auth/register', { data: {
    email: reporterEmail,
    password: reporterPassword,
    handle: `governance_${runID}`,
    displayName: 'Governance Workflow Test',
    locale: 'en-US',
    timezone: 'UTC',
  } })
  expect(reporterSession.ok()).toBeTruthy()
  await page.goto('/community')
  const post = page.locator('.community-post-row').first()
  await expect(post).toBeVisible()
  const targetTitle = (await post.getByRole('heading').textContent())?.trim()
  expect(targetTitle).toBeTruthy()
  const topicLink = post.locator('.ui-card-content__title a')
  const topicHref = await topicLink.getAttribute('href')
  expect(topicHref).toMatch(/^\/community\/posts\/[0-9a-f-]+$/)
  await topicLink.click()
  await expect(page).toHaveURL(topicHref!)
  await expect(page.getByRole('heading', { name: targetTitle!, exact: true, level: 1 })).toBeVisible()
  await page.getByRole('button', { name: 'More actions', exact: true }).click()
  const reportButton = page.getByRole('menuitem', { name: 'Report', exact: true })
  await expect(reportButton).toBeVisible()
  await reportButton.click()
  const reportForm = page.locator('.community-post-report')
  await chooseOption(reportForm.locator('select'), 'misleading')
  await reportForm.locator('textarea').fill(reportDetails)
  const reportResponsePromise = page.waitForResponse((response) => response.request().method() === 'POST' && /\/api\/v1\/community\/posts\/[^/]+\/reports$/.test(response.url()))
  await reportForm.getByRole('button', { name: 'Submit report', exact: true }).click()
  const reportResponse = await reportResponsePromise
  expect(reportResponse.ok()).toBeTruthy()
  const report = await reportResponse.json() as { id: string; resourceId: string }
  expect(report.resourceId).toBeTruthy()
  await expect(page.getByText('Report submitted for review.', { exact: true })).toBeVisible()

  const adminSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto(`/admin?tab=risk&resourceType=post&resourceId=${encodeURIComponent(report.resourceId)}`)
  const reportSignal = page.locator('.risk-admin-list article').filter({ hasText: targetTitle! }).first()
  await expect(reportSignal).toContainText('Community report')
  await expect(reportSignal).toContainText('Open')
  await page.goto(`/admin?tab=governance&reportQ=${runID}&reportType=post&reportCategory=misleading&reportStatus=open`)
  await expect(page.getByRole('searchbox', { name: 'Search reports', exact: true })).toHaveValue(runID)
  await expectSelection(page.getByRole('combobox', { name: 'Report category', exact: true }), 'misleading')
  await expect(page).toHaveURL(new RegExp(`reportQ=${runID}.*reportType=post.*reportCategory=misleading.*reportStatus=open`))
  const reportRow = page.locator('.governance-admin-list article').filter({ hasText: reportDetails }).first()
  await expect(reportRow).toBeVisible()
  await reportRow.getByRole('button', { name: 'Resolve', exact: true }).click()
  await chooseOption(page.locator('.admin-command-panel select'), 'hidden')
  await page.getByLabel('Decision reason', { exact: true }).fill('Reviewed the report and supporting evidence for this decision.')
  await page.getByRole('checkbox', { name: 'I have reviewed the content and confirm this decision' }).check()
  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await expect(reportRow).toHaveCount(0)

  const returnToReporter = await page.request.post('/api/v1/auth/login', { data: { email: reporterEmail, password: reporterPassword } })
  expect(returnToReporter.ok()).toBeTruthy()
  await page.goto(`/community/reports/${report.id}`)
  const caseRow = page.locator('.community-case-list article').filter({ hasText: reportDetails }).first()
  await expect(caseRow).toBeVisible()
  await caseRow.getByRole('button', { name: 'Appeal decision', exact: true }).click()
  await page.getByLabel('Appeal reason and supporting evidence', { exact: true }).fill(`E2E ${runID}: source evidence supports restoration after independent review.`)
  await page.getByRole('button', { name: 'Submit appeal', exact: true }).click()
  await expect(page.getByText('Appeal submitted for independent review.', { exact: true })).toBeVisible()

  const returnToAdmin = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect(returnToAdmin.ok()).toBeTruthy()
  await page.goto(`/admin?tab=governance&appealQ=${runID}&appealType=post&appealStatus=pending`)
  await expect(page.getByRole('searchbox', { name: 'Search appeals', exact: true })).toHaveValue(runID)
  await expect(page).toHaveURL(new RegExp(`appealQ=${runID}.*appealType=post.*appealStatus=pending`))
  const appealRow = page.locator('.governance-admin-list article').filter({ hasText: `E2E ${runID}: source evidence` }).first()
  await expect(appealRow).toBeVisible()
  await appealRow.getByRole('button', { name: 'Resolve', exact: true }).click()
  await chooseOption(page.locator('.admin-command-panel select'), 'upheld')
  await page.getByLabel('Decision reason', { exact: true }).fill('Reviewed the report and supporting evidence for this decision.')
  await page.getByRole('checkbox', { name: 'I have reviewed the content and confirm this decision' }).check()
  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.getByText('Operation completed.', { exact: true })).toBeVisible()
  await expect(appealRow).toHaveCount(0)

  await page.goto('/community')
  await expect(page.locator('.community-post-row').filter({ hasText: targetTitle! }).first()).toBeVisible()
})

test('keeps Community governance usable without mobile overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const creatorSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(creatorSession.ok()).toBeTruthy()
  await page.goto('/community')
  await expect(page.getByRole('heading', { name: 'Community', exact: true })).toBeVisible()
  const firstPost = page.locator('.community-post-row').first()
  await expect(firstPost).toBeVisible()
  let widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
  await firstPost.locator('.ui-card-content__title a').click()
  await expect(page).toHaveURL(/\/community\/posts\/[0-9a-f-]+$/)
  await expect(page.locator('.community-post-article')).toBeVisible()
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  const adminSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto('/admin?tab=governance')
  await expect(page.getByRole('region', { name: 'Governance', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Report queue', exact: true })).toBeVisible()
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})

test('loads a real second page of owned Community governance history without duplicates', async ({ page }) => {
  test.setTimeout(120_000)
  const runID = Date.now().toString(36)
  const creatorSession = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(creatorSession.ok()).toBeTruthy()

  const uploads = await Promise.all(Array.from({ length: 21 }, (_, index) => page.request.post('/api/v1/assets/uploads', { headers: { 'Idempotency-Key': randomUUID() },
    multipart: {
      title: `Governance pagination source ${runID} ${index + 1}`,
      file: {
        name: `governance-${runID}-${index + 1}.txt`,
        mimeType: 'text/plain',
        buffer: Buffer.from(`Independent governance pagination evidence ${runID} ${index + 1}`),
      },
    },
  })))
  uploads.forEach(response => expect(response.status()).toBe(201))
  const assets = await Promise.all(uploads.map(response => response.json() as Promise<{ id: string }>))
  await expect.poll(async () => {
    const responses = await Promise.all(assets.map(asset => page.request.get(`/api/v1/assets/${asset.id}`)))
    const items = await Promise.all(responses.map(response => response.json() as Promise<{ scanStatus: string }>))
    return items.every(item => item.scanStatus === 'clean')
  }, { timeout: 30_000 }).toBe(true)

  const publications = await Promise.all(assets.map((asset, index) => page.request.post('/api/v1/publications', { data: {
    assetId: asset.id,
    title: `Governance pagination work ${runID} ${index + 1}`,
    summary: `Bounded Community governance pagination fixture ${index + 1}.`,
    prompt: '',
    promptVisibility: 'private',
    aiDisclosure: 'Created from an explicit local E2E upload for pagination verification.',
    body: `Governance pagination post ${runID} ${index + 1}.`,
  } })))
  publications.forEach(response => expect(response.status()).toBe(201))
  const posts = await Promise.all(publications.map(response => response.json() as Promise<{ postId: string }>))

  const registered = await page.request.post('/api/v1/auth/register', { data: {
    email: `governance-history-${runID}@example.test`,
    password: `governance-history-${runID}`,
    handle: `history_${runID}`,
    displayName: 'Governance History Test',
    locale: 'en-US',
    timezone: 'UTC',
  } })
  expect(registered.status()).toBe(201)

  for (const [index, post] of posts.entries()) {
    const report = await page.request.post(`/api/v1/community/posts/${post.postId}/reports`, { data: {
      category: 'misleading',
      details: `Governance history ${runID} record ${index + 1} requires independent source review.`,
    } })
    expect(report.status()).toBe(201)
  }

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/community')
  await page.getByRole('button', { name: 'Community actions', exact: true }).click()
  await page.getByRole('menuitem', { name: 'My reports & appeals', exact: true }).click()
  const cases = page.locator('.community-case-list article')
  const caseHistory = page.locator('.community-cases')
  await expect(cases).toHaveCount(20)
  await caseHistory.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(cases).toHaveCount(21)
  await expect(caseHistory.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)

  const creatorAgain = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(creatorAgain.ok()).toBeTruthy()
  await expect.poll(async () => {
    const response = await page.request.get('/api/v1/notification-deliveries?limit=50')
    if (!response.ok()) return false
    const deliveryPage = await response.json() as { items: Array<{ kind: string; status: string }> }
    const scanDeliveries = deliveryPage.items.filter(item => item.kind === 'asset.scan_completed')
    return scanDeliveries.length >= 21 && scanDeliveries.every(item => item.status !== 'queued')
  }, { timeout: 60_000 }).toBe(true)
})
