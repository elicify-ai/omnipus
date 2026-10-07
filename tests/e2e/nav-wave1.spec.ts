import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'

// FR-026 / FR-020. The browser surfaces that do not need a model turn.
// The inline indicator's path matrix is the vitest file
// src/components/chat/ChatScreen.wave1-indicator.test.tsx — a live turn is
// not started here.

test('Sessions view is titled Sessions', async ({ page }) => {
  await page.goto('/#/')
  await page.getByRole('button', { name: 'Search sessions' }).click()
  await expect(page.getByRole('heading', { name: 'Sessions', exact: true })).toBeVisible({ timeout: 15_000 })
})

test('a built-in agent shows locked figure, role and colour and no upload', async ({ page }) => {
  await page.goto('/#/agents')
  await page.getByTestId('agent-card-mia').click()
  const figure = page.getByRole('button', { name: 'Omnipus' })
  await expect(figure).toBeVisible({ timeout: 15_000 })
  await expect(figure).toBeDisabled()
  await expect(page.getByRole('button', { name: 'General assistant' })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Azure' })).toBeDisabled()
  await expect(page.locator('input[type="file"]')).toHaveCount(0)
})
