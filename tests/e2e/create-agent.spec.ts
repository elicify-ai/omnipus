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
  const fillIdentityStep = async (
    page: Page,
    opts: { type: 'Main' | 'Subagent' | 'subagent_3p' },
  ): Promise<string> => {
    const name = `E2E Test Agent ${Date.now()}`
    await page.getByTestId('wizard-name').fill(name)
    if (opts.type === 'subagent_3p') {
      // External agents use a free-text model slug.
      await page.getByTestId('wizard-model').fill('claude-sonnet-4-6')
    } else {
      await selectFirstModel(page)
    }
    if (opts.type !== 'Main') {
      await page.getByTestId('wizard-description').fill('A test worker created by the E2E suite')
    }
    return name
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

    // Voice field (Main only). VoiceProviderSub auto-detects the configured
    // TTS provider on mount: enum mode renders a Radix Select (options appear
    // after clicking the trigger), free-text mode renders a plain Input.
    // In a fresh E2E environment no provider is configured → free-text.
    const voiceContainer = modal.getByTestId('wizard-voice')
    await expect(voiceContainer).toBeVisible()
    const voiceField = voiceContainer.getByTestId('voice-field')
    // Detection runs async on mount (field starts disabled); wait for it to
    // settle before interacting.
    await expect(voiceField).toBeEnabled({ timeout: 10_000 })
    await voiceField.click()
    // Enum mode → a dropdown of [role="option"] opens. Free-text mode → no
    // options; type a value instead.
    const voiceOptions = page.locator('[role="option"]')
    if ((await voiceOptions.count()) > 0) {
      await voiceOptions.first().click()
    } else {
      await voiceField.fill('alloy')
    }

    await modal.getByTestId('wizard-next-2').click()

    await expect(modal.getByTestId('wizard-create')).toBeVisible()
  })

  test('Main agent wizard creates an agent end-to-end', async ({ page }) => {
    await page.goto('/#/agents')
    await page.getByTestId('add-main-button').click()

    const modal = page.locator('[role="dialog"]')
    await expect(modal).toBeVisible()
    await expect(page.getByTestId('wizard-name')).toBeVisible()

    const agentName = await fillIdentityStep(page, { type: 'Main' })
    await modal.getByTestId('wizard-next-1').click()

    await expect(page.getByTestId('wizard-soul')).toBeVisible()
    await page.getByTestId('wizard-soul').fill('You are a helpful E2E test agent.')

    // Voice field (Main only). VoiceProviderSub auto-detects the configured
    // TTS provider on mount: enum mode renders a Radix Select (options appear
    // after clicking the trigger), free-text mode renders a plain Input.
    // In a fresh E2E environment no provider is configured → free-text.
    const voiceContainer = modal.getByTestId('wizard-voice')
    await expect(voiceContainer).toBeVisible()
    const voiceField = voiceContainer.getByTestId('voice-field')
    // Detection runs async on mount (field starts disabled); wait for it to
    // settle before interacting.
    await expect(voiceField).toBeEnabled({ timeout: 10_000 })
    await voiceField.click()
    // Enum mode → a dropdown of [role="option"] opens. Free-text mode → no
    // options; type a value instead.
    const voiceOptions = page.locator('[role="option"]')
    if ((await voiceOptions.count()) > 0) {
      await voiceOptions.first().click()
    } else {
      await voiceField.fill('alloy')
    }

    await modal.getByTestId('wizard-next-2').click()

    // ── Step 3: Tools — click "Create agent" and verify the POST. ──────────
    await expect(modal.getByTestId('wizard-create')).toBeVisible()

    // waitForResponse must be registered before the click that triggers it.
    const createResponsePromise = page.waitForResponse(
      (resp) =>
        resp.url().endsWith('/api/v1/agents') &&
        resp.request().method() === 'POST',
    )
    await modal.getByTestId('wizard-create').click()

    const createResponse = await createResponsePromise
    expect(createResponse.status()).toBe(201)

    // The created agent's id rides on the 201 body — use it to scope the
    // roster card assertion rather than matching on the (timestamped) name.
    const created = (await createResponse.json()) as { id: string; name: string }

    // The modal (a Radix Dialog surfaced as [role="dialog"]) closes on
    // success via CreateAgentModal's useMutation.onSuccess → handleClose.
    // There is no `create-agent-modal` testid; the dialog role is the
    // stable handle used throughout this suite.
    await expect(modal).not.toBeVisible({ timeout: 10_000 })

    // The new agent card must appear in the Main roster section.
    const baseSection = page.getByTestId('base-agents-section')
    const newCard = baseSection.getByTestId(`agent-card-${created.id}`)
    await expect(newCard).toBeVisible({ timeout: 10_000 })
    // Sanity: the card carries the name we typed in step 1.
    await expect(newCard).toContainText(agentName)
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
