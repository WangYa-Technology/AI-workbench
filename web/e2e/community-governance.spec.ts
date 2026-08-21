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
  const topicLink = post.locator('.community-post-title-link')
  const topicHref = await topicLink.getAttribute('href')
  expect(topicHref).toMatch(/^\/community\/posts\/[0-9a-f-]+$/)
  await topicLink.click()
  await expect(page).toHaveURL(topicHref!)
  await expect(page.getByRole('heading', { name: targetTitle!, exact: true })).toBeVisible()
  const reportButton = page.getByRole('button', { name: 'Report', exact: true })
  await expect(reportButton).toBeVisible()
  await reportButton.click()
  const reportForm = page.locator('.community-post-report')
  await reportForm.locator('select').selectOption('misleading')
  await reportForm.locator('textarea').fill(reportDetails)
  const reportResponsePromise = page.waitForResponse((response) => response.request().method() === 'POST' && /\/api\/v1\/community\/posts\/[^/]+\/reports$/.test(response.url()))
  await reportForm.getByRole('button', { name: 'Submit report', exact: true }).click()
  const reportResponse = await reportResponsePromise
  expect(reportResponse.ok()).toBeTruthy()
  const report = await reportResponse.json() as { resourceId: string }
  expect(report.resourceId).toBeTruthy()
  await expect(page.getByText('Report submitted for review.', { exact: true })).toBeVisible()

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto(`/admin?tab=risk&resourceType=post&resourceId=${encodeURIComponent(report.resourceId)}`)
  const reportSignal = page.locator('.risk-admin-list article').filter({ hasText: targetTitle! }).first()
  await expect(reportSignal).toContainText('Community report')
  await expect(reportSignal).toContainText('Open')
  await page.goto(`/admin?tab=governance&reportQ=${runID}&reportType=post&reportCategory=misleading&reportStatus=open`)
  await expect(page.getByRole('searchbox', { name: 'Search reports', exact: true })).toHaveValue(runID)
  await expect(page.getByRole('combobox', { name: 'Report category', exact: true })).toHaveValue('misleading')
  await expect(page).toHaveURL(new RegExp(`reportQ=${runID}.*reportType=post.*reportCategory=misleading.*reportStatus=open`))
  const reportRow = page.locator('.governance-admin-list article').filter({ hasText: reportDetails }).first()
  await expect(reportRow).toBeVisible()
  await reportRow.getByRole('button', { name: 'Resolve', exact: true }).click()
  await page.locator('.admin-command-panel select').selectOption('hidden')
  await page.getByLabel('Required reason', { exact: true }).fill(`E2E ${runID}: content hidden while disclosure evidence is reviewed.`)
  await page.getByLabel('I reviewed the target and confirm this operation.', { exact: true }).check()
  await page.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()
  await expect(reportRow).toHaveCount(0)

  const returnToReporter = await page.request.post('/api/v1/auth/login', { data: { email: reporterEmail, password: reporterPassword } })
  expect(returnToReporter.ok()).toBeTruthy()
  await page.goto('/community')
  await page.getByRole('button', { name: 'My reports & appeals', exact: true }).click()
  const caseRow = page.locator('.community-case-list article').filter({ hasText: reportDetails }).first()
  await expect(caseRow).toBeVisible()
  await caseRow.getByRole('button', { name: 'Appeal decision', exact: true }).click()
  await page.getByLabel('Appeal reason and supporting evidence', { exact: true }).fill(`E2E ${runID}: source evidence supports restoration after independent review.`)
  await page.getByRole('button', { name: 'Submit appeal', exact: true }).click()
  await expect(page.getByText('Appeal submitted for independent review.', { exact: true })).toBeVisible()

  const returnToAdmin = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(returnToAdmin.ok()).toBeTruthy()
  await page.goto(`/admin?tab=governance&appealQ=${runID}&appealType=post&appealStatus=pending`)
  await expect(page.getByRole('searchbox', { name: 'Search appeals', exact: true })).toHaveValue(runID)
  await expect(page).toHaveURL(new RegExp(`appealQ=${runID}.*appealType=post.*appealStatus=pending`))
  const appealRow = page.locator('.governance-admin-list article').filter({ hasText: `E2E ${runID}: source evidence` }).first()
  await expect(appealRow).toBeVisible()
  await appealRow.getByRole('button', { name: 'Resolve', exact: true }).click()
  await page.locator('.admin-command-panel select').selectOption('upheld')
  await page.getByLabel('Required reason', { exact: true }).fill(`E2E ${runID}: verified evidence supports restoring the Community work.`)
  await page.getByLabel('I reviewed the target and confirm this operation.', { exact: true }).check()
  await page.getByRole('button', { name: 'Apply and record', exact: true }).click()
  await expect(page.getByText('Operation completed and audit evidence recorded.', { exact: true })).toBeVisible()
  await expect(appealRow).toHaveCount(0)

  await page.goto('/community')
  await expect(page.locator('.community-post-row').filter({ hasText: targetTitle! }).first()).toBeVisible()
})

test('keeps Community governance usable without mobile overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const creatorSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(creatorSession.ok()).toBeTruthy()
  await page.goto('/community')
  await expect(page.getByRole('heading', { name: 'Community', exact: true })).toBeVisible()
  const firstPost = page.locator('.community-post-row').first()
  await expect(firstPost).toBeVisible()
  let widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
  await firstPost.locator('.community-post-title-link').click()
  await expect(page).toHaveURL(/\/community\/posts\/[0-9a-f-]+$/)
  await expect(page.locator('.community-post-article')).toBeVisible()
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.goto('/admin?tab=governance')
  await expect(page.getByRole('button', { name: 'Governance', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Report queue', exact: true })).toBeVisible()
  widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)
})

test('loads a real second page of owned Community governance history without duplicates', async ({ page }) => {
  test.setTimeout(120_000)
  const runID = Date.now().toString(36)
  const creatorSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(creatorSession.ok()).toBeTruthy()

  const uploads = await Promise.all(Array.from({ length: 21 }, (_, index) => page.request.post('/api/v1/assets/uploads', {
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
  await page.getByRole('button', { name: 'My reports & appeals', exact: true }).click()
  const cases = page.locator('.community-case-list article')
  const caseHistory = page.locator('.community-cases')
  await expect(cases).toHaveCount(20)
  await caseHistory.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(cases).toHaveCount(21)
  await expect(caseHistory.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)

  const creatorAgain = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(creatorAgain.ok()).toBeTruthy()
  await expect.poll(async () => {
    const response = await page.request.get('/api/v1/notification-deliveries?limit=50')
    if (!response.ok()) return false
    const deliveryPage = await response.json() as { items: Array<{ kind: string; status: string }> }
    const scanDeliveries = deliveryPage.items.filter(item => item.kind === 'asset.scan_completed')
    return scanDeliveries.length >= 21 && scanDeliveries.every(item => item.status !== 'queued')
  }, { timeout: 60_000 }).toBe(true)
})
