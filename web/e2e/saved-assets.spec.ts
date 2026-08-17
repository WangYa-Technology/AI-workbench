import { expect, test } from '@playwright/test'

test('saves Community work as a reference-only workspace item', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  const communityResponse = await page.request.get('/api/v1/community/posts')
  expect(communityResponse.ok()).toBeTruthy()
  const community = await communityResponse.json() as { items: Array<{ id: string; workId: string; workTitle: string; viewerBookmarked: boolean }> }
  expect(community.items.length).toBeGreaterThan(0)
  for (const item of community.items.filter(entry => entry.viewerBookmarked)) {
    expect((await page.request.put(`/api/v1/community/posts/${item.id}/reactions/bookmark`, { data: { active: false } })).ok()).toBeTruthy()
  }
  const target = community.items[0]!
  expect((await page.request.put(`/api/v1/community/posts/${target.id}/reactions/bookmark`, { data: { active: true } })).ok()).toBeTruthy()

  await page.goto('/workspace/assets?view=saved')
  await expect(page.getByRole('link', { name: 'Saved Works', exact: true })).toHaveClass(/active/)
  await expect(page.getByText('Saving is not a license grant', { exact: true })).toBeVisible()
  await expect(page.getByText(/no download, commercial, or derivative rights/i)).toBeVisible()
  const savedCard = page.locator('.saved-work-card').filter({ hasText: target.workTitle })
  await expect(savedCard).toBeVisible()

  await savedCard.getByRole('link', { name: 'View details', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/works/${target.workId}$`))
  await expect(page.getByRole('heading', { name: target.workTitle, exact: true })).toBeVisible()

  await page.goto('/workspace/assets?view=saved')
  await page.locator('.saved-work-card').filter({ hasText: target.workTitle }).getByRole('button', { name: 'Remove saved work', exact: true }).click()
  await expect(page.getByText('Saved work removed.', { exact: true })).toBeVisible()
  await expect(page.locator('.saved-work-card').filter({ hasText: target.workTitle })).toHaveCount(0)
  await expect(page.getByText('No saved works yet', { exact: true })).toBeVisible()
})
