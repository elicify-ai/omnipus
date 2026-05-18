/**
 * contract-counters.spec.ts — Schema-validation health after a real app flow.
 *
 * Asserts that during a normal authenticated page load + navigation:
 *   - No WS frames were dropped (schema validation failure at the SPA edge)
 *   - No API responses failed Zod schema validation
 *   - No WS frames had an unrecognised type field
 *
 * The counters are exposed on window.__omnipus_test_hooks only when the SPA
 * is built with import.meta.env.DEV === true or MODE === 'test' (see src/lib/ws.ts
 * and src/lib/api.ts). The embedded production binary uses MODE=production, so
 * the hooks are absent there. When they are absent (getDroppedFrameCount returns
 * -1 via the null-coalesce in the evaluate), the test skips itself using
 * test.skip() — documented in SKIP_ALLOWLIST in skip-tracking.ts.
 *
 * To run this test against a dev-mode Vite server (where hooks ARE present):
 *   npx playwright test contract-counters --project=chromium
 */

import { test, expect } from '@playwright/test'

test('no schema-validation errors during authenticated page load + navigation', async ({ page }) => {
  // Navigate to the dashboard. Global storageState provides pre-authenticated session.
  await page.goto('/')

  // Wait for the app shell to render (banner landmark = AppShell header).
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 20_000 })

  // Check whether __omnipus_test_hooks are available (only in dev builds).
  const hooksAvailable = await page.evaluate(() => {
    const w = window as unknown as { __omnipus_test_hooks?: Record<string, unknown> }
    return typeof w.__omnipus_test_hooks?.getDroppedFrameCount === 'function'
  })

  if (!hooksAvailable) {
    test.skip(
      true,
      'window.__omnipus_test_hooks not present — production build does not expose counters. ' +
        'Run against a Vite dev server (MODE=development) to exercise this test. ' +
        'See SKIP_ALLOWLIST entry in skip-tracking.ts.'
    )
    return
  }

  // Navigate to a second route to trigger more WS traffic and API calls.
  await page.goto('/#/settings')
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 10_000 })

  // Navigate back to the chat root.
  await page.goto('/')
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 10_000 })

  // Read counters from the module-level singletons via the test hooks.
  const droppedFrames = await page.evaluate(() => {
    const w = window as unknown as { __omnipus_test_hooks?: Record<string, unknown> }
    const fn = w.__omnipus_test_hooks?.getDroppedFrameCount
    return typeof fn === 'function' ? (fn as () => number)() : -1
  })

  const apiErrors = await page.evaluate(() => {
    const w = window as unknown as { __omnipus_test_hooks?: Record<string, unknown> }
    const fn = w.__omnipus_test_hooks?.getApiSchemaErrorCount
    return typeof fn === 'function' ? (fn as () => number)() : -1
  })

  const unknownTypes = await page.evaluate(() => {
    const w = window as unknown as { __omnipus_test_hooks?: Record<string, unknown> }
    const fn = w.__omnipus_test_hooks?.getUnknownFrameTypeCount
    return typeof fn === 'function' ? (fn as () => number)() : -1
  })

  // When hooks are present, all three counters must be 0.
  // A non-zero value means a wire-format schema mismatch made it to production.
  expect(droppedFrames, 'WS frames dropped due to Zod schema failure').toBe(0)
  expect(apiErrors, 'API responses that failed Zod schema validation').toBe(0)
  expect(unknownTypes, 'WS frames with unrecognised type field').toBe(0)
})
