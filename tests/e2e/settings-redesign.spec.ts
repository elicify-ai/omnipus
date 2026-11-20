/**
 * settings-redesign.spec.ts — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 29: E2E for the hands-on rows marked
 * "stable" that can run against a real gateway with the declared seams only.
 * Targeted file only (spec, Test Implementation Order row 29).
 *
 * Rows covered here (spec "Hands-on UAT rows"):
 *   - G-01 (US-1 AS1, AS2): 11 sections in 3 groups, no Sign-in; the open
 *     section survives a reload.
 *   - G-02 (US-1 AS3–AS7, spot-check incl. devices): D-3 links land per D-3.
 *   - G-03 (US-1 AS8–AS10): "web search" → moved to Skills & Tools; "zzqx"
 *     → "No settings match".
 *   - G-05 (US-2 AS1–AS5, Port 80 only): the bar counts follow the values;
 *     80 blocks Save with the range text; a valid port saves.
 *   - G-07(a) (US-2 AS7): the DECLARED seam — Playwright route interception
 *     of PUT /settings/context answering 400 (the spec names this seam) —
 *     Tries per goal saves, Default context window keeps its draft with the
 *     seam's message under it, bar "1 change could not be saved".
 *   - G-13 (US-5 AS1–AS4): defaults 90/180/off; 30/365 persist after a
 *     reload; 366 blocked with the range text; Keep forever on switches the
 *     day field off.
 *
 * RED today: the gateway serves the CURRENT SPA (13 flat tabs, no save bar,
 * no search, no Limits section, no retention fields) — every assertion
 * fails for the spec's reason, never with a harness error.
 */

import { expect, test, type Page } from '@playwright/test'

const SETTINGS = '/#/settings'

async function openSettings(page: Page, search = '') {
  await page.goto(`${SETTINGS}${search}`)
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
}

test.describe('G-01 — structure and reload (US-1 AS1, AS2)', () => {
  test('11 sections in three groups, no Sign-in; the open section survives a reload', async ({ page }) => {
    await openSettings(page)
    await expect(page.getByText('AI & agents')).toBeVisible()
    await expect(page.getByText('Security')).toBeVisible()
    await expect(page.getByText('System')).toBeVisible()
    for (const section of [
      'Providers',
      'Memory & recaps',
      'System agents',
      'Third-party agents',
      'Limits',
      'Protection',
      'Sandbox & network',
      'Keys & secrets',
      'Gateway',
      'Data & retention',
      'About',
    ]) {
      await expect(page.getByRole('link', { name: section }),
        `BLOCKED RED (G-01/FR-001): section ${section} must be in the grouped list`).toBeVisible()
    }
    await expect(page.getByRole('link', { name: 'Sign-in' })).toHaveCount(0)

    // AS2: open Sandbox & network, reload — it stays open.
    await page.getByRole('link', { name: 'Sandbox & network' }).click()
    await page.reload()
    await expect(page.getByRole('link', { name: 'Sandbox & network' })).toHaveAttribute(
      'aria-current',
      /page|true/,
    )
  })
})

test.describe('G-02 — old links land in the right place (US-1 AS3–AS7)', () => {
  const cases: Array<[string, string]> = [
    ['?tab=performance', 'Limits'],
    ['?tab=models', 'Limits'],
    ['?tab=security', 'Protection'],
    ['?tab=devices', 'Providers'], // hidden target opens Providers without an error (D-3 row 9)
  ]
  for (const [search, section] of cases) {
    test(`${search} opens ${section}`, async ({ page }) => {
      await openSettings(page, search)
      await expect(
        page.getByRole('heading', { name: section, level: 2 }),
        `BLOCKED RED (G-02/D-3): ${search} must open ${section}`,
      ).toBeVisible()
    })
  }
})

test.describe('G-03 — search (US-1 AS8–AS10)', () => {
  test('"web search" says it moved to Skills & Tools; "zzqx" shows the empty state', async ({ page }) => {
    await openSettings(page)
    const box = page.getByRole('searchbox', { name: /search/i })
    await box.fill('web search')
    await expect(page.getByText(/Skills & Tools/)).toBeVisible()
    await expect(page.getByRole('link', { name: /Skills & Tools/i })).toBeVisible()
    await box.fill('zzqx')
    await expect(page.getByText('No settings match')).toBeVisible()
    await expect(page.getByText(/zzqx/)).toBeVisible()
  })
})

test.describe('G-05 — the save bar on Gateway Port (US-2 AS1–AS5)', () => {
  test('counts follow the values; 80 blocks saving with the range text; a valid port saves', async ({ page }) => {
    await openSettings(page, '?tab=gateway')
    const port = page.getByLabel('Port')
    await port.fill('8081')
    await expect(page.getByText(/1 unsaved change/)).toBeVisible()
    await expect(page.getByText(/1 needs a gateway restart/)).toBeVisible()
    await expect(page.getByRole('button', { name: /save changes/i })).toBeVisible()

    await port.fill('80')
    await expect(page.getByText('Enter a whole number from 1,024 to 65,535.')).toBeVisible()
    await expect(page.getByText(/Fix 1 value before saving/)).toBeVisible()
    await expect(page.getByRole('button', { name: /save changes/i })).toHaveAttribute('aria-disabled', 'true')

    await port.fill('1024')
    await page.getByRole('button', { name: /save changes/i }).click()
    await expect(page.getByText(/Saved at/)).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('G-07(a) — one of two writes fails via the declared seam (US-2 AS7)', () => {
  test('the refused write keeps its draft and message; the other write saves', async ({ page }) => {
    // The DECLARED seam (spec G-07): PUT /settings/context answers 400.
    const SEAM_MESSAGE = 'default_context_window must be ≥ 1 (send null to clear it)'
    await page.route('**/api/v1/settings/context', async (route) => {
      await route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({ error: SEAM_MESSAGE }),
      })
    })
    await openSettings(page, '?tab=limits')
    await page.getByLabel('Default context window').fill('32000')
    await page.getByLabel('Tries per goal').fill('8')
    await page.getByRole('button', { name: /save changes/i }).click()
    // Tries per goal saves (the real PUT /performance reached the gateway).
    await expect(page.getByText(/Saved|saved/i).first()).toBeVisible({ timeout: 10_000 })
    // The refused value keeps its draft with the seam's message under it.
    await expect(page.getByLabel('Default context window')).toHaveValue('32000')
    await expect(page.getByText(SEAM_MESSAGE)).toBeVisible()
    await expect(page.getByText(/1 change could not be saved/)).toBeVisible()
  })
})

test.describe('G-13 — retention values (US-5 AS1–AS4)', () => {
  test('defaults 90/180/off; 30/365 persist; 366 is blocked; Keep forever switches the day field off', async ({ page }) => {
    await openSettings(page, '?tab=data')
    const chat = page.getByLabel('Keep chat history for')
    const memory = page.getByLabel('Keep memory for')
    await expect(chat).toHaveValue('90')
    await expect(memory).toHaveValue('180')

    await chat.fill('30')
    await memory.fill('365')
    await page.getByRole('button', { name: /save changes/i }).click()
    await expect(page.getByText(/Saved at/)).toBeVisible({ timeout: 10_000 })
    await page.reload()
    await expect(page.getByLabel('Keep chat history for')).toHaveValue('30')
    await expect(page.getByLabel('Keep memory for')).toHaveValue('365')

    await page.getByLabel('Keep chat history for').fill('366')
    await expect(page.getByText('Enter a whole number from 1 to 365.')).toBeVisible()
    await expect(page.getByRole('button', { name: /save changes/i })).toHaveAttribute('aria-disabled', 'true')

    await page.getByLabel('Keep chat history for').fill('30')
    await page.getByRole('switch', { name: /keep forever/i }).click()
    await expect(memory).toBeDisabled()
  })
})
