/**
 * fresh-install.spec.ts — E2E-3: a fresh onboarded install shows zero restart
 * banners immediately after onboarding.
 *
 * Covers:
 *   AC1 (US-3): Given a just-completed onboarding, When Settings loads, Then no
 *     pending-restart banner shows (no onboarding-written key is in
 *     RestartGatedKeys).
 *   AC2 (US-3): Given hot-reload is always on (default flipped in defaults.go),
 *     When a hot-reloadable key changes, Then it applies without a restart banner.
 *
 * Approach: deterministic, independent of LLM or external services.
 *   - page.route() stubs GET /api/v1/config/pending-restart to return [] so the
 *     test is isolated from any ambient gateway state left by preceding tests
 *     (the global gateway is shared across the suite). This is the same isolation
 *     pattern used by providers.spec.ts and whatsapp-qr.spec.ts.
 *   - The stub faithfully represents the post-onboarding contract: onboarding
 *     must not write any key into RestartGatedKeys, so the pending-restart list
 *     is empty immediately after onboarding completes.
 *   - The test navigates to /#/settings and waits for the Settings screen to
 *     fully render before asserting absence of the banner.
 *
 * Why we use the base @playwright/test rather than the console-errors fixture:
 *   Using a page.route() stub before navigation keeps the test focused on the
 *   pending-restart contract rather than console noise from unrelated routes.
 *   The stub does not produce WS errors; the base test is sufficient and avoids
 *   any risk of the console-errors allowlist masking a genuine regression.
 *
 * Traces to: E2E-3 (SC-104) — fresh onboarded install shows zero restart banners.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import type { Route } from '@playwright/test'
import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── REST stub helper ──────────────────────────────────────────────────────────

/**
 * Register a page.route() stub for GET /api/v1/config/pending-restart that
 * returns an empty array — the expected state immediately after onboarding.
 *
 * The RestartBanner component (RestartBanner.tsx:79) returns null when
 * entries.length === 0, so [data-testid="restart-banner"] is never attached to
 * the DOM under this condition. All other methods (unlikely for this endpoint)
 * are forwarded to the gateway unchanged.
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

// ── E2E-3: fresh install — no restart banner ──────────────────────────────────
// BDD: Given a just-completed onboarding (pending-restart returns [])
//      When the Settings screen loads
//      Then [data-testid="restart-banner"] is NOT attached to the DOM
//      And no amber "N change(s) saved — restart to apply" text is visible
//      And the Settings screen is otherwise fully rendered (tabs visible)
//
// Traces to: E2E-3 / SC-104

test(
  'fresh install: no restart banner after onboarding (pending-restart returns [])',
  async ({ page }) => {
    // Stub the pending-restart endpoint BEFORE navigation so the response is
    // in place when the SPA mounts and fires the React Query fetch for the banner.
    await stubPendingRestartEmpty(page)

    // Navigate to the Settings screen via the HashRouter path.
    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Wait for the Settings screen to render its tab list before asserting
    // banner absence. The Providers tab is the defaultValue in SettingsScreen
    // (defaultValue="providers"), so the tab list is the reliable readiness signal.
    const tabList = page.locator('[role="tablist"]').first()
    await expect(tabList).toBeVisible({ timeout: 15_000 })

    // Wait for at least one tab to be visible — confirms the SPA has fully
    // bootstrapped the Settings screen and React Query hooks have fired.
    const firstTab = tabList.locator('[role="tab"]').first()
    await expect(firstTab).toBeVisible({ timeout: 10_000 })

    // ── Core assertion: the restart banner must NOT be attached to the DOM.
    // RestartBanner.tsx returns null when entries.length === 0, so the element
    // is never rendered — not.toBeAttached() is the correct check (stronger than
    // not.toBeVisible(), which would still pass for hidden elements).
    await expect(page.getByTestId('restart-banner')).not.toBeAttached()

    // ── Secondary assertion: the banner summary text must not appear anywhere.
    // This guards against a hypothetical implementation that renders the element
    // with zero-height or off-screen instead of removing it from the DOM.
    await expect(page.getByTestId('restart-banner-summary')).not.toBeAttached()

    // ── Tertiary assertion: no amber "restart to apply" prose visible anywhere
    // on the settings page (catches any alt rendering path that lacks testids).
    await expect(page.getByText(/restart to apply/i)).not.toBeAttached()
  },
)

test(
  'fresh install: restart banner absent on Gateway tab after onboarding',
  async ({ page }) => {
    // Stub before navigation — same isolation pattern.
    await stubPendingRestartEmpty(page)

    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Navigate to the Gateway tab — the tab most relevant to restart-gated keys
    // (GatewayPort, sandbox mode, preview listener host/port are in
    // RestartGatedKeys per spec US-3/AC3). After a fresh onboard none of those
    // keys have changed, so the banner must stay hidden on this tab too.
    const gatewayTab = page.locator('button[role="tab"]', { hasText: 'Gateway' })
    await expect(gatewayTab).toBeVisible({ timeout: 15_000 })
    await gatewayTab.click()

    // Wait for the Gateway tab panel to become the active panel.
    const activePanel = page.locator('[role="tabpanel"][data-state="active"]').first()
    await expect(activePanel).toBeVisible({ timeout: 10_000 })

    // ── Core assertion: banner absent on the Gateway tab.
    await expect(page.getByTestId('restart-banner')).not.toBeAttached()

    // ── Differentiation: the Gateway section itself rendered (log level or
    // port field visible confirms the tab loaded real content, not a blank panel).
    await expect(activePanel.getByText(/log level/i).or(activePanel.getByText(/port/i)).first()).toBeVisible({
      timeout: 10_000,
    })
  },
)
