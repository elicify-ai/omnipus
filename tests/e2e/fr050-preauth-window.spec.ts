/**
 * fr050-preauth-window.spec.ts — the fresh-install auth posture, for real.
 *
 * WHY THIS FILE EXISTS
 *
 * Every other spec in this suite (including providers.spec.ts, which reads
 * the real provider catalog) runs against the ONE shared gateway
 * global-setup.ts boots — and that gateway is started with
 * `gateway.dev_mode_bypass: true` (tests/e2e/global-setup.ts:77,94 / the CI
 * "Seed gateway config" step in .github/workflows/pr.yml). Under
 * dev_mode_bypass, checkBearerAuth (pkg/gateway/auth.go) lets an
 * UNAUTHENTICATED request through unconditionally — so the entire rest of
 * this suite runs in the one auth posture in which an auth-gating defect on
 * a `withAuth` (rather than `withOptionalAuth`) route CANNOT surface: a route
 * that should 401 pre-onboarding instead sails through, and a route that
 * should reject an anonymous caller looks identical to one that correctly
 * allows one.
 *
 * That blind spot is exactly how `GET /api/v1/providers/catalog` shipped
 * registered `withAuth` (401 on every real fresh install — onboarding step
 * 3's provider picker rendered its error state and a new operator could not
 * pick a provider at all) with the full suite green: providers.spec.ts's
 * catalog-reading row runs against the bypass-enabled shared gateway, the one
 * posture where the bug is invisible, and every onboarding vitest test mocks
 * fetchProvidersCatalog so the wire is never exercised there either. See
 * docs/internal/false-green-patterns.md — a green suite that never visits
 * the real posture is not a green suite, it's an untested one.
 *
 * This file closes that gap by booting its OWN gateway instance — separate
 * OMNIPUS_HOME, separate ephemeral port (tests/e2e/setup.ts::getFreePort) —
 * with dev_mode_bypass left at its zero value (false, never written into
 * this instance's config.json) and no OMNIPUS_BEARER_TOKEN, and by NOT
 * calling onboardAdmin() before the first test runs (skipOnboarding: true).
 * That is the actual fresh-install posture: no configured users, no bypass,
 * onboarding incomplete. It follows the exact pattern hot-reload.spec.ts
 * already established for "this spec needs a gateway the shared one can't
 * give it" — a per-test gateway fixture, not a new global config — because
 * this repo already has that convention and duplicating global-setup.ts's
 * gateway-bootstrap machinery for one file would be a bigger, less
 * consistent change for the same result.
 *
 * `test.use({ storageState: { cookies: [], origins: [] } })` overrides the
 * suite-wide admin storageState (playwright.config.ts's `use.storageState`,
 * built for the SHARED gateway on OMNIPUS_URL): cookies are host-scoped, not
 * host:port-scoped (RFC 6265), so without this override the shared gateway's
 * `omnipus-session` cookie would ride along on requests to this file's
 * differently-ported gateway. It would fail to validate there (different
 * process, different signing state) and fall through to anonymous either
 * way — but overriding to a genuinely blank context removes any doubt about
 * what "anonymous" means in this file, matching hot-reload.spec.ts's own
 * documented rationale for the same override.
 *
 * WHAT THIS PROVES (traces to ADR-068 FR-050 /
 * pkg/gateway/rest_auth.go::preAuthOnboardingWindowOpen)
 *
 *   1. Fresh-install, anonymous: onboarding step 3 renders the provider
 *      picker from the REAL catalog endpoint — a Popular tile is visible,
 *      not merely "the GET returned 200" (a stubbed/hardcoded response could
 *      satisfy a bare status check; it cannot make ProviderPicker.tsx render
 *      a live catalog entry unless the real document arrived and passed the
 *      SPA's own zod validation).
 *   2. The FR-050 window closes: once onboarding completes
 *      (onboardingMgr.IsComplete() flips true), the identical anonymous
 *      request to the identical route is refused with 401 and the specific
 *      `{"error":"authentication required"}` body jsonErr produces — not
 *      just "some 4xx".
 *
 * MUTATION-TESTED (docs/internal/false-green-patterns.md checklist item 5):
 * reverting pkg/gateway/rest.go's catalog registration from
 * `a.withOptionalAuth(a.HandleProvidersCatalog)` back to
 * `a.withAuth(a.HandleProvidersCatalog)` makes test 1 fail — see the PR
 * description / session report for the transcript.
 */

import { test, expect } from '@playwright/test'
import { startGateway, stopGateway, getFreePort, onboardAdmin, type GatewayHandle } from './setup.js'

// Ordering matters: test 1 exercises the window OPEN, test 2 closes it and
// then re-probes. Both share one gateway instance (booted once in
// beforeAll) — serial mode keeps that dependency explicit rather than
// accidental.
test.describe.configure({ mode: 'serial' })

// This spec manages its own isolated, un-onboarded gateway — the suite-wide
// admin storageState (built for the shared gateway) must not apply here.
// See file header for why this is not merely defensive.
test.use({ storageState: { cookies: [], origins: [] } })

let handle: GatewayHandle

test.beforeAll(async () => {
  const port = await getFreePort()
  // skipOnboarding: true — this instance must boot with NO users configured
  // and onboarding incomplete, i.e. the real fresh-install posture. Every
  // other startGateway() caller (hot-reload.spec.ts) is unaffected: the
  // flag defaults to false there.
  handle = await startGateway({ port, skipOnboarding: true })
})

test.afterAll(async () => {
  await stopGateway(handle)
})

// ── FR-050 window OPEN: anonymous onboarding renders the real catalog ──────
// BDD: Given a fresh install (no users, no OMNIPUS_BEARER_TOKEN, no
//        dev_mode_bypass, onboarding incomplete)
//      When an anonymous browser opens the onboarding wizard and reaches
//        step 3
//      Then the provider picker renders at least one real "Popular" tile,
//        sourced from a 200 GET /api/v1/providers/catalog

test(
  'fresh install, no bypass: anonymous onboarding step 3 renders the real provider catalog',
  async ({ page }) => {
    const catalogStatuses: number[] = []
    page.on('response', (res) => {
      if (res.url().includes('/api/v1/providers/catalog')) {
        catalogStatuses.push(res.status())
      }
    })

    await page.goto(`${handle.baseURL}/#/onboarding`)
    await expect(page.getByText('Step 1 of 3').first()).toBeVisible({ timeout: 15_000 })

    await page.locator('#admin-username').fill('fresh-install-admin')
    await page.getByRole('button', { name: /^continue$/i }).click()
    await page.locator('#admin-password').fill('fresh-install-passw0rd!')
    await page.locator('#admin-password-confirm').fill('fresh-install-passw0rd!')
    await page.getByRole('button', { name: /^continue$/i }).click()

    // ── Core assertion: a real Popular tile rendered. ProviderPicker.tsx
    // only emits [data-testid^="picker-popular-"] rows from catalog entries
    // it actually received and validated — this is the assertion the
    // reviewer specifically asked for, deliberately stronger than "the GET
    // returned 200" (an empty 200 body, or one that fails the SPA's zod
    // edge, renders zero tiles and this line times out).
    const tiles = page.locator('[data-testid^="picker-popular-"]')
    await expect(tiles.first()).toBeVisible({ timeout: 15_000 })
    expect(
      await tiles.count(),
      'no Popular tiles rendered — the anonymous catalog GET did not deliver usable data',
    ).toBeGreaterThan(0)

    // ── Secondary assertion: the wire call this proves is possible at all —
    // an anonymous GET to the real route returned 200, not 401.
    await expect.poll(() => catalogStatuses.length).toBeGreaterThanOrEqual(1)
    expect(
      catalogStatuses.every((s) => s === 200),
      `expected every anonymous GET /providers/catalog to return 200, got: ${catalogStatuses.join(', ')}`,
    ).toBe(true)
  },
)

// ── FR-050 window CLOSES once onboarding completes ──────────────────────────
// BDD: Given the same fresh install, still pre-onboarding
//      When onboarding completes (POST /api/v1/onboarding/complete)
//      Then a subsequent anonymous GET /api/v1/providers/catalog is refused
//        with 401 and the specific "authentication required" body —
//        the pre-auth window closes, it does not stay open forever.

test(
  'window closes: after onboarding completes, the identical anonymous catalog request is refused',
  async ({ request }) => {
    // Sanity check on THIS test's own gateway state (not a re-assertion of
    // test 1's DOM result — a raw fetch through Playwright's APIRequestContext,
    // proving the window is still open immediately before we close it).
    const before = await request.get(`${handle.baseURL}/api/v1/providers/catalog`)
    expect(
      before.status(),
      'expected the FR-050 pre-auth window to still be open before onboarding completes',
    ).toBe(200)

    await onboardAdmin(handle.baseURL, handle.adminUsername, handle.adminPassword)

    // ── Core assertion: the identical anonymous request the picker relied
    // on in test 1 is now refused. No Authorization header, no cookie —
    // request.get() here shares this file's storageState override (blank).
    const after = await request.get(`${handle.baseURL}/api/v1/providers/catalog`)
    expect(
      after.status(),
      'the FR-050 pre-auth window did not close after onboarding completed — ' +
        'anonymous catalog access is still allowed post-onboarding',
    ).toBe(401)

    // ── Content assertion, not just status (docs/internal/false-green-patterns.md):
    // the specific jsonErr(w, http.StatusUnauthorized, "authentication required")
    // body from requireAuthOutsideOnboarding, not merely "some 4xx".
    const body = (await after.json()) as { error?: string }
    expect(body.error).toContain('authentication required')
  },
)
