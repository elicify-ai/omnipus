/**
 * providers.spec.ts — E2E-2: provider list loads from the real backend.
 *
 * Covers:
 *   AC1 (US-2): Given an onboarded install (openrouter seeded by global-setup),
 *     When the Providers tab in Settings renders, Then the real provider list is
 *     non-empty, the seeded "openrouter" provider appears, and at least one
 *     [data-testid="connected-badge"] is present (because OPENROUTER_API_KEY_CI
 *     is a real key, so the backend resolves it and sets status:'connected').
 *
 *   Differentiation (anti-hardcode): The two tests below assert on distinct,
 *     real state — one checks that a connected badge is visible, the other checks
 *     the exact provider name "openrouter" that onboarding seeded. A hardcoded
 *     response would have to exactly mirror what the real backend returns from
 *     config.json, which is impossible without running the real server.
 *
 * Approach: real-backend, no page.route() stubs on /api/v1/providers.
 *   - The global storageState provides a pre-authenticated admin session.
 *   - onboarding (global-setup.ts → onboardViaAPI) seeds one openrouter provider
 *     with the OPENROUTER_API_KEY_CI key. The backend resolves this key from the
 *     credential store, sets status:'connected', and returns it in GET /api/v1/providers.
 *   - Asserting "openrouter" and [data-testid="connected-badge"] therefore validates
 *     the real provider endpoint schema, status logic, and credential resolution.
 *
 * Why not mock:
 *   A mocked /api/v1/providers response cannot catch:
 *     - Schema drift (missing or renamed fields in the generated Provider type)
 *     - Wrong status logic (e.g., connected badge missing when api_key_ref resolves)
 *     - Credential-store read failures at the REST layer
 *
 * Traces to: E2E-2 (SC-103) — provider with a configured key renders as connected.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import { expect, test } from '@playwright/test'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── E2E-2: connected provider from real backend ────────────────────────────────
// BDD: Given an onboarded install (openrouter provider seeded, real API key stored)
//      When the Providers tab in Settings renders
//      Then the providers list is non-empty (at least one provider row visible)
//      And [data-testid="connected-badge"] is present (status:'connected')
//      And the seeded provider name "openrouter" is displayed
//
// Traces to: E2E-2 / SC-103

test(
  'Providers tab loads real provider list — openrouter seeded by onboarding is visible and connected',
  async ({ page }) => {
    // Navigate to the Settings page. The Providers tab is the defaultValue
    // (settings.tsx — defaultValue="providers"), so no tab click needed.
    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Wait for the providers list to finish loading. The skeleton disappears when
    // the React Query fetch resolves; the connected badge is the first reliable
    // signal that the real data rendered (not the loading skeleton).
    //
    // ProvidersSection.tsx:98 renders:
    //   <Badge data-testid="connected-badge" variant="success">Connected</Badge>
    // only when provider.status === 'connected'. This fires only when the backend
    // resolved a non-empty API key for the provider.
    const connectedBadge = page.getByTestId('connected-badge').first()
    await expect(connectedBadge).toBeVisible({ timeout: 15_000 })

    // ── Core assertion A: the connected badge is present.
    // Verifies the backend credential resolution path ran and returned 'connected'.
    await expect(connectedBadge).toBeVisible()

    // ── Core assertion B: "openrouter" provider name is displayed.
    // The backend returns Name:"openrouter" for the onboarded provider.
    // ProvidersSection.tsx:81 renders: displayName = provider.display_name ?? provider.name ?? provider.id
    // Since the contract returns Name:"openrouter" and no display_name, the SPA renders "openrouter".
    const openrouterRow = page.getByText('openrouter', { exact: true }).first()
    await expect(openrouterRow).toBeVisible({ timeout: 10_000 })

    // ── Core assertion C: at least one "Edit" button (not "Configure").
    // ProvidersSection.tsx:141 renders "Edit" when connected=true.
    // If this were a disconnected provider (broken credential resolution), "Configure" appears.
    const editButton = page.getByRole('button', { name: /^edit$/i }).first()
    await expect(editButton).toBeVisible({ timeout: 5_000 })
  },
)

// ── Differentiation test ───────────────────────────────────────────────────────
// BDD: Given the same onboarded install
//      When the Providers tab renders
//      Then the provider count is >= 1 (real list, not empty fallback "Default")
//      And at least one provider row is visible (differentiation from empty state)
//      And the "Default" fallback provider is NOT present (a real provider replaced it)
//
// The backend returns a "Default" fallback entry ONLY when cfg.Providers is empty
// (rest.go:3467-3474). After onboarding, cfg.Providers has at least one real entry,
// so "Default" must be absent. This catches a regression where providers is cleared
// or the onboarding write fails silently.
//
// Traces to: E2E-2 / SC-103 (differentiation: real list != empty-install fallback)

test(
  'Providers tab shows real providers — no "Default" fallback after onboarding',
  async ({ page }) => {
    await page.goto(`${BASE_URL}/#/settings`)
    await expect(page).toHaveURL(/settings/, { timeout: 10_000 })

    // Wait for real providers to load. The "openrouter" entry is the anchor
    // because it is deterministically seeded by global-setup.ts onboardViaAPI().
    const openrouterRow = page.getByText('openrouter', { exact: true }).first()
    await expect(openrouterRow).toBeVisible({ timeout: 15_000 })

    // ── Differentiation assertion: "Default" fallback is absent.
    // A hardcoded empty response or a broken onboarding would produce the
    // "Default" fallback from rest.go:3468. Asserting its absence proves the
    // real configured provider list was returned.
    //
    // Use 'exact: true' to avoid false positives if any other element contains
    // the word "Default" (e.g., a badge or hint text).
    const defaultFallback = page.getByText('Default', { exact: true })
    await expect(defaultFallback).toHaveCount(0)

    // ── Count assertion: at least one provider row is in the DOM.
    // [data-testid="connected-badge"] or the "Edit" button both imply a real entry.
    const connectedBadgeCount = await page.getByTestId('connected-badge').count()
    expect(connectedBadgeCount).toBeGreaterThanOrEqual(1)
  },
)
