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
import * as fs from 'fs'
import * as path from 'path'

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

// Regression guard for the 2026-05-21 production bug: navigating to a session
// containing tool_call entries via the chat-history loader (the REST path,
// NOT the WS replay path) failed SPA Zod validation with "Backend response
// failed validation. Server may be a different version." This test seeds a
// transcript on disk with the exact entry shapes that broke production
// (tool_call + turn_canceled with cancel-specific fields), navigates via the
// SPA loader, and asserts no ApiSchemaError is logged to console.
//
// This test does NOT depend on __omnipus_test_hooks — it works on both dev
// and production builds because console.error('[apiSchemaError]', ...) is
// emitted unconditionally from src/lib/queryClient.ts.
//
// Traces to: Bug-1 / contracts/components/schemas/Message.yaml type enum.
test('navigating to a session with tool_call + turn_canceled entries fires no ApiSchemaError', async ({ page, request }) => {
  // Capture every console.error from page load through navigation.
  const schemaErrors: string[] = []
  page.on('console', (msg) => {
    if (msg.type() === 'error' && msg.text().includes('apiSchemaError')) {
      schemaErrors.push(msg.text())
    }
  })

  await page.goto('/')
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 20_000 })

  // Create a fresh session via REST. The global storageState provides the
  // bearer token; use page.request (or just bare fetch via evaluate) so the
  // request carries that auth.
  const omnipusHome = process.env.OMNIPUS_HOME
  if (!omnipusHome) {
    test.skip(true, 'OMNIPUS_HOME env var must be set for this test (it usually is via CI)')
    return
  }

  // Use the existing default agent ("main") to create a session.
  const createResp = await page.request.post('/api/v1/sessions', {
    data: { agent_id: 'main', type: 'chat' },
  })
  expect(createResp.status(), 'must be able to create a session').toBe(201)
  const created = await createResp.json()
  const sessionId: string = created.id
  expect(sessionId, 'session must have an id').toBeTruthy()

  // Directly append the offending entry shapes to the transcript file. The
  // gateway reads transcripts from disk so this exercise the exact code path
  // a real user would hit.
  const sessionDir = path.join(omnipusHome, 'sessions', sessionId)
  // Find the day-partition file (any *.jsonl that's not transcript.jsonl).
  // For freshly-created sessions, write to today's date.
  const today = new Date().toISOString().slice(0, 10) // YYYY-MM-DD
  const partition = path.join(sessionDir, `${today}.jsonl`)
  fs.mkdirSync(sessionDir, { recursive: true })

  const ts = new Date().toISOString()
  const lines = [
    JSON.stringify({ id: 'msg-user-1', role: 'user', content: 'hi', timestamp: ts, agent_id: 'main' }),
    JSON.stringify({
      id: 'call_regression_001',
      type: 'tool_call',
      timestamp: ts,
      agent_id: 'main',
      tool_calls: [{ id: 'call_regression_001', tool: 'write_file', status: 'success' }],
    }),
    JSON.stringify({
      id: 'cancel_regression_001',
      type: 'turn_canceled',
      timestamp: ts,
      agent_id: 'main',
      turn_id: 'turn-T-regression',
      canceled_by_user: 'admin',
      canceled_by_channel: 'webchat',
      cancel_method: 'graceful',
      descendants_canceled: ['turn-T-regression-sub-1'],
    }),
  ]
  fs.writeFileSync(partition, lines.join('\n') + '\n')

  // Navigate to the session via the SPA loader path (the route that calls
  // fetchSessionDetail and threw on the production bug).
  await page.goto(`/#/sessions/${sessionId}`)

  // Wait for the chat area to render OR the error boundary to surface.
  // 5s budget — fast feedback for the regression.
  await page.waitForTimeout(5_000)

  // Assert: no apiSchemaError fired during the load.
  expect(schemaErrors, 'navigating to a session with tool_call/turn_canceled entries must not fire apiSchemaError').toEqual([])
})
