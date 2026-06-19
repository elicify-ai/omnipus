import { test, expect } from '@playwright/test'
import type { Page } from '@playwright/test'

/**
 * Walk the create-agent wizard for each agent tier.
 */

async function selectFirstModel(page: Page) {
  // Main/Subagent use the searchable ModelSelector combobox.
  const modelTrigger = page.getByTestId('wizard-model')
  await expect(modelTrigger).toBeVisible()
  await modelTrigger.click()
  const firstOption = page.locator('[role="option"]').first()
  await expect(firstOption).toBeVisible({ timeout: 5_000 })
  await firstOption.click()
}

test.describe('create agent wizard', () => {
  const fillIdentityStep = async (page: Page, opts: { type: 'Main' | 'Subagent' | 'subagent_3p' }) => {
    await page.getByTestId('wizard-name').fill(`E2E Test Agent ${Date.now()}`)
    if (opts.type === 'subagent_3p') {
      // External agents use a free-text model slug.
      await page.getByTestId('wizard-model').fill('claude-sonnet-4-6')
    } else {
      await selectFirstModel(page)
    }
    if (opts.type !== 'Main') {
      await page.getByTestId('wizard-description').fill('A test worker created by the E2E suite')
    }
  }

  test('Main agent wizard reaches creation state', async ({ page }) => {
    await page.goto('/#/agents')
    await page.getByTestId('add-main-button').click()

    const modal = page.locator('[role="dialog"]')
    await expect(modal).toBeVisible()
    await expect(page.getByTestId('wizard-name')).toBeVisible()

    await fillIdentityStep(page, { type: 'Main' })
    await modal.getByTestId('wizard-next-1').click()

    await expect(page.getByTestId('wizard-soul')).toBeVisible()
    await page.getByTestId('wizard-soul').fill('You are a helpful E2E test agent.')
    await modal.getByTestId('wizard-next-2').click()

    await expect(modal.getByTestId('wizard-create')).toBeVisible()
  })

  test('Subagent wizard reaches creation state', async ({ page }) => {
    await page.goto('/#/agents')
    await page.getByTestId('add-subagent-button').click()

    const modal = page.locator('[role="dialog"]')
    await expect(modal).toBeVisible()
    await expect(page.getByTestId('wizard-name')).toBeVisible()

    await fillIdentityStep(page, { type: 'Subagent' })
    await modal.getByTestId('wizard-next-1').click()

    await expect(page.getByTestId('wizard-soul')).toBeVisible()
    await page.getByTestId('wizard-soul').fill('You are a helpful E2E subagent.')
    await modal.getByTestId('wizard-next-2').click()

    await expect(modal.getByTestId('wizard-create')).toBeVisible()
  })

  test('subagent_3p wizard requires CLI selection and reaches creation state', async ({ page }) => {
    // The external CLI popover disables choices when the host scan says the
    // binary is missing. Intercept the scan so we can exercise the claude-code
    // path regardless of what is installed on the CI runner.
    await page.route('**/api/v1/system/cli-detect', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          hasClaude: true,
          hasCodex: true,
          hasOpencode: true,
        }),
      })
    })

    await page.goto('/#/agents')
    await page.getByTestId('add-external-trigger').click()
    await page.getByTestId('add-external-claude-code').click()

    const modal = page.locator('[role="dialog"]')
    await expect(modal).toBeVisible()
    // The roster pre-locks the CLI, so the chooser is hidden and a locked
    // CLI chip is shown instead.
    await expect(page.getByTestId('wizard-cli-chip')).toBeVisible()
    await expect(page.getByTestId('wizard-cli-path')).toBeVisible()

    await fillIdentityStep(page, { type: 'subagent_3p' })
    await page.getByTestId('wizard-cli-path').fill('/usr/local/bin/claude')
    await modal.getByTestId('wizard-next-1').click()

    await expect(page.getByTestId('wizard-soul')).toBeVisible()
    await page.getByTestId('wizard-soul').fill('You are a helpful external CLI subagent.')
    await modal.getByTestId('wizard-next-2').click()

    await expect(page.getByTestId('wizard-cli-args')).toBeVisible()
    await expect(modal.getByTestId('wizard-create')).toBeVisible()
  })
})
