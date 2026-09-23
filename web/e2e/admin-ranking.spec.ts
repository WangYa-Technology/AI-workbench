import { fixtureCredentials } from './helpers/identity'
import { chooseOption } from './helpers/select'
import { expect, test } from '@playwright/test'

test('evaluates, stages, and promotes a versioned ranking candidate with index evidence', async ({ page }) => {
  test.setTimeout(120_000)
  const query = 'Architectural campaign workflow'

  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  const denied = await page.request.get('/api/v1/admin/discovery/ranking')
  expect(denied.status()).toBe(403)

  const beforeSearchResponse = await page.request.get(`/api/v1/search?q=${encodeURIComponent(query)}&types=product`)
  expect(beforeSearchResponse.ok()).toBeTruthy()
  const beforeSearch = (await beforeSearchResponse.json()) as {
    policyVersion: number
    items: Array<{ title: string; rank: number }>
  }
  const beforeProduct = beforeSearch.items.find((item) => item.title === query)
  expect(beforeProduct).toBeDefined()

  await page.setViewportSize({ width: 390, height: 844 })
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
  const policyResponse = await page.request.get('/api/v1/admin/discovery/ranking')
  expect(policyResponse.ok()).toBeTruthy()
  const policy = (await policyResponse.json()) as {
    current: { version: number; productTypeBoost: number }
  }
  expect(policy.current.version).toBe(beforeSearch.policyVersion)
  const nextBoost = policy.current.productTypeBoost < 50 ? policy.current.productTypeBoost + 1 : policy.current.productTypeBoost - 1
  const expectedDelta = nextBoost - policy.current.productTypeBoost

  await page.goto('/admin?tab=ranking')
  await expect(page.getByRole('heading', { name: 'Public search ranking', exact: true })).toBeVisible()
  await expect(page.getByText(`v${policy.current.version}`, { exact: true }).first()).toBeVisible()
  const widths = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(widths.scroll).toBe(widths.client)

  const rankingForm = page.locator('.ranking-policy-form')
  await rankingForm.getByRole('spinbutton', { name: 'Product result boost', exact: true }).fill(String(nextBoost))
  await rankingForm.getByRole('button', { name: 'Create candidate', exact: true }).click()

  await expect(page.getByText('Candidate revision created. Public traffic is unchanged.', { exact: true })).toBeVisible()
  const candidatePolicyResponse = await page.request.get('/api/v1/admin/discovery/ranking')
  const candidatePolicy = await candidatePolicyResponse.json() as { candidate: { version: number } }
  expect(candidatePolicy.candidate).toBeDefined()
  const candidateVersion = candidatePolicy.candidate.version
  await expect(page.getByText(`v${candidateVersion}`, { exact: false }).first()).toBeVisible()
  const unchangedSearchResponse = await page.request.get(`/api/v1/search?q=${encodeURIComponent(query)}&types=product`)
  const unchangedSearch = await unchangedSearchResponse.json() as { policyVersion: number; items: Array<{ title: string; rank: number }> }
  expect(unchangedSearch.policyVersion).toBe(policy.current.version)
  expect(unchangedSearch.items.find((item) => item.title === query)?.rank).toBe(beforeProduct!.rank)

  const rolloutSection = page.locator('.ranking-rollout-section')
  const evaluationForm = rolloutSection.locator('.ranking-control-grid > form').nth(0)
  await evaluationForm.getByRole('button', { name: 'Run evaluation', exact: true }).click()
  await expect(page.getByText('Offline evaluation completed.', { exact: true })).toBeVisible()
  await expect(page.locator('.ranking-evaluation-list article').first()).toContainText('Passed')

  const indexForm = page.locator('.ranking-index-section form')
  await indexForm.getByRole('button', { name: 'Analyze indexes', exact: true }).click()
  await expect(page.getByText('Search index statistics refreshed.', { exact: true })).toBeVisible()
  await expect(page.locator('.ranking-index-list article').first()).toContainText('Succeeded')

  const rolloutPercent = page.locator('.ranking-control-grid').getByRole('combobox')
  const applyRollout = page.locator('.ranking-control-grid button[type="submit"]').nth(1)
  await expect(rolloutPercent).toHaveCount(1)
  await expect(rolloutPercent).toBeVisible()
  await chooseOption(rolloutPercent, '25')
  await applyRollout.click()
  await expect(page.getByText('Ranking rollout updated.', { exact: true })).toBeVisible()
  await expect(rolloutSection.getByText('25%', { exact: true }).first()).toBeVisible()

  await chooseOption(rolloutPercent, '100')
  await applyRollout.click()
  await expect(page.getByText('Ranking rollout updated.', { exact: true })).toBeVisible()
  await expect(rolloutSection.getByText('No candidate', { exact: true })).toBeVisible()
  const activeHistory = page.locator('.ranking-history-list article').first()
  await expect(activeHistory).toContainText('Active')

  const afterSearchResponse = await page.request.get(`/api/v1/search?q=${encodeURIComponent(query)}&types=product`)
  expect(afterSearchResponse.ok()).toBeTruthy()
  const afterSearch = (await afterSearchResponse.json()) as {
    policyVersion: number
    policyName: string
    items: Array<{ title: string; rank: number }>
  }
  const afterProduct = afterSearch.items.find((item) => item.title === query)
  expect(afterProduct).toBeDefined()
  expect(afterSearch.policyVersion).toBe(candidateVersion)
  expect(afterProduct!.rank).toBe(beforeProduct!.rank + expectedDelta)

  await page.goto(`/search?q=${encodeURIComponent(query)}&types=product`)
  await expect(page.getByRole('heading', { name: query, exact: true })).toBeVisible()

})
