import { test, expect } from '@playwright/test'
import type { Page } from '@playwright/test'

/**
 * Walk the create-agent wizard for each agent tier.
 *
 * These specs operate at the page-object level using selectors from
 * tests/e2e/fixtures/selectors.ts where applicable, and local data-testid
 * selectors for the wizard-specific controls.
 */

test.describe('create agent wizard', () => {
  const fillIdentityStep = async (page: Page, opts: { type: 'Main' | 'Subagent' | 'subagent_3p' }) => {
    await page.getByTestId('wizard-name').fill('E2E Test Agent')
    await page.getByTestId('wizard-model').fill('claude-sonnet-4-6')
    if (opts.type !== 'Main') {
      await page.getByTestId('wizard-description').fill('A test worker created by the E2E suite')
    }
  }

  test('Main agent wizard reaches creation state', async ({ page }) => {
    await page.goto('/#/agents')
    await page.getByTestId('add-main-button').click()

    await expect(page.getByTestId('create-agent-modal')).toBeVisible()
    await expect(page.getByTestId('wizard-name')).toBeVisible()
    await expect(page.getByTestId('wizard-voice')).toBeVisible()

    await fillIdentityStep(page, { type: 'Main' })
    await page.getByRole('button', { name: /Continue/i }).click()

    await expect(page.getByTestId('wizard-soul')).toBeVisible()
    await page.getByTestId('wizard-soul').fill('You are a helpful E2E test agent.')
    await page.getByRole('button', { name: /Continue/i }).click()

    await expect(page.getByRole('button', { name: /Create agent/i })).toBeVisible()
  })

  test('Subagent wizard reaches creation state', async ({ page }) => {
    await page.goto('/#/agents')
    await page.getByTestId('add-subagent-button').click()

    await expect(page.getByTestId('create-agent-modal')).toBeVisible()
    await expect(page.getByTestId('wizard-name')).toBeVisible()

    await fillIdentityStep(page, { type: 'Subagent' })
    await page.getByRole('button', { name: /Continue/i }).click()

    await expect(page.getByTestId('wizard-soul')).toBeVisible()
    await page.getByTestId('wizard-soul').fill('You are a helpful E2E subagent.')
    await page.getByRole('button', { name: /Continue/i }).click()

    await expect(page.getByRole('button', { name: /Create agent/i })).toBeVisible()
  })

  test('subagent_3p wizard requires CLI selection and reaches creation state', async ({ page }) => {
    await page.goto('/#/agents')
    await page.getByTestId('add-external-trigger').click()
    await page.getByTestId('add-external-claude-code').click()

    await expect(page.getByTestId('create-agent-modal')).toBeVisible()
    await expect(page.getByTestId('wizard-cli-chooser')).toBeVisible()

    await fillIdentityStep(page, { type: 'subagent_3p' })
    await page.getByTestId('wizard-cli-path').fill('/usr/local/bin/claude')
    await page.getByRole('button', { name: /Continue/i }).click()

    await expect(page.getByTestId('wizard-soul')).toBeVisible()
    await page.getByTestId('wizard-soul').fill('You are a helpful external CLI subagent.')
    await page.getByRole('button', { name: /Continue/i }).click()

    await expect(page.getByTestId('wizard-cli-args')).toBeVisible()
    await page.getByRole('button', { name: /Create agent/i }).toBeVisible()
  })
})
