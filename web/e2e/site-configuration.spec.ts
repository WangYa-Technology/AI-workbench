import { expect, test } from '@playwright/test'

test('saves branding, footer, and policy content from Settings', async ({ page }) => {
  const runID = Date.now().toString(36)
  const originalResponse = await page.request.get('/api/v1/site-config')
  expect(originalResponse.ok()).toBeTruthy()
  const original = await originalResponse.json()

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  try {
    await page.goto('/admin?tab=settings')
		const workspace = page.locator('.site-configuration-workspace')
		await expect(workspace.getByRole('heading', { name: 'Brand, policies, and footer' })).toBeVisible()
		await expect(workspace.locator('.site-configuration-heading > span')).toHaveCount(0)

    const siteName = `HCAI Config ${runID}`
		const footer = `Configured footer ${runID}`
		const terms = `Configured Terms of Service content ${runID}`
		const icon = `/brand/logo.png?config=${runID}`
    await workspace.getByLabel('Site name').fill(siteName)
    await workspace.getByLabel('Image URL', { exact: true }).fill(icon)
    await workspace.getByLabel('Footer text · English').fill(footer)
    await workspace.getByRole('tab', { name: 'Policies' }).click()
    await workspace.getByRole('button', { name: /Terms of Service/ }).click()
    await workspace.getByLabel('Policy content · English').fill(terms)
    await workspace.getByRole('button', { name: 'Save site configuration' }).click()
		await expect(page.getByText('Site configuration saved.', { exact: true })).toBeVisible()

    const publishedResponse = await page.request.get('/api/v1/site-config')
    expect(publishedResponse.ok()).toBeTruthy()
    expect(await publishedResponse.json()).toMatchObject({
      siteName,
      siteIconUrl: icon,
      footerText: { enUS: footer },
      policies: { terms: { enUS: terms } },
    })

    await page.goto('/policies/terms')
    await expect(page.getByText(terms, { exact: true })).toBeVisible()
    await expect(page.locator('.site-sidebar .brand')).toHaveAttribute('aria-label', siteName)
    await expect(page.locator('.site-sidebar .brand-logo')).toHaveAttribute('src', icon)
    await expect(page.locator('.site-footer').getByText(footer, { exact: true })).toBeVisible()
    await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', icon)
    await expect(page).toHaveTitle(siteName)
  } finally {
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
		await page.request.put('/api/v1/admin/site-config', { data: original })
  }
})

test('previews Markdown and renders sanitized policy and footer copy', async ({ page }) => {
  const originalResponse = await page.request.get('/api/v1/site-config')
  expect(originalResponse.ok()).toBeTruthy()
  const original = await originalResponse.json()
  const markdown = '# Live policy\n\n**Bold policy** with [a link](https://example.com).\n\n<script>alert(1)</script>'

  await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  try {
    await page.goto('/admin?tab=settings')
    const workspace = page.locator('.site-configuration-workspace')
    await workspace.getByRole('tab', { name: 'Policies' }).click()
    await workspace.getByRole('button', { name: /Terms of Service/ }).click()
    const markdownEditor = workspace.locator('.site-policy-editor .markdown-editor').first()
    const editor = markdownEditor.getByRole('textbox', { name: 'Policy content · English' })
    await editor.fill('First line')
    await editor.press('Enter')
    await editor.type('Second line')
    await expect(editor).toHaveValue('First line\nSecond line')

    await editor.fill('# Live policy\n\nBold policy with [a link](https://example.com).\n\n<script>alert(1)</script>')
    await editor.evaluate((element: HTMLTextAreaElement) => {
      const start = element.value.indexOf('Bold policy')
      element.setSelectionRange(start, start + 'Bold policy'.length)
    })
    await markdownEditor.getByRole('button', { name: 'Bold' }).click()
    await expect(editor).toHaveValue(markdown)

    await markdownEditor.getByRole('tab', { name: 'Preview' }).click()
    const preview = markdownEditor.locator('.markdown-editor__preview')
    await expect(preview.getByRole('heading', { name: 'Live policy', exact: true })).toBeVisible()
    await expect(preview.locator('strong')).toHaveText('Bold policy')
    await expect(preview.locator('script')).toHaveCount(0)

    await markdownEditor.getByRole('tab', { name: 'Write' }).click()
    await expect(markdownEditor.getByRole('textbox', { name: 'Policy content · English' })).toHaveValue(markdown)

    const configured = {
      ...original,
      footerText: { ...original.footerText, enUS: '**Footer** _Markdown_' },
      policies: { ...original.policies, terms: { ...original.policies.terms, enUS: markdown } },
    }
    const update = await page.request.put('/api/v1/admin/site-config', { data: configured })
    expect(update.ok()).toBeTruthy()

    await page.goto('/policies/terms')
    await expect(page.locator('.legal-policy-content').getByRole('heading', { name: 'Live policy', exact: true })).toBeVisible()
    await expect(page.locator('.legal-policy-content script')).toHaveCount(0)
    await page.goto('/discover')
    await expect(page.locator('.site-footer-content strong')).toHaveText('Footer')
    await expect(page.locator('.site-footer-content em')).toHaveText('Markdown')
  } finally {
    await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
    await page.request.put('/api/v1/admin/site-config', { data: original })
  }
})
