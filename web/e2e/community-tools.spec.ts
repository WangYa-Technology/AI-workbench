import { expect, test } from '@playwright/test'

test('community auxiliary actions are visible and clickable outside the toolbar', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })).ok()).toBeTruthy()
  for (const width of [1308, 1551, 390]) {
    await page.setViewportSize({ width, height: 901 })
    await page.goto('/community')
    const trigger = page.locator('.community-tools > button')
    const panel = page.locator('.community-tools-panel')
    await trigger.click()
    // Visibility alone does not detect an ancestor clipping the menu.
    await panel.getByRole('button', { name: 'My reports & appeals' }).click()
    await expect(page.locator('.community-cases')).toBeVisible()
    await expect(panel).toHaveCount(0)
    await trigger.click()
    await page.keyboard.press('Escape')
    await expect(panel).toHaveCount(0)
    await expect(trigger).toBeFocused()
    await trigger.click()
    await panel.getByRole('link', { name: 'Browse works' }).click()
    await expect(page).toHaveURL(/\/discover$/)
  }
})
