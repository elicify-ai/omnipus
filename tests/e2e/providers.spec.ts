/**
 * providers.spec.ts — E2E-2: unconfigured provider shows not-connected (not green).
 *
 * Covers:
 *   AC1 (US-2): Given a provider listed with no resolvable API key, When the
 *     Providers tab renders, Then it shows a not-connected state — NOT the green
 *     "Connected" badge ([data-testid="connected-badge"]) — and shows
 *     "Not configured" instead.
 *
 * Approach: deterministic, no real API key required.
 *   - page.route() stubs GET /api/v1/providers to return a provider with
 *     status:'disconnected' (simulating a default/fresh install with no key).
 *   - The SPA's ProvidersSection renders "Not configured" (muted Badge, no
 *     testid) and a "Configure" button for any provider whose status !== 'connected'.
 *   - Asserts absence of [data-testid="connected-badge"] and presence of the
 *     "Not configured" text.
 *
 * Why we use the base @playwright/test rather than the console-errors fixture:
 *   The stub intercepts the providers API before any real request fires.
 *   No WS mocking is needed. Using the base test avoids a dependency on the
 *   console-errors fixture's allowlist for any benign warnings that may surface
 *   when the real gateway is unavailable for the stubbed route.
 *
 * Traces to: E2E-2 (SC-103) — provider with no key renders not-connected.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import type { Route } from '@playwright/test'
import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── Stub data ─────────────────────────────────────────────────────────────────

/**
 * A minimal provider entry with status:'disconnected' (no API key configured).
 * This is what the backend returns when no credential resolves for the provider.
 */
const UNCONFIGURED_PROVIDER = {
  id: 'openai',
  name: 'openai',
  display_name: 'OpenAI',
  status: 'disconnected',
  models: [],
}

/**
 * A second provider, also unconfigured, so the list has more than one entry
 * and the rendering of the section is not gated on a single-item edge case.
 */
const SECOND_UNCONFIGURED_PROVIDER = {
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  status: 'disconnected',
  models: [],
}

// ── REST stub helper ──────────────────────────────────────────────────────────

/**
 * Register a page.route() stub for GET /api/v1/providers that returns two
 * providers both with status:'disconnected'. All other methods are passed
 * through to the real gateway (PUT/POST for save/test, if triggered).
 */
async function stubProvidersDisconnected(
  page: import('@playwright/test').Page,
): Promise<void> {
  await page.route('**/api/v1/providers', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([UNCONFIGURED_PROVIDER, SECOND_UNCONFIGURED_PROVIDER]),
      })
    } else {
      await route.continue()
    }
  })
}

// ── E2E-2: unconfigured provider not green ────────────────────────────────────
// BDD: Given a default install (no API keys configured)
//      When the Providers tab in Settings renders
//      Then no [data-testid="connected-badge"] is present in the DOM
//      And "Not configured" text is visible for at least one provider
//      And a "Configure" button (not "Edit") is shown
//
// Traces to: E2E-2 / SC-103

test(
  'unconfigured provider shows "Not configured" and no green Connected badge',
  async ({ page }) => {
    // Stub the providers API BEFORE navigation so the response is in place
    // when the SPA mounts and fires the React Query fetch.
    await stubProvidersDisconnected(page)

    // Navigate to the Settings page. The Providers tab is the defaultValue
    // (SettingsScreen.tsx:43 — defaultValue="providers"), so no tab click needed.
    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Wait for the Providers section to finish loading (skeleton disappears,
    // provider rows appear). The "Not configured" badge text is the first
    // reliable signal that the section has fully rendered.
    const notConfiguredBadge = page.getByText('Not configured').first()
    await expect(notConfiguredBadge).toBeVisible({ timeout: 15_000 })

    // ── Core assertion A: no "Connected" (green) badge exists anywhere.
    // [data-testid="connected-badge"] is rendered ONLY when provider.status
    // === 'connected' (ProvidersSection.tsx:98). It must be absent.
    await expect(page.getByTestId('connected-badge')).toHaveCount(0)

    // ── Core assertion B: at least one "Not configured" badge is visible.
    // This is the muted Badge rendered for status !== 'connected' && !== 'error'.
    await expect(notConfiguredBadge).toBeVisible()

    // ── Proxy assertion: the action button reads "Configure", not "Edit".
    // Connected providers show "Edit"; unconfigured ones show a "Configure"
    // button with a Plus icon (ProvidersSection.tsx:141-143). This verifies the
    // full branching logic, not just the badge.
    const configureButton = page.getByRole('button', { name: /configure/i }).first()
    await expect(configureButton).toBeVisible({ timeout: 5_000 })

    // ── Negative assertion: no "Edit" button should appear (would indicate a
    // provider is incorrectly shown as connected).
    const editButton = page.getByRole('button', { name: /^edit$/i }).first()
    await expect(editButton).toHaveCount(0)
  },
)

test(
  'no connected badge rendered when all providers are unconfigured (count check)',
  async ({ page }) => {
    // Stub before navigation — same pattern as the primary test.
    await stubProvidersDisconnected(page)

    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Wait for the providers list to render (any provider name visible).
    const openaiRow = page.getByText('OpenAI').first()
    await expect(openaiRow).toBeVisible({ timeout: 15_000 })

    // Both providers are stubbed as disconnected — verify exactly zero
    // [data-testid="connected-badge"] elements exist in the entire page.
    await expect(page.getByTestId('connected-badge')).toHaveCount(0)

    // Both should show "Not configured" — the stub returned two providers.
    const allNotConfigured = page.getByText('Not configured')
    await expect(allNotConfigured).toHaveCount(2)
  },
)
