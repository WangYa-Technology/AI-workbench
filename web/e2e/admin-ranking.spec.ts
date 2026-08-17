import { expect, test } from '@playwright/test'

test('evaluates, stages, and promotes an audited ranking candidate with index evidence', async ({ page }) => {
  test.setTimeout(120_000)
  const runID = Date.now().toString(36)
  const query = 'Architectural campaign workflow'

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
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
  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
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
  const reason = `Bounded browser verification of public product ranking candidate ${runID}.`
  await rankingForm.getByRole('textbox', { name: 'Required reason', exact: true }).fill(reason)
  await rankingForm.getByRole('checkbox', { name: 'I reviewed the weight ordering and confirm creation or activation of this immutable revision.', exact: true }).check()
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
  await evaluationForm.getByRole('textbox', { name: 'Required reason', exact: true }).fill(`Evaluate candidate ${runID} against deterministic public cases.`)
  await evaluationForm.getByRole('checkbox', { name: 'I confirm this immutable comparison should be recorded.', exact: true }).check()
  await evaluationForm.getByRole('button', { name: 'Run evaluation', exact: true }).click()
  await expect(page.getByText('Offline evaluation completed and immutable evidence recorded.', { exact: true })).toBeVisible()
  await expect(page.locator('.ranking-evaluation-list article').first()).toContainText('Passed')

  const indexForm = page.locator('.ranking-index-section form')
  await indexForm.getByRole('textbox', { name: 'Required reason', exact: true }).fill(`Refresh discovery index evidence for candidate ${runID}.`)
  await indexForm.getByRole('checkbox', { name: 'I confirm this maintenance run and its evidence should be recorded.', exact: true }).check()
  await indexForm.getByRole('button', { name: 'Analyze indexes', exact: true }).click()
  await expect(page.getByText('Search index statistics refreshed and evidence recorded.', { exact: true })).toBeVisible()
  await expect(page.locator('.ranking-index-list article').first()).toContainText('Succeeded')

  const rolloutPercent = page.locator('.ranking-control-grid select')
  const rolloutReason = page.locator('.ranking-control-grid textarea').nth(1)
  const rolloutConfirmation = page.locator('.ranking-control-grid input[type="checkbox"]').nth(1)
  const applyRollout = page.locator('.ranking-control-grid button[type="submit"]').nth(1)
  await expect(rolloutPercent).toHaveCount(1)
  await expect(rolloutPercent).toBeVisible()
  await rolloutPercent.selectOption('25')
  await rolloutReason.fill(`Stage candidate ${runID} to a bounded deterministic cohort.`)
  await rolloutConfirmation.check()
  await applyRollout.click()
  await expect(page.getByText('Ranking rollout updated with audit evidence.', { exact: true })).toBeVisible()
  await expect(rolloutSection.getByText('25%', { exact: true }).first()).toBeVisible()

  const promotionReason = `Promote evaluated candidate ${runID} after bounded browser verification.`
  await rolloutPercent.selectOption('100')
  await rolloutReason.fill(promotionReason)
  await rolloutConfirmation.check()
  await applyRollout.click()
  await expect(page.getByText('Ranking rollout updated with audit evidence.', { exact: true })).toBeVisible()
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
  await expect(page.getByText(`Ranking policy v${afterSearch.policyVersion} · ${afterSearch.policyName}`, { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: query, exact: true })).toBeVisible()

  await page.goto('/admin?tab=audit')
  await expect(page.getByText('admin.discovery_ranking_rollout_updated', { exact: true }).first()).toBeVisible()
  await expect(page.locator('.audit-list article').filter({ hasText: promotionReason }).first()).toBeVisible()
})
