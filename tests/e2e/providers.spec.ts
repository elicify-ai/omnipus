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
import { stubOnboarding } from './fixtures/onboarding-stubs'

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
    // ADR-031 (91825d2f): badges are per-provider — connected-badge-<id>.
    // Prefix locator keeps the assertion robust to the seeded provider id.
    const connectedBadge = page.locator('[data-testid^="connected-badge-"]').first()
    await expect(connectedBadge).toBeVisible({ timeout: 15_000 })

    // ── Core assertion A: the connected badge is present.
    // Verifies the backend credential resolution path ran and returned 'connected'.
    await expect(connectedBadge).toBeVisible()

    // ── Core assertion B: "openrouter" provider name is displayed.
    // The backend returns Name:"openrouter" for the onboarded provider.
    // ProvidersSection.tsx:81 renders: displayName = provider.display_name ?? provider.name ?? provider.id
    // Since the contract returns Name:"openrouter" and no display_name, the SPA renders "openrouter".
    // ADR-031 rows render the catalog label / display_name ("OpenRouter"),
    // not the raw provider id — match case-insensitively.
    const openrouterRow = page.getByText(/openrouter/i).first()
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
    // ADR-031 rows render the catalog label / display_name ("OpenRouter"),
    // not the raw provider id — match case-insensitively.
    const openrouterRow = page.getByText(/openrouter/i).first()
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
    const connectedBadgeCount = await page.locator('[data-testid^="connected-badge-"]').count()
    expect(connectedBadgeCount).toBeGreaterThanOrEqual(1)
  },
)

// ─────────────────────────────────────────────────────────────────────────────
// ADR-068 T068-31 — the provider rows that are writable on this branch.
//
// FR-037 / MAJ-004: the SPA reads its provider list from
// `GET /api/v1/providers/catalog` (ADR-067's route), never from a bundled TS
// module, and re-validates with `If-None-Match` so at most ONE 200 is served
// per ETag value within a page session.
//
// DELIBERATELY NOT WRITTEN YET (the task's `depends-on` list is not fully
// landed on this branch — T068-25 Settings → Providers, T068-26 the remove
// dialog, T068-27 *Check with my account*):
//   • "Remove an unused provider" — needs the rebuilt screen's row menu and the
//     confirm dialog; DELETE has no UI caller yet.
//   • "Change default model takes effect on the next turn" — needs the Default
//     model card (FR-019), which lives only on the rebuilt screen.
//   • "Check with my account" (FR-031) — needs the entitlement button on the
//     rebuilt row.
// Each of those rows would today assert against markup that does not exist, so
// they are named here rather than written as tests that pass by finding
// nothing (docs/internal/false-green-patterns.md).
// ─────────────────────────────────────────────────────────────────────────────

test('the picker reads the catalog from the GET, and serves at most one 200 per ETag', async ({
  page,
}) => {
  // Only `/state` is stubbed — the catalog comes from the real gateway, which
  // is the whole point of this row.
  await stubOnboarding(page, { catalog: 'real' })

  const catalogResponses: Array<{ status: number; etag: string | undefined }> = []
  page.on('response', (res) => {
    if (!res.url().includes('/api/v1/providers/catalog')) return
    catalogResponses.push({ status: res.status(), etag: res.headers()['etag'] })
  })

  await page.goto(`${BASE_URL}/#/onboarding`)
  await expect(page.getByText('Step 1 of 3').first()).toBeVisible({ timeout: 15_000 })
  await page.locator('#admin-username').fill('catalog-probe-admin')
  await page.getByRole('button', { name: /^continue$/i }).click()
  await page.locator('#admin-password').fill('catalog-passw0rd!')
  await page.locator('#admin-password-confirm').fill('catalog-passw0rd!')
  await page.getByRole('button', { name: /^continue$/i }).click()

  // The picker rendered from whatever the gateway served — Popular tiles exist,
  // which is only possible if a real catalog document arrived.
  const tiles = page.locator('[data-testid^="picker-popular-"]')
  await expect(tiles.first()).toBeVisible({ timeout: 15_000 })
  expect(await tiles.count(), 'the Popular band is empty — no catalog was read').toBeGreaterThan(0)

  // The GET happened, returned 200 and carried an ETag (ADR-067 A-1).
  await expect.poll(() => catalogResponses.length).toBeGreaterThanOrEqual(1)
  const twoHundreds = catalogResponses.filter((r) => r.status === 200)
  expect(twoHundreds.length, 'the catalog GET never returned 200').toBeGreaterThanOrEqual(1)
  expect(twoHundreds[0].etag, 'the catalog GET served no ETag to re-validate against').toBeTruthy()

  // Navigate away and back: the second visit must NOT pull a second 200 for the
  // same ETag — either nothing goes out, or the re-validation returns 304.
  // Same-document hash navigation, deliberately NOT page.goto: a full document
  // load would reset the SPA's in-memory ETag cache and this row would then be
  // measuring the harness rather than the re-validation policy.
  await page.evaluate(() => {
    window.location.hash = '#/agents'
  })
  await expect(page).toHaveURL(/agents/, { timeout: 10_000 })
  await page.evaluate(() => {
    window.location.hash = '#/onboarding'
  })
  await expect(page.getByText('Step 1 of 3').first()).toBeVisible({ timeout: 15_000 })

  const byEtag = new Map<string, number>()
  for (const r of catalogResponses) {
    if (r.status !== 200) continue
    const key = r.etag ?? '<none>'
    byEtag.set(key, (byEtag.get(key) ?? 0) + 1)
  }
  for (const [etag, count] of byEtag) {
    expect(count, `catalog ETag ${etag} was served ${count} times with a 200`).toBeLessThanOrEqual(1)
  }
})
