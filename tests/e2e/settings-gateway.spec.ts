/**
 * settings-gateway.spec.ts — E2E-4: Gateway tab has no auth_mode:none option
 * and no hot-reload toggle in the DOM.
 *
 * Covers:
 *   AC1 (US-4): The Gateway tab does not offer auth_mode:none; token auth is
 *     the only UI option. (The backend config-file capability is retained, but
 *     the UI control for selecting "no login" is removed — FR-107.)
 *   AC2 (US-4): The hot-reload toggle is removed from the Gateway tab (always
 *     on by default; no user-facing switch is rendered).
 *
 * Approach: deterministic, no real API key or LLM required.
 *   - page.route() stubs GET /api/v1/config/pending-restart to return [] so the
 *     restart banner does not interfere with the assertion surface. This matches
 *     the isolation pattern used by fresh-install.spec.ts.
 *   - The SPA's GatewaySection is inspected after the Gateway tab is clicked.
 *   - All negative assertions use .not.toBeAttached() — stronger than
 *     .not.toBeVisible() because removed elements are never inserted into the
 *     DOM at all (not merely hidden).
 *
 * Why we use the base @playwright/test rather than the console-errors fixture:
 *   The stub intercepts the pending-restart API before any real request fires.
 *   No WS mocking is needed. Using the base test avoids a dependency on the
 *   console-errors fixture's allowlist for any benign warnings that may surface
 *   when real gateway routes are partially stubbed.
 *
 * Traces to: E2E-4 (SC-106) — no auth_mode:none control and no hot-reload
 *   toggle in the DOM.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import type { Route } from '@playwright/test'
import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── REST stub helper ──────────────────────────────────────────────────────────

/**
 * Register a page.route() stub for GET /api/v1/config/pending-restart that
 * returns an empty array. The RestartBanner component returns null when
 * entries.length === 0, so [data-testid="restart-banner"] never attaches.
 * This prevents the banner from obscuring or interacting with the assertion
 * surface in GatewaySection.
 */
async function stubPendingRestartEmpty(
  page: import('@playwright/test').Page,
): Promise<void> {
  await page.route('**/api/v1/config/pending-restart', async (route: Route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    } else {
      await route.continue()
    }
  })
}

// ── E2E-4: no auth_mode:none option ──────────────────────────────────────────
// BDD: Given the operator is on the Settings → Gateway tab
//      When the GatewaySection renders
//      Then no control for auth_mode:none exists in the DOM
//      And no "no login" text appears in the DOM
//      And no "Login requirement" label appears (the entire auth-mode group is gone)
//
// Traces to: E2E-4 / SC-106 / US-4 AC1

test(
  'Gateway tab: no auth_mode:none option in the DOM (FR-107)',
  async ({ page }) => {
    // Stub the pending-restart endpoint BEFORE navigation so the response is
    // in place when the SPA mounts and fires the React Query fetch for the banner.
    await stubPendingRestartEmpty(page)

    // Navigate to the Settings screen via the HashRouter path.
    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Switch to the Gateway tab. GatewaySection is the relevant panel.
    const gatewayTab = page.locator('button[role="tab"]', { hasText: 'Gateway' })
    await expect(gatewayTab).toBeVisible({ timeout: 15_000 })
    await gatewayTab.click()

    // Wait for the Gateway tab panel to become active and fully rendered.
    // Log Level is always present in GatewaySection — use it as the readiness signal.
    const activePanel = page.locator('[role="tabpanel"][data-state="active"]').first()
    await expect(activePanel).toBeVisible({ timeout: 10_000 })
    await expect(
      activePanel.getByText(/log level/i).or(activePanel.getByText(/port/i)).first(),
    ).toBeVisible({ timeout: 10_000 })

    // ── Core assertion A: the risky-option-none button (data-testid stamped by
    // RiskySettingControl as `risky-option-${opt.value}`) must NOT be in the DOM.
    // If auth_mode:none were still offered, this testid would be present.
    await expect(page.getByTestId('risky-option-none')).not.toBeAttached()

    // ── Core assertion B: the human-readable "no login" label must not appear.
    // GatewaySection previously rendered this as option text for the none value.
    await expect(page.getByText(/none.*no login/i)).not.toBeAttached()

    // ── Secondary assertion: the "Login requirement" group label must not appear.
    // If the entire auth-mode selector group was removed, no label renders either.
    await expect(page.getByText(/login requirement/i)).not.toBeAttached()

    // ── Tertiary assertion: no "Bearer token.*login required" description text.
    // This would only appear if the token option were still rendered as part of
    // a multi-option auth-mode selector; since the group is gone entirely, it
    // must not appear.
    await expect(page.getByText(/bearer token.*login required/i)).not.toBeAttached()
  },
)

// ── E2E-4: no hot-reload toggle ───────────────────────────────────────────────
// BDD: Given the operator is on the Settings → Gateway tab
//      When the GatewaySection renders
//      Then no element with role="switch" exists in the DOM
//      And no "Hot reload" label appears in the DOM
//
// Traces to: E2E-4 / SC-106 / US-4 AC2

test(
  'Gateway tab: no hot-reload toggle in the DOM (always on)',
  async ({ page }) => {
    // Stub before navigation — same isolation pattern as the auth_mode test.
    await stubPendingRestartEmpty(page)

    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Switch to the Gateway tab.
    const gatewayTab = page.locator('button[role="tab"]', { hasText: 'Gateway' })
    await expect(gatewayTab).toBeVisible({ timeout: 15_000 })
    await gatewayTab.click()

    // Wait for the Gateway tab panel to become active and fully rendered.
    const activePanel = page.locator('[role="tabpanel"][data-state="active"]').first()
    await expect(activePanel).toBeVisible({ timeout: 10_000 })
    await expect(
      activePanel.getByText(/log level/i).or(activePanel.getByText(/port/i)).first(),
    ).toBeVisible({ timeout: 10_000 })

    // ── Core assertion A: no hot-reload switch in the active Gateway panel.
    // The hot-reload toggle was a shadcn Switch (role="switch"). The bind-address
    // RiskySettingControl uses buttons, not switches. The ONLY legitimate switch
    // in the Gateway panel is the god-mode toggle (S5/O14 danger zone), so assert
    // no switch OTHER than god-mode exists (the hot-reload toggle is gone).
    await expect(
      activePanel.locator('[role="switch"]:not([data-testid="god-mode-toggle"])'),
    ).not.toBeAttached()

    // ── Core assertion B: no "Hot reload" label text anywhere on the page.
    // The label text matches /hot reload/i regardless of capitalisation.
    await expect(page.getByText(/hot reload/i)).not.toBeAttached()
  },
)

// ── E2E-4: combined — Gateway tab renders meaningful content without removed items ──
// BDD: Given the operator is on the Settings → Gateway tab
//      When the GatewaySection renders
//      Then it contains expected retained content (port, log level)
//      And neither the auth_mode:none control nor the hot-reload toggle is present
//
// This combined test confirms the Gateway section is not blank (guards against a
// regression where the section fails to load and accidentally passes the negative
// assertions above due to an empty/errored panel).
//
// Traces to: E2E-4 / SC-106

test(
  'Gateway tab: retained controls visible, removed controls absent',
  async ({ page }) => {
    // Stub before navigation.
    await stubPendingRestartEmpty(page)

    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    const gatewayTab = page.locator('button[role="tab"]', { hasText: 'Gateway' })
    await expect(gatewayTab).toBeVisible({ timeout: 15_000 })
    await gatewayTab.click()

    const activePanel = page.locator('[role="tabpanel"][data-state="active"]').first()
    await expect(activePanel).toBeVisible({ timeout: 10_000 })

    // ── Positive assertion: the Gateway section has real content.
    // At least one of these labels must be present — confirms the panel rendered
    // actual gateway config UI (not a blank or errored state).
    await expect(
      activePanel.getByText(/log level/i).or(activePanel.getByText(/port/i)).first(),
    ).toBeVisible({ timeout: 10_000 })

    // ── Negative assertion (auth_mode:none): the none option control is absent.
    await expect(page.getByTestId('risky-option-none')).not.toBeAttached()
    await expect(page.getByText(/none.*no login/i)).not.toBeAttached()

    // ── Negative assertion (hot-reload): no switch (other than the legitimate
    // god-mode toggle) and no label.
    await expect(
      activePanel.locator('[role="switch"]:not([data-testid="god-mode-toggle"])'),
    ).not.toBeAttached()
    await expect(page.getByText(/hot reload/i)).not.toBeAttached()
  },
)
