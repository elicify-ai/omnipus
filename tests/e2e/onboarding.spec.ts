/**
 * onboarding.spec.ts — ADR-068 T068-31 / SC-008.
 *
 * The four onboarding rows the spec names, each with its own oracle:
 *
 *   1. "Auth-method control keeps three steps" (FR-028) — the step tracker has
 *      exactly 3 steps, and step 3 still carries the auth-method control INSIDE
 *      its second-level panel. A fourth numbered step would fail row 1.
 *   2. "Onboarding model field is empty" (FR-029, FR-040) — step 3's model
 *      control starts with NO value; its accessible name is the caller's label
 *      verbatim, which is only true while nothing is selected.
 *   3. "Finish is disabled until a model is chosen" (SC-008) — disabled with no
 *      model, still disabled while the probe is in flight / for a stale probe,
 *      enabled only once the probe passed FOR THAT model.
 *   4. "completion carries auth_method" (FR-035) — the POST body is inspected on
 *      the wire, so a SPA that dropped the discriminator fails here.
 *
 * Plus the free-string probe id row (FR-023/FR-036): a Custom endpoint id that
 * is NOT in the catalog reaches the probe verbatim.
 *
 * Harness: `fixtures/onboarding-stubs.ts` — read its header for why the wizard
 * is driven against stubbed edges rather than a second real onboarding of the
 * ONE shared gateway this suite runs against.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)": the page
 * under test is the embedded SPA served by the gateway; only four JSON edges
 * are replaced.
 */

import { expect, test } from '@playwright/test'
import { catalogRow, stubOnboarding } from './fixtures/onboarding-stubs'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

/** FR-029, verbatim — mirrors ONBOARDING_MODEL_LABEL in src/routes/onboarding.tsx. */
const MODEL_LABEL = 'Model for your first agent'

/** An api-key company that exists in the fixture with exactly one variant. */
const OPENAI = catalogRow('openai')
const OPENAI_MODEL = OPENAI.models?.[0]?.id as string

/** Walk steps 1 and 2 so the test body starts on step 3. */
async function reachStepThree(page: import('@playwright/test').Page): Promise<void> {
  await page.goto(`${BASE_URL}/#/onboarding`)
  await expect(page.getByText('Step 1 of 3').first()).toBeVisible({ timeout: 15_000 })

  await page.locator('#admin-username').fill('e2e-admin')
  await page.getByRole('button', { name: /^continue$/i }).click()

  await expect(page.getByText('Step 2 of 3').first()).toBeVisible()
  await page.locator('#admin-password').fill('e2e-passw0rd!')
  await page.locator('#admin-password-confirm').fill('e2e-passw0rd!')
  await page.getByRole('button', { name: /^continue$/i }).click()

  await expect(page.getByText('Step 3 of 3').first()).toBeVisible()
}

/** Pick the OpenAI Popular tile, type a key, confirm the second-level panel. */
async function confirmOpenAIWithKey(page: import('@playwright/test').Page): Promise<void> {
  await page.getByTestId(`picker-popular-${OPENAI.id}`).click()
  const panel = page.getByTestId('provider-detail-panel')
  await expect(panel).toBeVisible()
  await panel.getByTestId('provider-detail-panel-api-key-input').fill('sk-e2e-not-a-real-key')
  await panel.getByTestId('provider-detail-panel-continue').click()
  await expect(page.getByTestId('onboarding-provider-summary')).toBeVisible()
}

// ── Row 1: three steps, auth method inside step 3 (FR-028 / SC-008) ───────────

test('onboarding step tracker has exactly 3 steps and the auth-method control lives inside step 3', async ({
  page,
}) => {
  await stubOnboarding(page)
  await reachStepThree(page)

  // The progress semantics carry the count, not the dots' styling.
  const progress = page.getByRole('progressbar', { name: /onboarding progress/i })
  await expect(progress).toHaveAttribute('aria-valuemax', '3')
  await expect(progress).toHaveAttribute('aria-valuemin', '1')
  await expect(progress).toHaveAttribute('aria-valuenow', '3')

  // Differentiation: a 4-step tracker would render "Step 3 of 4".
  await expect(page.getByText('Step 3 of 4')).toHaveCount(0)

  // FR-028: the auth-method control is INSIDE step 3's second-level panel — it
  // is what a fourth numbered step would otherwise have carried.
  await page.getByTestId(`picker-popular-${OPENAI.id}`).click()
  await expect(page.getByTestId('provider-detail-panel-auth')).toBeVisible()
  await expect(progress).toHaveAttribute('aria-valuenow', '3')
})

// ── Row 2: the model field starts empty (FR-029) ──────────────────────────────

test('onboarding step 3 pre-selects no model — the field is empty and labelled verbatim', async ({
  page,
}) => {
  await stubOnboarding(page)
  await reachStepThree(page)
  await confirmOpenAIWithKey(page)

  const modelTrigger = page.getByTestId('onboarding-model-select')
  await expect(modelTrigger).toBeVisible()

  // With no value the accessible name is the caller's label and NOTHING else
  // (model-selector.tsx appends ", currently <model>" the moment a value
  // exists). An exact match is therefore a true "nothing is selected" oracle.
  await expect(modelTrigger).toHaveAccessibleName(MODEL_LABEL)

  // And the fixture's first model — the one a "helpful" pre-selection would
  // have picked — is not showing on the trigger.
  await expect(modelTrigger).not.toContainText(OPENAI_MODEL)
})

// ── Row 3: Finish gated on a passed probe for the chosen model (SC-008) ───────

test('Finish stays disabled until a model is chosen and its probe passes', async ({ page }) => {
  const wire = await stubOnboarding(page)
  await reachStepThree(page)

  const finish = page.getByRole('button', { name: /finish/i })

  // (a) No provider confirmed yet — Finish is not even reachable as enabled.
  await expect(finish).toBeDisabled()

  await confirmOpenAIWithKey(page)

  // (b) Provider confirmed, no model — still disabled.
  await expect(finish).toBeDisabled()
  expect(wire.probes, 'no probe is sent before a model is chosen').toHaveLength(0)

  // (c) Choose a model → auto-probe → Finish enables.
  await page.getByTestId('onboarding-model-select').click()
  await page.getByTestId(`onboarding-model-${OPENAI_MODEL}`).click()
  await expect(finish).toBeEnabled({ timeout: 15_000 })

  // The probe that enabled it exercised THAT model with the api_key method.
  expect(wire.probes.length).toBeGreaterThanOrEqual(1)
  const probe = wire.probes[wire.probes.length - 1]
  expect(probe.id).toBe(OPENAI.id)
  expect(probe.auth).toBe('api_key')
  expect(probe.model).toBe(OPENAI_MODEL)
})

// ── Row 4: completion carries auth_method (FR-035 / SC-008) ───────────────────

test('onboarding completion sends auth_method with the provider', async ({ page }) => {
  const wire = await stubOnboarding(page)
  await reachStepThree(page)
  await confirmOpenAIWithKey(page)

  await page.getByTestId('onboarding-model-select').click()
  await page.getByTestId(`onboarding-model-${OPENAI_MODEL}`).click()

  const finish = page.getByRole('button', { name: /finish/i })
  await expect(finish).toBeEnabled({ timeout: 15_000 })
  await finish.click()

  await expect
    .poll(() => wire.completions.length, { timeout: 15_000 })
    .toBeGreaterThanOrEqual(1)

  const body = wire.completions[wire.completions.length - 1]
  const provider = body.provider as Record<string, unknown>
  expect(provider, 'completion body carries a provider object').toBeTruthy()
  // FR-035: the discriminator is present and names the method actually used.
  expect(provider.auth_method).toBe('api_key')
  expect(provider.id).toBe(OPENAI.id)
  expect(provider.model).toBe(OPENAI_MODEL)
  // The api_key variant carries the key; the sign_in variant has no such field
  // at all (sending one is a 400), which is why the two are built separately.
  expect(typeof provider.api_key).toBe('string')
})

// ── Free-string probe id (FR-023 / FR-036) ────────────────────────────────────

test('a Custom endpoint id that is not in the catalog reaches the probe verbatim', async ({
  page,
}) => {
  const wire = await stubOnboarding(page)
  await reachStepThree(page)

  // The Custom endpoint row is the picker's last row and settles on its own.
  await page.getByTestId('picker-custom-endpoint').click()
  const custom = page.getByTestId('custom-endpoint-panel')
  await expect(custom).toBeVisible()

  const FREE_ID = 'my-own-gateway'
  await custom.getByTestId('custom-endpoint-id').fill(FREE_ID)
  await custom.getByTestId('custom-endpoint-api-base').fill('https://llm.example.internal/v1')
  await custom.getByTestId('custom-endpoint-api-key').fill('sk-e2e-not-a-real-key')
  await custom.getByTestId('custom-endpoint-submit').click()

  await expect(page.getByTestId('onboarding-provider-summary')).toContainText(FREE_ID)

  // No catalog listing → free-text slug, and the explicit check button is the
  // probe trigger (a probe per keystroke would be a request storm).
  await page.getByTestId('onboarding-model-select').fill('some-model-v1')
  await page.getByTestId('onboarding-probe-button').click()

  await expect.poll(() => wire.probes.length, { timeout: 15_000 }).toBeGreaterThanOrEqual(1)
  const probe = wire.probes[wire.probes.length - 1]
  // The operator's free string is sent EXACTLY as typed — no normalisation, no
  // catalog substitution, no silent fallback to a known id.
  expect(probe.id).toBe(FREE_ID)
  expect(probe.model).toBe('some-model-v1')
})
