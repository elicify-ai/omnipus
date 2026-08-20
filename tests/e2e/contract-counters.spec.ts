/**
 * contract-counters.spec.ts — Schema-validation health after a real app flow.
 *
 * Asserts that during a normal authenticated page load + navigation:
 *   - No WS frames were dropped (schema validation failure at the SPA edge)
 *   - No API responses failed Zod schema validation
 *   - No WS frames had an unrecognised type field
 *
 * The counters live on window.__omnipus_test_hooks. src/lib/ws.ts and
 * src/lib/api.ts expose them whenever import.meta.env.DEV === true,
 * MODE === 'test', OR the browser reports navigator.webdriver === true.
 * That third condition is what makes the hooks available in THIS spec, which
 * runs against the EMBEDDED PRODUCTION BINARY (MODE=production): Playwright's
 * Chromium always sets navigator.webdriver = true, so WebDriver-controlled
 * automation gets the hooks even though a real end user's browser — which
 * does not set that flag — never does. This is the same opt-in surface
 * src/lib/ws.ts already relies on for window.__ws_instances (see
 * ws-reconnect.spec.ts), applied here to the schema-validation counters.
 * Verified directly in the built bundle: `grep -o 'navigator.webdriver.\{0,60\}'
 * pkg/gateway/spa/assets/index-*.js` shows the minified production build still
 * carries the check (the DEV/test literals get folded away by Terser, leaving
 * just the navigator.webdriver branch).
 *
 * Because the hooks are expected to be present under any Playwright run
 * (against dev server or production binary alike), their absence is now a
 * hard test FAILURE, not a skip — see the `hooksAvailable` assertion below.
 * Previously this test soft-skipped itself when hooks were missing (tracked
 * in SKIP_ALLOWLIST, issue #155); that assumption was stale and the entry has
 * been removed — no soft-skip is permitted here per skip-tracking.ts policy.
 */

import { test, expect, type Page } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'

// Auth helpers — mirror the pattern from tests/e2e/cancel-cross-channel.spec.ts
// so POST /api/v1/sessions succeeds (it requires both Authorization and the
// CSRF cookie). Inlined rather than exported from a shared util to keep this
// spec self-contained.

function getStoredAuthToken(): string | null {
  const authFile = process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(
        path.dirname(new URL(import.meta.url).pathname),
        'fixtures/.auth/admin.json',
      )
  if (!fs.existsSync(authFile)) return null
  try {
    const raw = fs.readFileSync(authFile, 'utf-8')
    const state = JSON.parse(raw) as {
      origins?: Array<{
        origin: string
        localStorage?: Array<{ name: string; value: string }>
      }>
    }
    for (const origin of state.origins ?? []) {
      for (const entry of origin.localStorage ?? []) {
        if (entry.name === 'omnipus_auth_token') return entry.value
      }
    }
  } catch {
    return null
  }
  return null
}

async function getCsrfToken(page: Page): Promise<string | null> {
  const cookies = await page.context().cookies()
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  return csrfCookie?.value ?? null
}

async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const authToken = getStoredAuthToken()
  const csrfToken = await getCsrfToken(page)
  return {
    'Content-Type': 'application/json',
    ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
    ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
  }
}

test('no schema-validation errors during authenticated page load + navigation', async ({ page }) => {
  // Navigate to the dashboard. Global storageState provides pre-authenticated session.
  await page.goto('/')

  // Wait for the app shell to render (banner landmark = AppShell header).
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 20_000 })

  // The hooks must be present here — Playwright's Chromium sets
  // navigator.webdriver=true, which src/lib/ws.ts / src/lib/api.ts both
  // check to expose window.__omnipus_test_hooks even in production builds
  // (see header comment). If this is ever false, the test-hook gate itself
  // has regressed — fail hard rather than silently skipping.
  const hooksAvailable = await page.evaluate(() => {
    const w = window as unknown as { __omnipus_test_hooks?: Record<string, unknown> }
    return typeof w.__omnipus_test_hooks?.getDroppedFrameCount === 'function'
  })
  expect(
    hooksAvailable,
    'window.__omnipus_test_hooks was not exposed. Expected navigator.webdriver=true ' +
      '(set by Playwright automation) to trigger the test-hook gate in src/lib/ws.ts ' +
      'and src/lib/api.ts even against the production build. If this assertion fails, ' +
      'that gate has regressed — this must be fixed, not re-skipped.',
  ).toBe(true)

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
test('navigating to a session with tool_call + turn_canceled entries fires no ApiSchemaError', async ({ page }) => {
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

  // Use the existing default agent ("main") to create a session. Must pass
  // both Authorization (Bearer) and the CSRF token via the shared helper —
  // bare page.request.post() 403s on the CSRF guard.
  const createResp = await page.request.post('/api/v1/sessions', {
    headers: await apiHeaders(page),
    data: { agent_id: 'mia', type: 'chat' },
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
    JSON.stringify({ id: 'msg-user-1', role: 'user', content: 'hi', timestamp: ts, agent_id: 'mia' }),
    JSON.stringify({
      id: 'call_regression_001',
      type: 'tool_call',
      timestamp: ts,
      agent_id: 'mia',
      tool_calls: [{ id: 'call_regression_001', tool: 'write_file', status: 'success' }],
    }),
    JSON.stringify({
      id: 'cancel_regression_001',
      type: 'turn_canceled',
      timestamp: ts,
      agent_id: 'mia',
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
